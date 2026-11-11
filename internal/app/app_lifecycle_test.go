package app

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every column that a migration adds to a legacy database must already be
// present in the fresh schema; otherwise new and upgraded databases diverge.
func TestFreshSchemaMatchesMigrations(t *testing.T) {
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "schema-test-password")
	a, err := newApp(t.TempDir()+"/fresh.db", false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	columns := func(table string) map[string]bool {
		rows, err := a.DB.Query("PRAGMA table_info(" + table + ")")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]bool{}
		for rows.Next() {
			var cid, notnull, pk int
			var name, typ string
			var def any
			if err := rows.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil {
				t.Fatal(err)
			}
			out[name] = true
		}
		return out
	}
	for table, definitions := range migrationColumns {
		have := columns(table)
		for _, definition := range definitions {
			name, _, _ := strings.Cut(definition, " ")
			if !have[name] {
				t.Errorf("fresh schema for %s lacks migrated column %s", table, name)
			}
		}
	}
	for _, name := range []string{"actor", "detail"} {
		if !columns("audit_events")[name] {
			t.Errorf("audit_events lacks %s", name)
		}
	}
	var applied int
	if err := a.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil || applied != len(migrations) {
		t.Fatalf("applied=%d err=%v", applied, err)
	}
	if err := migrate(a.DB); err != nil {
		t.Fatalf("migrations are not idempotent: %v", err)
	}
}

func TestLegacyNullColumnsAreBackfilled(t *testing.T) {
	path := t.TempDir() + "/legacy-null.db"
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE accounts(id TEXT PRIMARY KEY, account TEXT UNIQUE NOT NULL, plan TEXT, register_location TEXT, register_time TEXT, plan_start_time TEXT, plan_end_time TEXT, proxy_id TEXT, metadata_json TEXT, credentials_json TEXT, version INTEGER DEFAULT 1, archived INTEGER DEFAULT 0, created_at TEXT, updated_at TEXT);
INSERT INTO accounts(id,account) VALUES('legacy','legacy@example.test');`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "schema-test-password")
	a, err := newApp(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	w := httptest.NewRecorder()
	a.accounts(w, httptest.NewRequest(http.MethodGet, "/accounts", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "legacy@example.test") {
		t.Fatalf("legacy row with NULL columns is missing from the list: %d %s", w.Code, w.Body.String())
	}
}

func TestCloseStopsWorkersAndReleasesDatabase(t *testing.T) {
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "schema-test-password")
	path := t.TempDir() + "/close.db"
	a, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return; a worker ignored cancellation")
	}
	if err := a.Close(); err != nil {
		t.Fatalf("second Close must be a no-op: %v", err)
	}
	if _, err := os.Stat(path + "-wal"); err == nil {
		info, _ := os.Stat(path + "-wal")
		if info.Size() != 0 {
			t.Fatalf("wal was not checkpointed, size=%d", info.Size())
		}
	}
}

func TestHealthzReportsDatabaseAndVersion(t *testing.T) {
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "schema-test-password")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.DBPath = t.TempDir() + "/health.db"
	cfg.Version = "test-build"
	a, err := NewWithConfig(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"version":"test-build"`) || !strings.Contains(w.Body.String(), `"database":"ok"`) {
		t.Fatalf("healthz %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("X-Request-ID") == "" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("observability headers missing")
	}
	for _, key := range []string{`"web_accounts_last_tick"`, `"web_bills_last_tick"`} {
		if !strings.Contains(w.Body.String(), key) {
			t.Fatalf("healthz lacks %s: %s", key, w.Body.String())
		}
	}
}

