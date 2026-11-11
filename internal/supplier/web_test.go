package supplier

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fixtureAccount is the 428802 row from the 2026-11-11 HAR with the email
// replaced. auth_config and extra are JSON strings, exactly as the console
// returns them.
const fixtureAccount = `{"a1_id":20204,"a1_status":"using","a2_id":0,"a2_status":"","account_alias":"claude-住享科智-20261111002","account_md5":"d41d8cd98f00b204e9800998ecf8427ed41d8cd98f00b204e9800998ecf8427e","account_type":2,"api_key":"","api_secret":"","auth_config":"{\"account\": \"fixture@example.test\", \"password\": \"jonas****e3785\", \"plan\": \"claude/cc-max-20x\", \"plan_start_time\": \"2026-11-11\", \"plan_end_time\": \"2026-12-10\", \"register_time\": \"2026-11-03\", \"register_location\": \"US\", \"balance\": \"100\", \"payment_method\": \"google_pay\", \"register_device\": \"Mac\", \"warranty_period\": 1, \"tags\": [\"慢蒸\"]}","created_at":"2026-11-11 19:36","ext_status":"using","extra":"{\"cpa_usage\": {\"cost\": 35209959, \"utilization\": {\"week_reset_at\": 1794978000, \"week_used_percent\": 0.01, \"five_hour_reset_at\": 1794415800, \"five_hour_used_percent\": 0.05}, \"input_tokens\": 411638, \"output_tokens\": 82648}}","has_api_key":false,"has_api_secret":false,"has_cpa_auth_json":true,"id":428802,"is_locked":0,"model":"claude/cc-max-20x","models":[{"name":"claude/cc-max-20x","usable":true}],"project_id":"fixture@example.test","provider":"claude","proxy_type":"pool","region":"","remark":"","status":1,"supplier_id":228,"supplier_name":"住享科智","updated_at":"2026-11-11 21:20","usage_limit_period":"","usage_limit_type":"","usage_limit_value":0}`

func withField(t *testing.T, raw, key string, value any) json.RawMessage {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	m[key] = value
	out, _ := json.Marshal(m)
	return out
}

func TestParseWebAccountTolerates(t *testing.T) {
	a, err := parseWebAccount(json.RawMessage(fixtureAccount))
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != 428802 || a.AID != 20204 || a.Email != "fixture@example.test" || a.Alias != "claude-住享科智-20261111002" || a.ExtStatus != "using" || a.A1Status != "using" || a.Locked || a.ProxyType != "pool" || !a.ModelsUsable || a.CreatedAt != "2026-11-11 19:36" {
		t.Fatalf("identity fields: %+v", a)
	}
	p := a.Profile
	if p.Plan != "claude/cc-max-20x" || p.Balance != "100" || p.PaymentMethod != "google_pay" || p.RegisterLocation != "US" || p.PlanEnd != "2026-12-10" || p.WarrantyPeriod != "1" || len(p.Tags) != 1 || p.Tags[0] != "慢蒸" {
		t.Fatalf("profile: %+v", p)
	}
	u := a.Usage
	if u == nil || u.CostMicro != 35209959 || u.InputTokens != 411638 || u.OutputTokens != 82648 || u.FiveHourUsed != 0.05 || u.WeekUsed != 0.01 || u.FiveHourResetAt.Unix() != 1794415800 || u.WeekResetAt.Unix() != 1794978000 {
		t.Fatalf("usage: %+v", u)
	}
	for _, secret := range []string{"jonas****e3785", "d41d8cd98f00b204e9800998ecf8427e"} {
		if strings.Contains(a.RawJSON, secret) {
			t.Fatalf("raw json leaks %q", secret)
		}
	}
	if !strings.Contains(a.RawJSON, "fixture@example.test") {
		t.Fatal("raw json should keep diagnostic fields")
	}
	for name, extra := range map[string]any{"empty": "", "garbage": "not json", "no-usage": `{"cpa_usage":{}}`, "no-key": `{}`} {
		v, err := parseWebAccount(withField(t, fixtureAccount, "extra", extra))
		if err != nil || v.Usage != nil || v.ID != 428802 {
			t.Fatalf("%s: err=%v usage=%+v", name, err, v.Usage)
		}
	}
	v, err := parseWebAccount(withField(t, fixtureAccount, "auth_config", ""))
	if err != nil || v.Profile.Plan != "" || v.Profile.Balance != "" || len(v.Profile.Tags) != 0 || v.Email != "fixture@example.test" {
		t.Fatalf("empty auth_config: err=%v profile=%+v email=%q", err, v.Profile, v.Email)
	}
	v, err = parseWebAccount(withField(t, fixtureAccount, "auth_config", `{"warranty_period":"2","account":"other@example.test"}`))
	if err != nil || v.Profile.WarrantyPeriod != "2" {
		t.Fatalf("string warranty: err=%v profile=%+v", err, v.Profile)
	}
	if _, err = parseWebAccount(json.RawMessage(`{"id":"x"}`)); err == nil {
		t.Fatal("non-numeric id accepted")
	}
}

