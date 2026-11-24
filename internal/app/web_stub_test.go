package app

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// webConsoleStub imitates the supplier web console for app-level tests:
// RSA login, a paged account list, reason endpoints and /bill/query. Every
// behaviour a test needs is a field; all state is guarded by mu.
type webConsoleStub struct {
	t            *testing.T
	priv         *rsa.PrivateKey
	server       *httptest.Server
	mu           sync.Mutex
	password     string
	accounts     []map[string]any
	total        *int
	reason       func(id, kind string) string
	reasonStatus int
	bills        func(ids []string, start, end string) string
	logins       atomic.Int32
	lists        atomic.Int32
	reasons      atomic.Int32
	billHits     atomic.Int32
	tokenSeq     atomic.Int32
	// listStatus, when non-zero, makes every account listing fail with it.
	listStatus int
	// updates records every PUT /supplier/accounts/{id} payload; the stub
	// also applies remark and the cpa_* fields to its account so a later
	// listing reflects the change. updateResponse overrides the reply.
	updates        []map[string]any
	updateResponse func(w http.ResponseWriter)
}

func newWebConsoleStub(t *testing.T) *webConsoleStub {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	s := &webConsoleStub{t: t, priv: priv, password: "console-secret"}
	s.reason = func(id, kind string) string { return `{"reason":"原因 ` + kind + ` ` + id + `"}` }
	s.bills = func(ids []string, start, end string) string {
		return `{"list":[],"range":{"start_ds":` + strings.ReplaceAll(start, "-", "") + `,"end_ds":` + strings.ReplaceAll(end, "-", "") + `}}`
	}
	s.server = httptest.NewTLSServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.server.Close)
	return s
}

func (s *webConsoleStub) setAccounts(list ...map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accounts = list
}

func (s *webConsoleStub) decrypt(encoded string) string {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return ""
	}
	var env map[string]string
	if json.Unmarshal(raw, &env) != nil {
		return ""
	}
	wrapped, _ := base64.StdEncoding.DecodeString(env["k"])
	key, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, s.priv, wrapped, nil)
	if err != nil {
		return ""
	}
	iv, _ := base64.StdEncoding.DecodeString(env["iv"])
	ct, _ := base64.StdEncoding.DecodeString(env["ct"])
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, iv, ct, nil)
	if err != nil {
		return ""
	}
	return string(plain)
}

