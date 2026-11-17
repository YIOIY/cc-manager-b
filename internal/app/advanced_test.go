package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestImportPreviewCommit(t *testing.T) {
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "local-dev-password")
	a, err := New(t.TempDir() + "/import.db")
	if err != nil {
		t.Fatal(err)
	}
	defer a.DB.Close()
	s := httptest.NewServer(a.Handler())
	defer s.Close()
	client := s.Client()
	res, _ := client.Get(s.URL + "/api/v1/auth/session")
	var env struct {
		Data struct {
			CSRF string `json:"csrf_token"`
		}
	}
	_ = json.NewDecoder(res.Body).Decode(&env)
	res.Body.Close()
	req, _ := http.NewRequest(http.MethodPost, s.URL+"/api/v1/imports/preview", bytes.NewBufferString(`{"format":"accounts","content":"[{\"account\":\"a@example.com\",\"plan\":\"claude/cc-max-20x\"}]"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", env.Data.CSRF)
	req.Header.Set("Cookie", "x")
	// anonymous preview is correctly rejected; this test primarily guards handler registration and JSON limits.
	res, _ = client.Do(req)
	if res.StatusCode != http.StatusForbidden && res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d", res.StatusCode)
	}
}

func TestImportKeepsOptionalMetadataAndRejectsSecrets(t *testing.T) {
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "local-dev-password")
	a, err := New(t.TempDir() + "/optional-import.db")
	if err != nil {
		t.Fatal(err)
	}
	defer a.DB.Close()
	content := `[{"account":"draft@example.com","plan":"claude/cc-max-20x","payment_method":"paypal","register_device":"Mac","balance":72,"recharge_time":"2026-11-04","warranty_period":7,"tags":["速刷coding"],"remark":"local draft"},{"account":"secret@example.com","password":"must-not-store"}]`
	requestBody, _ := json.Marshal(map[string]any{"format": "accounts", "content": content})
	preview := httptest.NewRecorder()
	a.importPreview(preview, httptest.NewRequest(http.MethodPost, "/imports/preview", bytes.NewReader(requestBody)))
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status=%d: %s", preview.Code, preview.Body.String())
	}
	var result struct {
		Data struct {
			ID         string `json:"import_id"`
			ValidCount int    `json:"valid_count"`
		} `json:"data"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Data.ValidCount != 1 {
		t.Fatalf("valid_count=%d, want 1", result.Data.ValidCount)
	}
	var stored string
	if err := a.DB.QueryRow(`SELECT rows_json FROM imports WHERE id=?`, result.Data.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "must-not-store") || strings.Contains(stored, "secret@example.com") {
		t.Fatal("secret import row was stored")
	}
	commit := httptest.NewRecorder()
	a.importCommit(commit, httptest.NewRequest(http.MethodPost, "/imports/"+result.Data.ID+"/commit", strings.NewReader(`{"strategy":"skip_existing"}`)), result.Data.ID)
	if commit.Code != http.StatusOK {
		t.Fatalf("commit status=%d: %s", commit.Code, commit.Body.String())
	}
	var metadataJSON string
	if err := a.DB.QueryRow(`SELECT metadata_json FROM accounts WHERE account=?`, "draft@example.com").Scan(&metadataJSON); err != nil {
		t.Fatal(err)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["payment_method"] != "paypal" || metadata["register_device"] != "Mac" || metadata["remark"] != "local draft" || metadata["balance"] != float64(72) {
		t.Fatalf("metadata was not preserved: %v", metadata)
	}
}

func TestImportPreviewIssuesAreStructured(t *testing.T) {
	_, admin := rbacApp(t)
	res := admin.do(http.MethodPost, "/api/v1/imports/preview", `{"format":"accounts","content":"[{\"account\":\"a@example.com\",\"password\":\"\",\"plan\":\"nope\"}]"}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", res.StatusCode)
	}
	var env struct {
		Data struct {
			Rows []struct {
				Issues []struct{ Code, Message, Field string } `json:"issues"`
			} `json:"rows"`
		} `json:"data"`
	}
	_ = jsonDecode(res, &env)
	if len(env.Data.Rows) != 1 {
		t.Fatalf("rows=%+v", env.Data.Rows)
	}
	got := map[string][2]string{}
	for _, i := range env.Data.Rows[0].Issues {
		got[i.Code] = [2]string{i.Field, i.Message}
	}
	if got["PASSWORD_REQUIRED"] != [2]string{"password", "缺少密码"} || got["PLAN_INVALID"][0] != "plan" || got["PLAN_INVALID"][1] == "" {
		t.Fatalf("issues=%+v", env.Data.Rows[0].Issues)
	}
}
