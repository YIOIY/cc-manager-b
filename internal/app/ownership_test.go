package app

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cc-manager/internal/errs"
)

// ownershipUsers boots an app with an admin plus two operators.
func ownershipUsers(t *testing.T) (a *App, admin, opsA, opsB *testClient) {
	t.Helper()
	t.Setenv("CC_MANAGER_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	a, admin = rbacApp(t)
	for _, u := range []struct{ name, pass string }{
		{"ops-a", "operator-password-a"},
		{"ops-b", "operator-password-b"},
	} {
		if res := admin.do(http.MethodPost, "/api/v1/users", `{"username":"`+u.name+`","password":"`+u.pass+`","role_ids":["`+roleOperatorID+`"]}`); res.StatusCode != http.StatusCreated {
			t.Fatalf("create %s=%d", u.name, res.StatusCode)
		}
	}
	opsA, opsB = newTestClient(t, a), newTestClient(t, a)
	opsA.loginAs("ops-a", "operator-password-a")
	opsB.loginAs("ops-b", "operator-password-b")
	return a, admin, opsA, opsB
}

func TestOwnershipScopesAccountsProxiesJobsDashboard(t *testing.T) {
	a, admin, opsA, opsB := ownershipUsers(t)

	proxyA := createOwnedProxy(t, opsA, "proxy-a")
	proxyB := createOwnedProxy(t, opsB, "proxy-b")
	accA := createOwnedAccount(t, opsA, "a@example.test", proxyA)
	accB := createOwnedAccount(t, opsB, "b@example.test", proxyB)
	if _, err := a.DB.Exec(`INSERT INTO accounts(id,account,plan,created_at,updated_at,created_by_user_id) VALUES('orphan','orphan@example.test','claude/cc-pro',?,?,?)`, now(), now(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`INSERT INTO jobs(id,kind,state,account_id,created_at,updated_at) VALUES('job-a','account.submit','succeeded',?,?,?)`, accA, now(), now()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`INSERT INTO jobs(id,kind,state,account_id,created_at,updated_at) VALUES('job-b','account.submit','failed',?,?,?)`, accB, now(), now()); err != nil {
		t.Fatal(err)
	}

	// Operator A sees only own account / proxy / job.
	assertListIDs(t, opsA.do(http.MethodGet, "/api/v1/accounts?page=1&page_size=20", ""), []string{accA})
	assertListIDs(t, opsA.do(http.MethodGet, "/api/v1/proxies", ""), []string{proxyA})
	assertListIDs(t, opsA.do(http.MethodGet, "/api/v1/lookups/proxies", ""), []string{proxyA})
	assertListIDs(t, opsA.do(http.MethodGet, "/api/v1/jobs?page=1&page_size=20", ""), []string{"job-a"})

	// Operator B cannot see or mutate A's resources.
	assertEnvelope(t, opsB.do(http.MethodGet, "/api/v1/accounts/"+accA, ""), http.StatusNotFound, "NOT_FOUND")
	assertEnvelope(t, opsB.do(http.MethodPatch, "/api/v1/accounts/"+accA, `{"plan":"claude/cc-max-5x","expected_version":1}`), http.StatusNotFound, "NOT_FOUND")
	assertEnvelope(t, opsB.do(http.MethodGet, "/api/v1/proxies/"+proxyA+"/checks", ""), http.StatusNotFound, "NOT_FOUND")

	// Operator A can mutate own account; admin sees A's account but cannot edit it.
	if res := opsA.do(http.MethodPatch, "/api/v1/accounts/"+accA, `{"plan":"claude/cc-max-5x","expected_version":1}`); res.StatusCode != http.StatusOK {
		t.Fatalf("owner patch=%d", res.StatusCode)
	}
	assertEnvelope(t, admin.do(http.MethodPatch, "/api/v1/accounts/"+accA, `{"plan":"claude/cc-pro","expected_version":2}`), http.StatusForbidden, "PERMISSION_DENIED")
	if res := admin.do(http.MethodGet, "/api/v1/accounts/"+accA, ""); res.StatusCode != http.StatusOK {
		t.Fatalf("admin get other=%d", res.StatusCode)
	}

	// Empty-creator accounts are admin-only; admin may mutate them.
	assertEnvelope(t, opsA.do(http.MethodGet, "/api/v1/accounts/orphan", ""), http.StatusNotFound, "NOT_FOUND")
	if res := admin.do(http.MethodGet, "/api/v1/accounts/orphan", ""); res.StatusCode != http.StatusOK {
		t.Fatalf("admin get orphan=%d", res.StatusCode)
	}
	if res := admin.do(http.MethodPatch, "/api/v1/accounts/orphan", `{"plan":"claude/cc-max-5x","expected_version":1}`); res.StatusCode != http.StatusOK {
		t.Fatalf("admin patch orphan=%d", res.StatusCode)
	}

	// Cannot bind someone else's proxy.
	assertEnvelope(t, opsA.do(http.MethodPatch, "/api/v1/accounts/"+accA, `{"proxy_id":"`+proxyB+`","expected_version":2}`), http.StatusUnprocessableEntity, "PROXY_INVALID")

	// Admin dashboard sees everyone; operator dashboard is scoped.
	var dash struct {
		Data struct {
			Accounts struct {
				Total int `json:"total"`
			} `json:"accounts"`
			Proxies struct {
				Enabled int `json:"enabled"`
			} `json:"proxies"`
			Jobs struct {
				Succeeded int `json:"succeeded"`
				Failed    int `json:"failed"`
			} `json:"jobs"`
		} `json:"data"`
	}
	_ = jsonDecode(admin.do(http.MethodGet, "/api/v1/dashboard", ""), &dash)
	if dash.Data.Accounts.Total < 3 || dash.Data.Proxies.Enabled < 2 {
		t.Fatalf("admin dashboard=%+v", dash.Data)
	}
	_ = jsonDecode(opsA.do(http.MethodGet, "/api/v1/dashboard", ""), &dash)
	if dash.Data.Accounts.Total != 1 || dash.Data.Proxies.Enabled != 1 || dash.Data.Jobs.Succeeded != 1 || dash.Data.Jobs.Failed != 0 {
		t.Fatalf("opsA dashboard=%+v", dash.Data)
	}
}

func TestOwnershipScopesBillAccounts(t *testing.T) {
	a, admin, opsA, opsB := ownershipUsers(t)
	accA := createOwnedAccount(t, opsA, "bill-a@example.test", "")
	accB := createOwnedAccount(t, opsB, "bill-b@example.test", "")
	if _, err := a.DB.Exec(`INSERT INTO platform_connections(id,name,base_url,timezone,enabled,version,created_at,updated_at) VALUES('bill-conn','Bill','https://x.example','Asia/Shanghai',1,1,?,?)`, now(), now()); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ id, account, upstream string }{
		{"link-a", accA, "1001"},
		{"link-b", accB, "1002"},
	} {
		if _, err := a.DB.Exec(`INSERT INTO platform_links(id,account_id,connection_id,upstream_id,ingestion_state,created_at,updated_at) VALUES(?,?,'bill-conn',?,'accepted',?,?)`, row.id, row.account, row.upstream, now(), now()); err != nil {
			t.Fatal(err)
		}
	}

	var list struct {
		Data []struct {
			ID string `json:"id"`
		}
	}
	_ = jsonDecode(opsA.do(http.MethodGet, "/api/v1/platform-usage/bills/accounts?connection_id=bill-conn", ""), &list)
	if len(list.Data) != 1 || list.Data[0].ID != accA {
		t.Fatalf("opsA bill accounts=%+v", list.Data)
	}
	_ = jsonDecode(admin.do(http.MethodGet, "/api/v1/platform-usage/bills/accounts?connection_id=bill-conn", ""), &list)
	if len(list.Data) != 2 {
		t.Fatalf("admin bill accounts=%d", len(list.Data))
	}
}

func TestSpendSummaryAndDeleteUserRespectOwnership(t *testing.T) {
	a, admin, opsA, opsB := ownershipUsers(t)
	accA := createOwnedAccount(t, opsA, "spend-a@example.test", "")
	accB := createOwnedAccount(t, opsB, "spend-b@example.test", "")
	if _, err := a.DB.Exec(`INSERT INTO platform_connections(id,name,base_url,timezone,enabled,version,created_at,updated_at) VALUES('spend-conn','Spend','https://x.example','UTC',1,1,?,?)`, now(), now()); err != nil {
		t.Fatal(err)
	}
	day := "2026-11-15"
	for _, row := range []struct{ id, account string }{{"sa", accA}, {"sb", accB}} {
		if _, err := a.DB.Exec(`INSERT INTO platform_bill_models(id,connection_id,upstream_id,account_id,bill_date,model_name,bill_model_name,cost_fen,fetched_at,generation,final) VALUES(?,?,?,?,?,'m','m',100,?,1,1)`, row.id, "spend-conn", row.id, row.account, day, now()); err != nil {
			t.Fatal(err)
		}
	}
	var meA struct {
		Data struct {
			User struct{ ID string }
		}
	}
	_ = jsonDecode(opsA.do(http.MethodGet, "/api/v1/auth/session", ""), &meA)
	spend, err := a.spendSummary(context.Background(), mustParseTime(day+"T12:00:00Z"), ownershipScope{UserID: meA.Data.User.ID})
	if err != nil {
		t.Fatal(err)
	}
	accounts, _ := spend["cost_by_account"].([]map[string]any)
	if len(accounts) != 1 || accounts[0]["account_id"] != accA {
		t.Fatalf("opsA spend=%v", accounts)
	}

	var created struct {
		Data struct{ ID string }
	}
	_ = jsonDecode(admin.do(http.MethodPost, "/api/v1/users", `{"username":"ops-gone","password":"operator-password-x","role_ids":["`+roleOperatorID+`"]}`), &created)
	gone := newTestClient(t, a)
	gone.loginAs("ops-gone", "operator-password-x")
	accGone := createOwnedAccount(t, gone, "gone@example.test", "")
	if res := admin.do(http.MethodDelete, "/api/v1/users/"+created.Data.ID, ""); res.StatusCode != http.StatusOK {
		t.Fatalf("delete user=%d", res.StatusCode)
	}
	var creator string
	if err := a.DB.QueryRow(`SELECT created_by_user_id FROM accounts WHERE id=?`, accGone).Scan(&creator); err != nil || creator != "" {
		t.Fatalf("creator after delete=%q err=%v", creator, err)
	}
	if res := admin.do(http.MethodPatch, "/api/v1/accounts/"+accGone, `{"plan":"claude/cc-max-5x","expected_version":1}`); res.StatusCode != http.StatusOK {
		t.Fatalf("admin patch orphaned account=%d", res.StatusCode)
	}
}

func mustParseTime(v string) time.Time {
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		panic(err)
	}
	return t
}