// consoleStub imitates the supplier web console: RSA login, a paged account
// list, reasons and bills. Every behaviour a test needs is a field.
type consoleStub struct {
	t          *testing.T
	priv       *rsa.PrivateKey
	password   string
	server     *httptest.Server
	logins     atomic.Int32
	reasons    atomic.Int32
	billCalls  atomic.Int32
	pages      map[int]string // page -> JSON array of accounts
	total      int
	tokenSeq   atomic.Int32
	rejectPage func(page int, token string, attempt int) bool
	pageHits   map[int]int
	billBody   func(r *http.Request) string
	reasonBody string
}

func newConsoleStub(t *testing.T) *consoleStub {
	t.Helper()
	t.Setenv("CC_MANAGER_SUPPLIER_INSECURE_TLS", "1")
	t.Setenv("CC_MANAGER_SUPPLIER_TLS_PINS", "")
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	s := &consoleStub{t: t, priv: priv, password: "secret", pages: map[int]string{}, pageHits: map[int]int{}, reasonBody: `{"reason":"账号被封禁","code":7}`}
	s.server = httptest.NewTLSServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.server.Close)
	return s
}

func (s *consoleStub) currentToken() string { return fmt.Sprintf("tok%d", s.tokenSeq.Load()) }

func (s *consoleStub) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/supplier-account/api/supplier/pubkey":
		fmt.Fprintf(w, `{"public_key":%q}`, publicKeyPEM(s.t, &s.priv.PublicKey))
	case r.URL.Path == "/supplier-account/api/supplier/login":
		var body struct{ Username, EncPassword string }
		var raw map[string]string
		_ = json.NewDecoder(r.Body).Decode(&raw)
		body.Username, body.EncPassword = raw["username"], raw["enc_password"]
		plain, _ := decryptLoginPassword(s.t, s.priv, body.EncPassword)
		s.logins.Add(1)
		if body.Username != "zhuxiangkezhi-cpa" || plain != s.password {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"detail":"用户名或密码错误"}`)
			return
		}
		s.tokenSeq.Add(1)
		fmt.Fprintf(w, `{"token":%q,"name":"住享科智","public_key":"","expires_in":18000,"allow_fast_brush":0}`, s.currentToken())
	default:
		if r.Header.Get("Authorization") != "Bearer "+s.currentToken() {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"detail":"供应商未登录或凭证失效"}`)
			return
		}
		switch {
		case r.URL.Path == "/supplier-account/api/supplier/accounts":
			page := 1
			fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
			s.pageHits[page]++
			if s.rejectPage != nil && s.rejectPage(page, r.Header.Get("Authorization"), s.pageHits[page]) {
				w.WriteHeader(401)
				fmt.Fprint(w, `{"detail":"供应商未登录或凭证失效"}`)
				return
			}
			if r.URL.Query().Get("page_size") != "100" || r.URL.Query().Get("status") != "all" {
				s.t.Errorf("unexpected list query %s", r.URL.RawQuery)
			}
			list := s.pages[page]
			if list == "" {
				list = "[]"
			}
			fmt.Fprintf(w, `{"counts":{"all":%d,"using":%d,"ban":0},"list":%s,"page":%d,"page_size":100,"supplier_name":"住享科智","total":%d}`, s.total, s.total, list, page, s.total)
		case strings.HasSuffix(r.URL.Path, "/check-fail-reason") || strings.HasSuffix(r.URL.Path, "/claude-status-reason"):
			s.reasons.Add(1)
			fmt.Fprint(w, s.reasonBody)
		case r.URL.Path == "/supplier-account/api/bill/query":
			s.billCalls.Add(1)
			fmt.Fprint(w, s.billBody(r))
		default:
			w.WriteHeader(404)
			fmt.Fprint(w, `{"detail":"not found"}`)
		}
	}
}

