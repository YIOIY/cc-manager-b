package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestWebBillWindow(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	at := time.Date(2026, 11, 11, 1, 0, 0, 0, loc)
	start, end := webBillWindow(at, loc)
	if start != "2026-11-05" || end != "2026-11-11" {
		t.Fatalf("window %s..%s", start, end)
	}
	// 2026-11-10 23:30 UTC is already the 11th in Shanghai.
	start, end = webBillWindow(time.Date(2026, 11, 10, 23, 30, 0, 0, time.UTC), loc)
	if start != "2026-11-05" || end != "2026-11-11" {
		t.Fatalf("utc window %s..%s", start, end)
	}
}

// billRow renders one /bill/query list entry.
func billRow(id int, day, model string, cost string) string {
	ds := strings.ReplaceAll(day, "-", "")
	return fmt.Sprintf(`{"account_alias":"a","account_id":%d,"bill_model_name":%q,"cost":%s,"ds":%s,"ds_text":%q,"model_name":%q}`, id, model, cost, ds, day, model)
}

func billBody(rows []string, start, end string) string {
	return `{"accounts":[],"by_account":[],"by_model":[],"list":[` + strings.Join(rows, ",") + `],"range":{"start_ds":` + strings.ReplaceAll(start, "-", "") + `,"end_ds":` + strings.ReplaceAll(end, "-", "") + `},"summary":{}}`
}

func billsFixture(t *testing.T) (*App, *webConsoleStub, string, string) {
	t.Helper()
	a, stub := webFixture(t)
	loc, _ := time.LoadLocation("Asia/Shanghai")
	today := time.Now().In(loc).Format("2006-01-02")
	yesterday := time.Now().In(loc).AddDate(0, 0, -1).Format("2006-01-02")
	stub.setAccounts(
		remoteAccount(428802, "one@example.test", "using", "2026-11-01 10:00", nil, nil),
		remoteAccount(428242, "two@example.test", "using", "2026-11-02 10:00", nil, nil),
	)
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	return a, stub, today, yesterday
}