func TestSPAHandlerFallsBackToIndexAndNeverListsDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>app</html>"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("console.log(1)"), 0o644)
	h := spaHandler(dir)
	for _, tc := range []struct {
		path, want string
		status     int
	}{
		{"/", "app", 200},
		{"/accounts", "app", 200},
		{"/assets/app.js", "console.log", 200},
		{"/assets/", "", 200},
		{"/missing.png", "", 404},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.want) {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
		if tc.path == "/assets/" && !strings.Contains(w.Body.String(), "<html>app") {
			t.Fatalf("directory path leaked a listing: %s", w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
	if !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatal("hashed assets must be cacheable")
	}
	// A rebuilt index.html must be served without restarting the process.
	_ = os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>rebuilt</html>"), 0o644)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/accounts", nil))
	if !strings.Contains(w.Body.String(), "rebuilt") {
		t.Fatal("index.html is cached across frontend rebuilds")
	}
}

func TestAccountCreateConflictAndVersionedPatch(t *testing.T) {
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "correct-horse-battery")
	a, err := newApp(t.TempDir()+"/patch.db", false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	c := newTestClient(t, a)
	c.login("correct-horse-battery")
	res := c.do(http.MethodPost, "/api/v1/accounts", `{"account":"dup@example.test","plan":"claude/cc-max-20x"}`)
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = jsonDecode(res, &created)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d", res.StatusCode)
	}
	if res := c.do(http.MethodPost, "/api/v1/accounts", `{"account":"dup@example.test","plan":"claude/cc-max-20x"}`); res.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate status=%d", res.StatusCode)
	}
	if res := c.do(http.MethodPatch, "/api/v1/accounts/"+created.Data.ID, `{"plan":"claude/cc-pro","expected_version":99}`); res.StatusCode != http.StatusConflict {
		t.Fatalf("stale version status=%d", res.StatusCode)
	}
	if res := c.do(http.MethodPatch, "/api/v1/accounts/"+created.Data.ID, `{"plan":"claude/cc-pro","expected_version":1}`); res.StatusCode != http.StatusOK {
		t.Fatalf("patch status=%d", res.StatusCode)
	}
	if res := c.do(http.MethodPatch, "/api/v1/accounts/"+created.Data.ID, `{"plan":"claude/unknown"}`); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid plan status=%d", res.StatusCode)
	}
	if res := c.do(http.MethodPatch, "/api/v1/accounts/"+created.Data.ID, `{"register_time":"2026-02-01","plan_start_time":"2026-01-01"}`); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("date order status=%d", res.StatusCode)
	}
	oversized := `{"account":"big@example.test","plan":"claude/cc-max-20x","remark":"` + strings.Repeat("x", defaultBodyLimit) + `"}`
	if res := c.do(http.MethodPost, "/api/v1/accounts", oversized); res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body status=%d", res.StatusCode)
	}
	var audited int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action IN ('account.create','account.update')`).Scan(&audited)
	if audited != 2 {
		t.Fatalf("audit events=%d", audited)
	}
}

func jsonDecode(res *http.Response, v any) error {
	defer res.Body.Close()
	return jsonNewDecoder(res).Decode(v)
}

func jsonNewDecoder(res *http.Response) interface{ Decode(any) error } {
	return json.NewDecoder(res.Body)
}

func TestMetricsEndpointExposesCountersAndGauges(t *testing.T) {
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "correct-horse-battery")
	a, err := newApp(t.TempDir()+"/metrics.db", false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	c := newTestClient(t, a)
	c.login("correct-horse-battery")
	c.do(http.MethodGet, "/api/v1/does-not-exist", "").Body.Close()
	res := c.do(http.MethodGet, "/metrics", "")
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("status=%d type=%s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	body, _ := io.ReadAll(res.Body)
	text := string(body)
	for _, want := range []string{
		`cc_manager_http_requests_total{class="2xx"}`,
		`cc_manager_http_requests_total{class="4xx"}`,
		"cc_manager_http_request_duration_seconds_sum",
		"cc_manager_sessions 1",
		`cc_manager_worker_last_tick_seconds{worker="jobs"}`,
		`cc_manager_users{enabled="true"} 1`,
		"# TYPE cc_manager_jobs gauge",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("metrics missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, `class="4xx"} 0`) {
		t.Fatal("4xx counter did not record the 404")
	}
}