func accountsJSON(t *testing.T, from, to int) string {
	t.Helper()
	var items []string
	for id := from; id <= to; id++ {
		items = append(items, string(withField(t, fixtureAccount, "id", id)))
	}
	return "[" + strings.Join(items, ",") + "]"
}

func TestWebClientLoginAndList(t *testing.T) {
	stub := newConsoleStub(t)
	stub.total = 108
	stub.pages[1] = accountsJSON(t, 1, 100)
	stub.pages[2] = accountsJSON(t, 101, 108)
	c, err := NewWebClient(stub.server.URL, "zhuxiangkezhi-cpa", "secret")
	if err != nil {
		t.Fatal(err)
	}
	session, err := c.Login(context.Background())
	if err != nil || session.SupplierName != "住享科智" || session.ExpiresIn != 18000 {
		t.Fatalf("login: %+v %v", session, err)
	}
	accounts, counts, total, err := c.ListAccounts(context.Background())
	if err != nil || len(accounts) != 108 || total != 108 || counts["using"] != 108 {
		t.Fatalf("list: n=%d total=%d counts=%v err=%v", len(accounts), total, counts, err)
	}
	if accounts[107].ID != 108 || stub.logins.Load() != 1 {
		t.Fatalf("last id=%d logins=%d", accounts[107].ID, stub.logins.Load())
	}
	if _, err = NewWebClient("http://plain.example", "u", "p"); err == nil {
		t.Fatal("accepted a non-HTTPS base")
	}
	if _, err = NewWebClient(stub.server.URL, "", "p"); err == nil {
		t.Fatal("accepted an empty username")
	}
}

func TestWebClientReloginOnce(t *testing.T) {
	stub := newConsoleStub(t)
	stub.total = 101
	stub.pages[1] = accountsJSON(t, 1, 100)
	stub.pages[2] = accountsJSON(t, 101, 101)
	// The first request for page 2 finds the token expired; the retry after a
	// fresh login succeeds.
	stub.rejectPage = func(page int, token string, attempt int) bool { return page == 2 && attempt == 1 }
	c, err := NewWebClient(stub.server.URL, "zhuxiangkezhi-cpa", "secret")
	if err != nil {
		t.Fatal(err)
	}
	accounts, _, _, err := c.ListAccounts(context.Background())
	if err != nil || len(accounts) != 101 || stub.logins.Load() != 2 {
		t.Fatalf("n=%d logins=%d err=%v", len(accounts), stub.logins.Load(), err)
	}

	always := newConsoleStub(t)
	always.total = 1
	always.pages[1] = accountsJSON(t, 1, 1)
	always.rejectPage = func(int, string, int) bool { return true }
	c, err = NewWebClient(always.server.URL, "zhuxiangkezhi-cpa", "secret")
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = c.ListAccounts(context.Background())
	var we *WebHTTPError
	if !errors.As(err, &we) || we.UpstreamCode() != "WEB_LOGIN_FAILED" || we.UpstreamStatus() != 401 {
		t.Fatalf("err=%v", err)
	}
	if always.logins.Load() != 2 {
		t.Fatalf("logins=%d, want exactly one re-login", always.logins.Load())
	}

	wrong := newConsoleStub(t)
	c, err = NewWebClient(wrong.server.URL, "zhuxiangkezhi-cpa", "wrong-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Login(context.Background()); !errors.As(err, &we) || we.UpstreamCode() != "WEB_LOGIN_FAILED" {
		t.Fatalf("wrong password: %v", err)
	}
	if strings.Contains(err.Error(), "wrong-password") {
		t.Fatal("error text leaks the password")
	}
}