func billModelRows(t *testing.T, a *App) map[string]string {
	t.Helper()
	rows, err := a.DB.Query(`SELECT upstream_id,bill_date,model_name,cost_fen,generation,final,account_id FROM platform_bill_models ORDER BY upstream_id,bill_date,model_name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var upstream, day, model, account string
		var fen, generation, final int
		if err = rows.Scan(&upstream, &day, &model, &fen, &generation, &final, &account); err != nil {
			t.Fatal(err)
		}
		out[upstream+"/"+day+"/"+model] = fmt.Sprintf("fen=%d gen=%d final=%d account=%s", fen, generation, final, account)
	}
	return out
}

func TestWebBillsSyncUpsertsDeletesAndFinal(t *testing.T) {
	a, stub, today, yesterday := billsFixture(t)
	stub.mu.Lock()
	stub.bills = func(ids []string, start, end string) string {
		if strings.Join(ids, ",") != "428242,428802" {
			t.Errorf("ids=%v", ids)
		}
		return billBody([]string{billRow(428802, today, "claude-opus-5", "27.61"), billRow(428802, today, "claude-opus-5-5", "7.60"), billRow(428242, yesterday, "claude-opus-5", "0.92"), billRow(428242, yesterday, "claude-opus-5-5", "78.04")}, start, end)
	}
	stub.mu.Unlock()
	if err := a.syncWebBills(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	rows := billModelRows(t, a)
	if len(rows) != 4 {
		t.Fatalf("rows=%v", rows)
	}
	oneID := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	if rows["428802/"+today+"/claude-opus-5"] != "fen=2761 gen=1 final=0 account="+oneID {
		t.Fatalf("today row: %s", rows["428802/"+today+"/claude-opus-5"])
	}
	if !strings.HasPrefix(rows["428242/"+yesterday+"/claude-opus-5-5"], "fen=7804 gen=1 final=1") {
		t.Fatalf("yesterday row: %s", rows["428242/"+yesterday+"/claude-opus-5-5"])
	}
	state, code, processed, failed := lastRun(t, a, "web-bills")
	if state != "succeeded" || code != "" || processed != 4 || failed != 0 {
		t.Fatalf("run state=%s code=%q processed=%d failed=%d", state, code, processed, failed)
	}
	// Second round: the console dropped one row and revised another.
	stub.mu.Lock()
	stub.bills = func(ids []string, start, end string) string {
		return billBody([]string{billRow(428802, today, "claude-opus-5", "30.00"), billRow(428242, yesterday, "claude-opus-5", "0.92"), billRow(428242, yesterday, "claude-opus-5-5", "78.04")}, start, end)
	}
	stub.mu.Unlock()
	if err := a.syncWebBills(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	rows = billModelRows(t, a)
	if len(rows) != 3 || !strings.HasPrefix(rows["428802/"+today+"/claude-opus-5"], "fen=3000 gen=2") || rows["428802/"+today+"/claude-opus-5-5"] != "" {
		t.Fatalf("after revision rows=%v", rows)
	}
	// Third round: a range mismatch leaves the table alone.
	stub.mu.Lock()
	stub.bills = func(ids []string, start, end string) string {
		return billBody([]string{billRow(428802, today, "claude-opus-5", "99.99")}, "2026-01-01", end)
	}
	stub.mu.Unlock()
	if err := a.syncWebBills(context.Background(), "conn"); err == nil {
		t.Fatal("range mismatch must be reported")
	}
	if got := billModelRows(t, a); len(got) != 3 || got["428802/"+today+"/claude-opus-5"] != rows["428802/"+today+"/claude-opus-5"] {
		t.Fatalf("range mismatch changed rows: %v", got)
	}
	state, code, _, failed = lastRun(t, a, "web-bills")
	// The only batch failed, so nothing was processed: that is a failed run,
	// not a partial one.
	if state != "failed" || code != "WEB_BILL_RANGE_MISMATCH" || failed != 2 {
		t.Fatalf("mismatch run state=%s code=%s failed=%d", state, code, failed)
	}
}

func TestWebBillsSyncIgnoresRowsOutsideWindow(t *testing.T) {
	a, stub, today, _ := billsFixture(t)
	old := time.Now().AddDate(0, 0, -8).Format("2006-01-02")
	stub.mu.Lock()
	stub.bills = func(ids []string, start, end string) string {
		return billBody([]string{billRow(428802, today, "claude-opus-5", "1.00"), billRow(428802, old, "claude-opus-5", "5.00")}, start, end)
	}
	stub.mu.Unlock()
	if err := a.syncWebBills(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	if rows := billModelRows(t, a); len(rows) != 1 || rows["428802/"+old+"/claude-opus-5"] != "" {
		t.Fatalf("rows=%v", rows)
	}
}

func TestWebBillsSyncBatchesOf50(t *testing.T) {
	a, stub := webFixture(t)
	var many []map[string]any
	for i := 1; i <= 120; i++ {
		many = append(many, remoteAccount(9000+i, fmt.Sprintf("b%d@example.test", i), "using", "2026-11-01 10:00", nil, nil))
	}
	stub.setAccounts(many...)
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	var sizes []int
	stub.mu.Lock()
	stub.bills = func(ids []string, start, end string) string {
		sizes = append(sizes, len(ids))
		return billBody(nil, start, end)
	}
	stub.mu.Unlock()
	if err := a.syncWebBills(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	if stub.billHits.Load() != 3 || fmt.Sprint(sizes) != "[50 50 20]" {
		t.Fatalf("hits=%d sizes=%v", stub.billHits.Load(), sizes)
	}
}

func TestWebSyncsOfOneConnectionDoNotOverlap(t *testing.T) {
	a, _ := webFixture(t)
	lock := a.webSyncLock("conn")
	lock.Lock()
	defer lock.Unlock()
	if err := a.syncWebBills(context.Background(), "conn"); err != nil {
		t.Fatalf("a busy connection must be skipped, not failed: %v", err)
	}
	if queryInt(t, a, `SELECT COUNT(*) FROM sync_runs WHERE kind='web-bills'`) != 0 {
		t.Fatal("skipped sync wrote a run")
	}
}