func (s *webConsoleStub) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := strings.TrimPrefix(r.URL.Path, "/supplier-account/api")
	switch path {
	case "/supplier/pubkey":
		der, _ := x509.MarshalPKIXPublicKey(&s.priv.PublicKey)
		fmt.Fprintf(w, `{"public_key":%q}`, string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})))
		return
	case "/supplier/login":
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.logins.Add(1)
		s.mu.Lock()
		ok := s.decrypt(body["enc_password"]) == s.password && body["username"] == "console-user"
		s.mu.Unlock()
		if !ok {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"detail":"用户名或密码错误"}`)
			return
		}
		fmt.Fprintf(w, `{"token":"tok%d","name":"益充汇","expires_in":18000}`, s.tokenSeq.Add(1))
		return
	}
	if r.Header.Get("Authorization") != fmt.Sprintf("Bearer tok%d", s.tokenSeq.Load()) || s.tokenSeq.Load() == 0 {
		w.WriteHeader(401)
		fmt.Fprint(w, `{"detail":"供应商未登录或凭证失效"}`)
		return
	}
	switch {
	case r.Method == http.MethodPut && strings.HasPrefix(path, "/supplier/accounts/"):
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			s.t.Errorf("update body: %v", err)
		}
		s.mu.Lock()
		s.updates = append(s.updates, payload)
		id := strings.TrimPrefix(path, "/supplier/accounts/")
		for _, acc := range s.accounts {
			if fmt.Sprint(acc["id"]) != id {
				continue
			}
			var cfg map[string]any
			_ = json.Unmarshal([]byte(fmt.Sprint(acc["auth_config"])), &cfg)
			cfg["account"] = payload["account_id"]
			for _, k := range []string{"plan", "register_time", "plan_start_time", "plan_end_time", "register_location", "balance", "payment_method", "register_device", "recharge_time", "warranty_period"} {
				cfg[k] = payload["cpa_"+k]
			}
			encoded, _ := json.Marshal(cfg)
			acc["auth_config"] = string(encoded)
			acc["remark"] = payload["remark"]
		}
		override := s.updateResponse
		s.mu.Unlock()
		if override != nil {
			override(w)
			return
		}
		fmt.Fprintf(w, `{"id":%s,"msg":"修改成功"}`, id)
	case path == "/supplier/accounts":
		s.lists.Add(1)
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.listStatus != 0 {
			w.WriteHeader(s.listStatus)
			fmt.Fprint(w, `{"detail":"console down"}`)
			return
		}
		page := 1
		fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
		from, to := (page-1)*100, page*100
		if from > len(s.accounts) {
			from = len(s.accounts)
		}
		if to > len(s.accounts) {
			to = len(s.accounts)
		}
		list, _ := json.Marshal(s.accounts[from:to])
		total := len(s.accounts)
		if s.total != nil {
			total = *s.total
		}
		counts := map[string]int{"all": total}
		for _, a := range s.accounts {
			counts[fmt.Sprint(a["ext_status"])]++
		}
		encodedCounts, _ := json.Marshal(counts)
		fmt.Fprintf(w, `{"counts":%s,"list":%s,"page":%d,"page_size":100,"supplier_name":"益充汇","total":%d}`, encodedCounts, list, page, total)
	case strings.HasPrefix(path, "/supplier/accounts/"):
		parts := strings.Split(strings.TrimPrefix(path, "/supplier/accounts/"), "/")
		if len(parts) != 2 {
			w.WriteHeader(404)
			return
		}
		s.reasons.Add(1)
		s.mu.Lock()
		body := s.reason(parts[0], strings.TrimSuffix(parts[1], "-reason"))
		status := s.reasonStatus
		s.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			fmt.Fprint(w, `{"detail":"not found"}`)
			return
		}
		fmt.Fprint(w, body)
	case path == "/bill/query":
		s.billHits.Add(1)
		q := r.URL.Query()
		s.mu.Lock()
		body := s.bills(strings.Split(q.Get("account_ids"), ","), q.Get("start_ds"), q.Get("end_ds"))
		s.mu.Unlock()
		fmt.Fprint(w, body)
	default:
		w.WriteHeader(404)
		fmt.Fprint(w, `{"detail":"not found"}`)
	}
}

// remoteAccount builds one console list entry. auth_config and extra are
// JSON strings like the real console; pass nil usage for "no usage block".
func remoteAccount(id int, email, status string, created string, usage map[string]any, profile map[string]any) map[string]any {
	cfg := map[string]any{"account": email, "password": "ab****cd", "plan": "claude/cc-max-20x", "plan_start_time": "2026-11-11", "plan_end_time": "2026-12-10", "register_time": "2026-11-03", "register_location": "US", "balance": "100", "payment_method": "google_pay", "register_device": "Mac"}
	for k, v := range profile {
		cfg[k] = v
	}
	authConfig, _ := json.Marshal(cfg)
	extra := ""
	if usage != nil {
		b, _ := json.Marshal(map[string]any{"cpa_usage": usage})
		extra = string(b)
	}
	return map[string]any{
		"id": id, "a1_id": id + 10000, "a1_status": status, "a2_id": 0, "a2_status": "", "account_alias": fmt.Sprintf("claude-fixture-%d", id), "account_md5": "md5", "account_type": 2,
		"api_key": "", "api_secret": "", "auth_config": string(authConfig), "created_at": created, "ext_status": status, "extra": extra, "has_cpa_auth_json": true,
		"is_locked": 0, "model": "claude/cc-max-20x", "models": []map[string]any{{"name": "claude/cc-max-20x", "usable": true}}, "project_id": email, "provider": "claude",
		"proxy_type": "pool", "region": "", "remark": "", "status": 1, "supplier_id": 228, "supplier_name": "益充汇", "updated_at": created, "usage_limit_period": "", "usage_limit_type": "", "usage_limit_value": 0,
	}
}

func fixtureUsage(costMicro int64, fiveHour float64) map[string]any {
	return map[string]any{"cost": costMicro, "input_tokens": 411638, "output_tokens": 82648, "utilization": map[string]any{"five_hour_used_percent": fiveHour, "five_hour_reset_at": 1794415800, "week_used_percent": 0.01, "week_reset_at": 1794978000}}
}

// webFixture returns an app whose connection "conn" points at a console stub
// and already holds the stub's credentials.
func webFixture(t *testing.T) (*App, *webConsoleStub) {
	t.Helper()
	a, _, _, _ := deliveryFixture(t)
	stub := newWebConsoleStub(t)
	setWebCredentials(t, a, stub)
	return a, stub
}

func setWebCredentials(t *testing.T, a *App, stub *webConsoleStub) {
	t.Helper()
	sealed, err := a.keys.Seal([]byte(stub.password), []byte("conn:web"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.DB.Exec(`UPDATE platform_connections SET base_url=?,web_username='console-user',web_password_secret=?,web_suspended_at='',web_suspended_reason='',updated_at=? WHERE id='conn'`, stub.server.URL, sealed, now()); err != nil {
		t.Fatal(err)
	}
	a.dropWebClient("conn")
}