func TestImportCommitWithProxyDoesNotDeadlock(t *testing.T) {
	_, _, opsA, opsB := ownershipUsers(t)
	proxyA := createOwnedProxy(t, opsA, "imp-a")
	proxyB := createOwnedProxy(t, opsB, "imp-b")

	done := make(chan int, 1)
	go func() {
		content := `[{"account":"imp-own@example.test","plan":"claude/cc-pro","proxy_id":"` + proxyA + `"}]`
		body, _ := json.Marshal(map[string]any{"format": "accounts", "content": content})
		preview := opsA.do(http.MethodPost, "/api/v1/imports/preview", string(body))
		var env struct {
			Data struct {
				ID string `json:"import_id"`
			} `json:"data"`
		}
		_ = jsonDecode(preview, &env)
		if preview.StatusCode != http.StatusOK || env.Data.ID == "" {
			done <- preview.StatusCode
			return
		}
		commit := opsA.do(http.MethodPost, "/api/v1/imports/"+env.Data.ID+"/commit", `{"strategy":"skip_existing"}`)
		done <- commit.StatusCode
	}()
	select {
	case code := <-done:
		if code != http.StatusOK {
			t.Fatalf("import with owned proxy status=%d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("import commit deadlocked on proxy ownership check")
	}

	content := `[{"account":"imp-foreign@example.test","plan":"claude/cc-pro","proxy_id":"` + proxyB + `"}]`
	body, _ := json.Marshal(map[string]any{"format": "accounts", "content": content})
	preview := opsA.do(http.MethodPost, "/api/v1/imports/preview", string(body))
	var env struct {
		Data struct {
			ID string `json:"import_id"`
		} `json:"data"`
	}
	_ = jsonDecode(preview, &env)
	assertEnvelope(t, opsA.do(http.MethodPost, "/api/v1/imports/"+env.Data.ID+"/commit", `{"strategy":"skip_existing"}`), http.StatusUnprocessableEntity, "PROXY_INVALID")
}

func TestRemoteStatusCountsAndBillRunRespectOwnership(t *testing.T) {
	a, admin, opsA, opsB := ownershipUsers(t)
	accA := createOwnedAccount(t, opsA, "remote-a@example.test", "")
	accB := createOwnedAccount(t, opsB, "remote-b@example.test", "")
	stamp := now()
	if _, err := a.DB.Exec(`INSERT INTO platform_connections(id,name,base_url,timezone,enabled,version,created_at,updated_at) VALUES('own-conn','Own','https://x.example','UTC',1,1,?,?)`, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		id, account, status string
	}{
		{"ra", accA, "using"},
		{"rb", accB, "ban"},
	} {
		if _, err := a.DB.Exec(`INSERT INTO platform_remote_accounts(connection_id,upstream_id,account_id,ext_status,missing_since,first_seen_at,last_seen_at) VALUES('own-conn',?,?,?,'',?,?)`, row.id, row.account, row.status, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	var meA struct {
		Data struct {
			User struct{ ID string }
		}
	}
	_ = jsonDecode(opsA.do(http.MethodGet, "/api/v1/auth/session", ""), &meA)
	counts, err := a.remoteStatusCounts(context.Background(), "", ownershipScope{UserID: meA.Data.User.ID})
	if err != nil {
		t.Fatal(err)
	}
	if counts["using"] != 1 || counts["ban"] != 0 {
		t.Fatalf("opsA remote counts=%v", counts)
	}
	adminCounts, err := a.remoteStatusCounts(context.Background(), "", ownershipScope{Admin: true})
	if err != nil {
		t.Fatal(err)
	}
	if adminCounts["using"] != 1 || adminCounts["ban"] != 1 {
		t.Fatalf("admin remote counts=%v", adminCounts)
	}

	// Remote-accounts drawer is ownership-scoped: operators only see remotes
	// linked to their own accounts; unmatched rows stay admin-only.
	if _, err := a.DB.Exec(`INSERT INTO platform_remote_accounts(connection_id,upstream_id,account_id,account_email,ext_status,missing_since,first_seen_at,last_seen_at) VALUES('own-conn','ru','','','using','',?,?)`, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	var remoteOut struct {
		Data struct {
			List []struct {
				UpstreamID string `json:"upstream_id"`
				AccountID  string `json:"account_id"`
			} `json:"list"`
			Summary       map[string]int `json:"summary"`
			CountsByState map[string]int `json:"counts_by_status"`
			Total         int            `json:"total"`
		} `json:"data"`
	}
	_ = jsonDecode(opsA.do(http.MethodGet, "/api/v1/platform-connections/own-conn/remote-accounts?page=1&page_size=20", ""), &remoteOut)
	if remoteOut.Data.Total != 1 || len(remoteOut.Data.List) != 1 || remoteOut.Data.List[0].UpstreamID != "ra" || remoteOut.Data.List[0].AccountID != accA {
		t.Fatalf("opsA remote-accounts=%+v", remoteOut.Data)
	}
	if remoteOut.Data.Summary["total"] != 1 || remoteOut.Data.Summary["linked"] != 1 || remoteOut.Data.CountsByState["using"] != 1 || remoteOut.Data.CountsByState["ban"] != 0 {
		t.Fatalf("opsA remote summary=%v counts=%v", remoteOut.Data.Summary, remoteOut.Data.CountsByState)
	}
	_ = jsonDecode(opsB.do(http.MethodGet, "/api/v1/platform-connections/own-conn/remote-accounts?page=1&page_size=20", ""), &remoteOut)
	if remoteOut.Data.Total != 1 || len(remoteOut.Data.List) != 1 || remoteOut.Data.List[0].UpstreamID != "rb" {
		t.Fatalf("opsB remote-accounts=%+v", remoteOut.Data)
	}
	_ = jsonDecode(admin.do(http.MethodGet, "/api/v1/platform-connections/own-conn/remote-accounts?page=1&page_size=20", ""), &remoteOut)
	if remoteOut.Data.Total != 3 || remoteOut.Data.Summary["unmatched"] != 1 {
		t.Fatalf("admin remote-accounts total=%d summary=%v", remoteOut.Data.Total, remoteOut.Data.Summary)
	}

	payload := `{"connection_id":"own-conn","scope":"selected","from":"2026-11-01","to":"2026-11-01","timezone":"UTC","account_ids":["` + accA + `","` + accB + `"]}`
	if _, err := a.DB.Exec(`INSERT INTO sync_runs(id,connection_id,kind,state,started_at,finished_at,request_json,total_items,created_at,updated_at) VALUES('bill-own','own-conn','bill','partial','`+stamp+`','`+stamp+`',?,2,?,?)`, payload, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		id, account, state string
	}{
		{"bi-a", accA, "failed"},
		{"bi-b", accB, "updated"},
	} {
		if _, err := a.DB.Exec(`INSERT INTO bill_sync_items(id,run_id,connection_id,account_id,upstream_id,source_timezone,bill_date,state,updated_at) VALUES(?,'bill-own','own-conn',?,?,'UTC','2026-11-01',?,?)`, row.id, row.account, row.id, row.state, stamp); err != nil {
			t.Fatal(err)
		}
	}
	detail, err := a.billRunDetail("bill-own", ownershipScope{UserID: meA.Data.User.ID})
	if err != nil {
		t.Fatal(err)
	}
	got := detail["counts"].(map[string]int)
	if detail["total_items"] != 1 || detail["account_count"] != 1 || detail["state"] != "failed" || got["failed"] != 1 || got["updated"] != 0 {
		t.Fatalf("opsA bill detail=%v", detail)
	}

	var meB struct {
		Data struct {
			User struct{ ID string }
		}
	}
	_ = jsonDecode(opsB.do(http.MethodGet, "/api/v1/auth/session", ""), &meB)
	// Operator B must not retry A's failed items (only A has a failed row).
	retryB := httptest.NewRecorder()
	a.retryBillRun(retryB, withSessionUser(httptest.NewRequest("POST", "/retry-failed", strings.NewReader("{}")), meB.Data.User.ID, false), "bill-own")
	if retryB.Code != 422 || !strings.Contains(retryB.Body.String(), "NO_RETRY_ITEMS") {
		t.Fatalf("opsB retry=%d %s", retryB.Code, retryB.Body.String())
	}

	// List SQL must keep A's run even when many newer foreign runs exist.
	for i := 0; i < 20; i++ {
		runID := "bill-foreign-" + strconv.Itoa(i)
		created := stamp + fmt.Sprintf(".%03dZ", i+1)
		if _, err := a.DB.Exec(`INSERT INTO sync_runs(id,connection_id,kind,state,started_at,finished_at,request_json,total_items,created_at,updated_at) VALUES(?,'own-conn','bill','succeeded',?,?,?,1,?,?)`, runID, stamp, stamp, `{"connection_id":"own-conn"}`, created, stamp); err != nil {
			t.Fatal(err)
		}
		if _, err := a.DB.Exec(`INSERT INTO bill_sync_items(id,run_id,connection_id,account_id,upstream_id,source_timezone,bill_date,state,updated_at) VALUES(?,?,?,?,?,'UTC','2026-11-01','updated',?)`, "fi-"+runID, runID, "own-conn", accB, "rb", stamp); err != nil {
			t.Fatal(err)
		}
	}
	list := httptest.NewRecorder()
	a.billRunList(list, withSessionUser(httptest.NewRequest("GET", "/sync-runs?kind=bill", nil), meA.Data.User.ID, false))
	if list.Code != 200 || !strings.Contains(list.Body.String(), `"run_id":"bill-own"`) {
		t.Fatalf("opsA list missing own run: %s", list.Body.String())
	}
	_ = admin
}

func TestCollectBillLinksRespectsOwnership(t *testing.T) {
	a, _, opsA, opsB := ownershipUsers(t)
	accA := createOwnedAccount(t, opsA, "link-a@example.test", "")
	accB := createOwnedAccount(t, opsB, "link-b@example.test", "")
	stamp := now()
	if _, err := a.DB.Exec(`INSERT INTO platform_connections(id,name,base_url,timezone,enabled,version,created_at,updated_at) VALUES('link-conn','Link','https://x.example','UTC',1,1,?,?)`, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ id, account, upstream string }{
		{"pl-a", accA, "1001"},
		{"pl-b", accB, "1002"},
	} {
		if _, err := a.DB.Exec(`INSERT INTO platform_links(id,account_id,connection_id,upstream_id,ingestion_state,created_at,updated_at) VALUES(?,?,'link-conn',?,'accepted',?,?)`, row.id, row.account, row.upstream, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	var meA struct {
		Data struct {
			User struct{ ID string }
		}
	}
	_ = jsonDecode(opsA.do(http.MethodGet, "/api/v1/auth/session", ""), &meA)
	links, err := a.collectBillLinks(context.Background(), billSyncRequest{ConnectionID: "link-conn", Scope: "all_linked"}, ownershipScope{UserID: meA.Data.User.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].Account != accA {
		t.Fatalf("opsA all_linked=%+v", links)
	}
	_, err = a.collectBillLinks(context.Background(), billSyncRequest{ConnectionID: "link-conn", Scope: "selected", AccountIDs: []string{accB}}, ownershipScope{UserID: meA.Data.User.ID})
	var rejected *errs.Error
	if !errors.As(err, &rejected) || rejected.Code() != errs.CodeNoLinkedAccounts {
		t.Fatalf("selected foreign account err=%v", err)
	}
	_, err = a.collectBillLinks(context.Background(), billSyncRequest{ConnectionID: "link-conn", Scope: "selected", AccountIDs: []string{accA, accB}}, ownershipScope{UserID: meA.Data.User.ID})
	if !errors.As(err, &rejected) || rejected.Code() != errs.CodeAccountLinkMissing {
		t.Fatalf("selected mixed accounts err=%v", err)
	}
	adminLinks, err := a.collectBillLinks(context.Background(), billSyncRequest{ConnectionID: "link-conn", Scope: "all_linked"}, ownershipScope{Admin: true})
	if err != nil || len(adminLinks) != 2 {
		t.Fatalf("admin all_linked=%+v err=%v", adminLinks, err)
	}
}

func withSessionUser(r *http.Request, userID string, admin bool) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), sessionUserKey, sessionUserInfo{
		ID: userID, Username: userID, Access: access{Super: admin},
	}))
}

func TestProxyCreatorMigrationAddsColumn(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/proxy-owner.db?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE proxies(id TEXT PRIMARY KEY,name TEXT NOT NULL DEFAULT '',scheme TEXT NOT NULL DEFAULT '',host TEXT NOT NULL DEFAULT '',port INTEGER NOT NULL DEFAULT 0,username TEXT NOT NULL DEFAULT '',password_secret TEXT NOT NULL DEFAULT '',enabled INTEGER NOT NULL DEFAULT 1,version INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL DEFAULT '',updated_at TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := migrateProxyCreatorV15(tx); err != nil {
			tx.Rollback()
			t.Fatalf("pass %d: %v", i, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('proxies') WHERE name='created_by_user_id'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("created_by_user_id present=%d err=%v", n, err)
	}
}

func TestAdminAssignAccountAndProxy(t *testing.T) {
	a, admin, opsA, opsB := ownershipUsers(t)
	var meA, meB struct {
		Data struct {
			User struct{ ID string }
		}
	}
	_ = jsonDecode(opsA.do(http.MethodGet, "/api/v1/auth/session", ""), &meA)
	_ = jsonDecode(opsB.do(http.MethodGet, "/api/v1/auth/session", ""), &meB)

	if _, err := a.DB.Exec(`INSERT INTO accounts(id,account,plan,created_at,updated_at,created_by_user_id) VALUES('assign-acc','assign@example.test','claude/cc-pro',?,?,?)`, now(), now(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`INSERT INTO proxies(id,name,scheme,host,port,enabled,created_at,updated_at,created_by_user_id) VALUES('assign-proxy','assign-p','http','127.0.0.1',1080,1,?,?,?)`, now(), now(), ""); err != nil {
		t.Fatal(err)
	}

	assertEnvelope(t, opsA.do(http.MethodPost, "/api/v1/accounts/assign-acc/assign", `{"user_id":"`+meA.Data.User.ID+`"}`), http.StatusForbidden, "PERMISSION_DENIED")
	assertEnvelope(t, admin.do(http.MethodPost, "/api/v1/accounts/assign-acc/assign", `{"user_id":"missing"}`), http.StatusUnprocessableEntity, "USER_INVALID")

	if res := admin.do(http.MethodPost, "/api/v1/accounts/assign-acc/assign", `{"user_id":"`+meA.Data.User.ID+`"}`); res.StatusCode != http.StatusOK {
		t.Fatalf("assign account=%d", res.StatusCode)
	}
	if res := admin.do(http.MethodPost, "/api/v1/proxies/assign-proxy/assign", `{"user_id":"`+meA.Data.User.ID+`"}`); res.StatusCode != http.StatusOK {
		t.Fatalf("assign proxy=%d", res.StatusCode)
	}
	assertListIDs(t, opsA.do(http.MethodGet, "/api/v1/accounts?page=1&page_size=20", ""), []string{"assign-acc"})
	assertListIDs(t, opsA.do(http.MethodGet, "/api/v1/proxies", ""), []string{"assign-proxy"})
	assertEnvelope(t, opsB.do(http.MethodGet, "/api/v1/accounts/assign-acc", ""), http.StatusNotFound, "NOT_FOUND")

	if res := admin.do(http.MethodPost, "/api/v1/accounts/assign-acc/assign", `{"user_id":"`+meB.Data.User.ID+`"}`); res.StatusCode != http.StatusOK {
		t.Fatalf("reassign account=%d", res.StatusCode)
	}
	assertEnvelope(t, opsA.do(http.MethodGet, "/api/v1/accounts/assign-acc", ""), http.StatusNotFound, "NOT_FOUND")
	if res := opsB.do(http.MethodGet, "/api/v1/accounts/assign-acc", ""); res.StatusCode != http.StatusOK {
		t.Fatalf("opsB get assigned=%d", res.StatusCode)
	}

	if res := admin.do(http.MethodPost, "/api/v1/proxies/assign-proxy/assign", `{"user_id":""}`); res.StatusCode != http.StatusOK {
		t.Fatalf("unassign proxy=%d", res.StatusCode)
	}
	assertEnvelope(t, opsA.do(http.MethodGet, "/api/v1/proxies/assign-proxy/checks", ""), http.StatusNotFound, "NOT_FOUND")
	if res := admin.do(http.MethodGet, "/api/v1/proxies", ""); res.StatusCode != http.StatusOK {
		t.Fatalf("admin list proxies=%d", res.StatusCode)
	}

	// Bound proxy cannot move to another user while the binder still owns it.
	proxyBound := createOwnedProxy(t, opsA, "bound-p")
	accBound := createOwnedAccount(t, opsA, "bound@example.test", proxyBound)
	assertEnvelope(t, admin.do(http.MethodPost, "/api/v1/proxies/"+proxyBound+"/assign", `{"user_id":"`+meB.Data.User.ID+`"}`), http.StatusConflict, "PROXY_IN_USE")
	if res := opsA.do(http.MethodPatch, "/api/v1/accounts/"+accBound, `{"proxy_id":"","expected_version":1}`); res.StatusCode != http.StatusOK {
		t.Fatalf("unbind=%d", res.StatusCode)
	}
	if res := admin.do(http.MethodPost, "/api/v1/proxies/"+proxyBound+"/assign", `{"user_id":"`+meB.Data.User.ID+`"}`); res.StatusCode != http.StatusOK {
		t.Fatalf("assign unbound proxy=%d", res.StatusCode)
	}

	// Exclusive proxy moves with the account; shared proxy is unbound only.
	proxySolo := createOwnedProxy(t, opsA, "solo-p")
	accSolo := createOwnedAccount(t, opsA, "solo@example.test", proxySolo)
	var assignSolo struct {
		Data struct {
			ProxyAction string `json:"proxy_action"`
			ProxyID     string `json:"proxy_id"`
		}
	}
	resSolo := admin.do(http.MethodPost, "/api/v1/accounts/"+accSolo+"/assign", `{"user_id":"`+meB.Data.User.ID+`"}`)
	_ = jsonDecode(resSolo, &assignSolo)
	if resSolo.StatusCode != http.StatusOK || assignSolo.Data.ProxyAction != "transferred" || assignSolo.Data.ProxyID != proxySolo {
		t.Fatalf("exclusive transfer=%d %+v", resSolo.StatusCode, assignSolo.Data)
	}
	var proxySoloOwner string
	if err := a.DB.QueryRow(`SELECT created_by_user_id FROM proxies WHERE id=?`, proxySolo).Scan(&proxySoloOwner); err != nil || proxySoloOwner != meB.Data.User.ID {
		t.Fatalf("proxy transferred owner=%q err=%v", proxySoloOwner, err)
	}
	assertListIDs(t, opsB.do(http.MethodGet, "/api/v1/proxies", ""), []string{proxyBound, proxySolo})

	proxyShare := createOwnedProxy(t, opsA, "share-p")
	accShare1 := createOwnedAccount(t, opsA, "share1@example.test", proxyShare)
	accShare2 := createOwnedAccount(t, opsA, "share2@example.test", proxyShare)
	authJSON, _ := json.Marshal(map[string]any{
		"access_token": "a", "refresh_token": "r", "last_refresh": "2026-09-20T00:17:44-07:00",
		"email": "share1@example.test", "account_uuid": "u", "organization_uuid": "o",
		"organization_name": "n", "claude_device_ids": []any{"d"}, "type": "t",
		"expired": "2026-10-20T00:17:44-07:00", "proxy_url": "http://10.0.0.1:8080",
		"verification_status": "v", "id_token": "",
	})
	sealedShare, err := a.keys.Seal(authJSON, []byte(accShare1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`UPDATE accounts SET credentials_json=? WHERE id=?`, sealedShare, accShare1); err != nil {
		t.Fatal(err)
	}
	var assignShare struct {
		Data struct {
			ProxyAction string `json:"proxy_action"`
		}
	}
	resShare := admin.do(http.MethodPost, "/api/v1/accounts/"+accShare1+"/assign", `{"user_id":"`+meB.Data.User.ID+`"}`)
	_ = jsonDecode(resShare, &assignShare)
	if resShare.StatusCode != http.StatusOK || assignShare.Data.ProxyAction != "unbound" {
		t.Fatalf("shared unbind=%d %+v", resShare.StatusCode, assignShare.Data)
	}
	var shareProxyOwner, share1Proxy, share2Proxy string
	_ = a.DB.QueryRow(`SELECT created_by_user_id FROM proxies WHERE id=?`, proxyShare).Scan(&shareProxyOwner)
	_ = a.DB.QueryRow(`SELECT proxy_id FROM accounts WHERE id=?`, accShare1).Scan(&share1Proxy)
	_ = a.DB.QueryRow(`SELECT proxy_id FROM accounts WHERE id=?`, accShare2).Scan(&share2Proxy)
	if shareProxyOwner != meA.Data.User.ID || share1Proxy != "" || share2Proxy != proxyShare {
		t.Fatalf("shared result owner=%q a1=%q a2=%q", shareProxyOwner, share1Proxy, share2Proxy)
	}
	cred, err := a.loadCredential(accShare1)
	if err != nil {
		t.Fatal(err)
	}
	if _, kept := cred["proxy_url"]; kept {
		t.Fatalf("proxy_url should be stripped after shared unbind: %#v", cred["proxy_url"])
	}

	// Archived co-binder still blocks exclusive transfer.
	proxyArch := createOwnedProxy(t, opsA, "arch-p")
	accLive := createOwnedAccount(t, opsA, "live@example.test", proxyArch)
	accArch := createOwnedAccount(t, opsA, "arch@example.test", proxyArch)
	if _, err := a.DB.Exec(`UPDATE accounts SET archived=1 WHERE id=?`, accArch); err != nil {
		t.Fatal(err)
	}
	var assignArch struct {
		Data struct {
			ProxyAction string `json:"proxy_action"`
		}
	}
	resArch := admin.do(http.MethodPost, "/api/v1/accounts/"+accLive+"/assign", `{"user_id":"`+meB.Data.User.ID+`"}`)
	_ = jsonDecode(resArch, &assignArch)
	if resArch.StatusCode != http.StatusOK || assignArch.Data.ProxyAction != "unbound" {
		t.Fatalf("archived co-bind should unbind not transfer=%d %+v", resArch.StatusCode, assignArch.Data)
	}
	var archProxyOwner string
	_ = a.DB.QueryRow(`SELECT created_by_user_id FROM proxies WHERE id=?`, proxyArch).Scan(&archProxyOwner)
	if archProxyOwner != meA.Data.User.ID {
		t.Fatalf("archived co-bind must keep proxy with original owner, got %q", archProxyOwner)
	}

	// Queued onboard blocks account assign so ownership does not change mid-flight.
	if _, err := a.DB.Exec(`INSERT INTO jobs(id,kind,state,account_id,created_at,updated_at) VALUES('assign-job','account.submit','queued',?,?,?)`, accBound, now(), now()); err != nil {
		t.Fatal(err)
	}
	assertEnvelope(t, admin.do(http.MethodPost, "/api/v1/accounts/"+accBound+"/assign", `{"user_id":"`+meB.Data.User.ID+`"}`), http.StatusConflict, "ACCOUNT_IN_PROGRESS")
}

var ownedProxyPortSeq atomic.Int64

func createOwnedProxy(t *testing.T, c *testClient, name string) string {
	t.Helper()
	port := 20000 + int(ownedProxyPortSeq.Add(1))
	res := c.do(http.MethodPost, "/api/v1/proxies", fmt.Sprintf(`{"name":%q,"scheme":"http","host":"127.0.0.1","port":%d}`, name, port))
	var out struct {
		Data  struct{ ID string }
		Error struct{ Message string }
	}
	_ = jsonDecode(res, &out)
	if res.StatusCode != http.StatusCreated || out.Data.ID == "" {
		t.Fatalf("create proxy %s=%d %+v", name, res.StatusCode, out.Error)
	}
	return out.Data.ID
}

func createOwnedAccount(t *testing.T, c *testClient, email, proxyID string) string {
	t.Helper()
	body := map[string]any{"account": email, "plan": "claude/cc-pro"}
	if proxyID != "" {
		body["proxy_id"] = proxyID
	}
	raw, _ := json.Marshal(body)
	res := c.do(http.MethodPost, "/api/v1/accounts", string(raw))
	var out struct {
		Data  struct{ ID string }
		Error struct{ Message string }
	}
	_ = jsonDecode(res, &out)
	if res.StatusCode != http.StatusCreated || out.Data.ID == "" {
		t.Fatalf("create account %s=%d %+v", email, res.StatusCode, out.Error)
	}
	return out.Data.ID
}

func assertListIDs(t *testing.T, res *http.Response, want []string) {
	t.Helper()
	var envelope struct {
		Data  json.RawMessage `json:"data"`
		Error struct{ Message string }
	}
	_ = jsonDecode(res, &envelope)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("list status=%d %+v", res.StatusCode, envelope.Error)
	}
	var ids []string
	var paged struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if json.Unmarshal(envelope.Data, &paged) == nil && paged.Items != nil {
		for _, item := range paged.Items {
			ids = append(ids, item.ID)
		}
	} else {
		var items []struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(envelope.Data, &items); err != nil {
			t.Fatalf("decode list: %v raw=%s", err, envelope.Data)
		}
		for _, item := range items {
			ids = append(ids, item.ID)
		}
	}
	if len(ids) != len(want) {
		t.Fatalf("ids=%v want=%v", ids, want)
	}
	have := map[string]bool{}
	for _, id := range ids {
		have[id] = true
	}
	for _, id := range want {
		if !have[id] {
			t.Fatalf("missing %s in %v", id, ids)
		}
	}
}
