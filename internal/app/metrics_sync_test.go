package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The metrics worker must write one snapshot per linked account, refresh the
// link's runtime status and record a succeeded sync run.
func TestSyncPlatformConnectionStoresSnapshotsAndRunState(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	var requested []int64
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/supplier-account/api/openapi/metrics/query" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			AccountIDs []int64 `json:"account_ids"`
			Aggregate  bool    `json:"aggregate"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		requested = body.AccountIDs
		if body.Aggregate {
			t.Error("aggregate must be false")
		}
		_, _ = w.Write([]byte(`{"accounts":[{"id":12345,"aid":777,"account_alias":"alias-a","status":"online","requests":42,"success_rate":99.5,"prompt_tokens":1000,"completion_tokens":200}]}`))
	}))
	defer remote.Close()
	if _, err := a.DB.Exec(`UPDATE platform_connections SET base_url=? WHERE id='conn'`, remote.URL); err != nil {
		t.Fatal(err)
	}
	stamp := now()
	if _, err := a.DB.Exec(`INSERT INTO accounts(id,account,plan,created_at,updated_at) VALUES('acc','a@example.test','claude/cc-pro',?,?)`, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`INSERT INTO platform_links(id,account_id,connection_id,upstream_id,ingestion_state,created_at,updated_at) VALUES('link','acc','conn','12345','accepted',?,?)`, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err := a.syncAllPlatformUsage(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(requested) != 1 || requested[0] != 12345 {
		t.Fatalf("requested ids=%v", requested)
	}
	var alias, status, aid string
	var requests int64
	var rate float64
	if err := a.DB.QueryRow(`SELECT alias,runtime_status,upstream_aid,requests,success_rate FROM platform_metric_snapshots WHERE account_id='acc'`).Scan(&alias, &status, &aid, &requests, &rate); err != nil {
		t.Fatal(err)
	}
	if alias != "alias-a" || status != "online" || aid != "777" || requests != 42 || rate != 99.5 {
		t.Fatalf("snapshot=%s %s %s %d %v", alias, status, aid, requests, rate)
	}
	var linkStatus string
	_ = a.DB.QueryRow(`SELECT runtime_status FROM platform_links WHERE id='link'`).Scan(&linkStatus)
	if linkStatus != "online" {
		t.Fatalf("link status=%s", linkStatus)
	}
	var state string
	var processed int
	_ = a.DB.QueryRow(`SELECT state,processed FROM sync_runs WHERE kind='platform-usage' ORDER BY started_at DESC LIMIT 1`).Scan(&state, &processed)
	if state != "succeeded" || processed != 1 {
		t.Fatalf("run state=%s processed=%d", state, processed)
	}
	w := httptest.NewRecorder()
	a.accounts(w, httptest.NewRequest(http.MethodGet, "/accounts", nil))
	if !strings.Contains(w.Body.String(), `"platform_account_id":"777"`) || !strings.Contains(w.Body.String(), `"platform_requests":42`) {
		t.Fatalf("list does not expose the latest snapshot: %s", w.Body.String())
	}
	// A second sync on the same day upserts instead of duplicating.
	if err := a.syncAllPlatformUsage(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM platform_metric_snapshots WHERE account_id='acc'`).Scan(&count)
	if count != 1 {
		t.Fatalf("snapshots=%d, want 1", count)
	}
}

// A duplicate inside one batch must roll back every row of that batch.
func TestImportCommitIsAtomic(t *testing.T) {
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "local-dev-password")
	a, err := newApp(t.TempDir()+"/atomic-import.db", false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	stamp := now()
	if _, err := a.DB.Exec(`INSERT INTO accounts(id,account,created_at,updated_at) VALUES('existing','dup@example.test',?,?)`, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"format": "accounts", "content": `[{"account":"fresh@example.test","plan":"claude/cc-pro"},{"account":"dup@example.test","plan":"claude/cc-pro"}]`})
	preview := httptest.NewRecorder()
	a.importPreview(preview, httptest.NewRequest(http.MethodPost, "/imports/preview", strings.NewReader(string(body))))
	var result struct {
		Data struct {
			ID string `json:"import_id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(preview.Body.Bytes(), &result)
	commit := httptest.NewRecorder()
	a.importCommit(commit, httptest.NewRequest(http.MethodPost, "/imports/x/commit", strings.NewReader(`{"strategy":"fail"}`)), result.Data.ID)
	if commit.Code != http.StatusConflict {
		t.Fatalf("commit status=%d body=%s", commit.Code, commit.Body.String())
	}
	var count int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM accounts WHERE account='fresh@example.test'`).Scan(&count)
	if count != 0 {
		t.Fatal("first row survived a failed batch")
	}
	var state string
	_ = a.DB.QueryRow(`SELECT state FROM imports WHERE id=?`, result.Data.ID).Scan(&state)
	if state != "preview" {
		t.Fatalf("import state=%s, want preview", state)
	}
	retry := httptest.NewRecorder()
	a.importCommit(retry, httptest.NewRequest(http.MethodPost, "/imports/x/commit", strings.NewReader(`{"strategy":"skip_existing"}`)), result.Data.ID)
	if retry.Code != http.StatusOK || !strings.Contains(retry.Body.String(), `"created":1`) || !strings.Contains(retry.Body.String(), `"skipped":1`) {
		t.Fatalf("retry %d %s", retry.Code, retry.Body.String())
	}
}