func TestQueryModelBillsRange(t *testing.T) {
	stub := newConsoleStub(t)
	rangeJSON := `{"end_ds": 20261111, "start_ds": 20261105}`
	stub.billBody = func(r *http.Request) string {
		q := r.URL.Query()
		if q.Get("account_ids") != "428802,428242" || q.Get("start_ds") != "20261105" || q.Get("end_ds") != "20261111" {
			t.Errorf("unexpected bill query %s", r.URL.RawQuery)
		}
		return `{"accounts":[],"by_account":[],"by_model":[],"list":[
{"account_alias":"a","account_id":428802,"bill_model_name":"claude-opus-5","cost":27.61,"ds":20261111,"ds_text":"2026-11-11","model_name":"claude-opus-5"},
{"account_alias":"a","account_id":428802,"bill_model_name":"claude-opus-5-5","cost":7.60,"ds":20261111,"ds_text":"2026-11-11","model_name":"claude-opus-5-5"},
{"account_alias":"b","account_id":428242,"bill_model_name":"claude-opus-5","cost":0.92,"ds":20261111,"ds_text":"2026-11-11","model_name":"claude-opus-5"},
{"account_alias":"b","account_id":428242,"bill_model_name":"claude-opus-5-5","cost":78.04,"ds":20261111,"ds_text":"2026-11-11","model_name":"claude-opus-5-5"},
{"account_alias":"b","account_id":428242,"bill_model_name":"claude-opus-5","cost":1.00,"ds":20261104,"ds_text":"2026-11-04","model_name":"claude-opus-5"}
],"range":` + rangeJSON + `,"summary":{"account_count":2,"total_cost":114.18}}`
	}
	c, err := NewWebClient(stub.server.URL, "zhuxiangkezhi-cpa", "secret")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := c.QueryModelBills(context.Background(), []int64{428802, 428242}, "2026-11-05", "2026-11-11")
	if err != nil || len(rows) != 4 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	want := map[string]int64{"428802/claude-opus-5": 2761, "428802/claude-opus-5-5": 760, "428242/claude-opus-5": 92, "428242/claude-opus-5-5": 7804}
	for _, r := range rows {
		key := fmt.Sprintf("%d/%s", r.AccountID, r.Model)
		if want[key] != r.CostFen || r.Date != "2026-11-11" || r.BillModel != r.Model {
			t.Fatalf("row %+v", r)
		}
	}
	rangeJSON = `{"end_ds": 20261111, "start_ds": 20261101}`
	_, err = c.QueryModelBills(context.Background(), []int64{428802, 428242}, "2026-11-05", "2026-11-11")
	var we *WebHTTPError
	if !errors.As(err, &we) || we.UpstreamCode() != "WEB_BILL_RANGE_MISMATCH" {
		t.Fatalf("range mismatch not reported: %v", err)
	}
	ids := make([]int64, 51)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	if _, err = c.QueryModelBills(context.Background(), ids, "2026-11-05", "2026-11-11"); err == nil {
		t.Fatal("accepted 51 ids")
	}
	if _, err = c.QueryModelBills(context.Background(), []int64{1}, "2026-11-5", "2026-11-11"); err == nil {
		t.Fatal("accepted a malformed date")
	}
}

func TestAccountReason(t *testing.T) {
	stub := newConsoleStub(t)
	c, err := NewWebClient(stub.server.URL, "zhuxiangkezhi-cpa", "secret")
	if err != nil {
		t.Fatal(err)
	}
	reason, err := c.AccountReason(context.Background(), 385753, "claude-status")
	if err != nil || reason.Text != "账号被封禁" || !strings.Contains(reason.Raw, `"code":7`) {
		t.Fatalf("reason=%+v err=%v", reason, err)
	}
	stub.reasonBody = `{"data":{"msg":"验证失败：需要手机号"},"ok":true}`
	if reason, err = c.AccountReason(context.Background(), 385753, "check-fail"); err != nil || reason.Text != "验证失败：需要手机号" {
		t.Fatalf("nested reason=%+v err=%v", reason, err)
	}
	if _, err = c.AccountReason(context.Background(), 1, "other"); err == nil {
		t.Fatal("accepted an unknown reason kind")
	}
	if stub.reasons.Load() != 2 {
		t.Fatalf("reason calls=%d", stub.reasons.Load())
	}
}
