package app

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

func TestUserCanOnboardToggleAndGate(t *testing.T) {
	a, admin := rbacApp(t)

	res := admin.do(http.MethodPost, "/api/v1/users", `{"username":"ops-gate","password":"operator-password","role_ids":["`+roleOperatorID+`"]}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create operator status=%d", res.StatusCode)
	}
	var created struct {
		Data struct {
			ID         string `json:"id"`
			CanOnboard bool   `json:"can_onboard"`
			Version    int    `json:"version"`
		} `json:"data"`
	}
	_ = jsonDecode(res, &created)
	if !created.Data.CanOnboard {
		t.Fatalf("new user should allow onboard by default: %+v", created.Data)
	}

	ops := newTestClient(t, a)
	if res := ops.loginAs("ops-gate", "operator-password"); res.StatusCode != http.StatusOK {
		t.Fatalf("login=%d", res.StatusCode)
	}
	var session struct {
		Data struct {
			User struct {
				CanOnboard       bool     `json:"can_onboard"`
				OnboardAllowed   bool     `json:"onboard_allowed"`
				OnboardBlockCode string   `json:"onboard_block_code"`
				Permissions      []string `json:"permissions"`
			} `json:"user"`
		} `json:"data"`
	}
	_ = jsonDecode(ops.do(http.MethodGet, "/api/v1/auth/session", ""), &session)
	if !session.Data.User.CanOnboard || !session.Data.User.OnboardAllowed {
		t.Fatalf("session can_onboard=%v onboard_allowed=%v", session.Data.User.CanOnboard, session.Data.User.OnboardAllowed)
	}
	hasOnboard := false
	for _, p := range session.Data.User.Permissions {
		if p == string(PermAccountsOnboard) {
			hasOnboard = true
			break
		}
	}
	if !hasOnboard {
		t.Fatalf("operator should keep accounts.onboard in permissions: %v", session.Data.User.Permissions)
	}

	res = admin.do(http.MethodPatch, "/api/v1/users/"+created.Data.ID, `{"can_onboard":false,"expected_version":`+strconv.Itoa(created.Data.Version)+`}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("disable onboard status=%d", res.StatusCode)
	}
	_ = jsonDecode(res, &created)
	if created.Data.CanOnboard {
		t.Fatalf("patched can_onboard still true")
	}

	_ = jsonDecode(ops.do(http.MethodGet, "/api/v1/auth/session", ""), &session)
	if session.Data.User.CanOnboard || session.Data.User.OnboardAllowed || session.Data.User.OnboardBlockCode != "ONBOARD_DISABLED" {
		t.Fatalf("session should refresh can_onboard=false allowed=%v code=%q", session.Data.User.OnboardAllowed, session.Data.User.OnboardBlockCode)
	}

	for _, call := range []struct {
		method, path, body string
	}{
		{http.MethodPost, "/api/v1/claude-oauth/sessions", `{"proxy_id":""}`},
		{http.MethodPost, "/api/v1/accounts", `{"account":"blocked@example.test","plan":"claude/cc-pro"}`},
		{http.MethodPost, "/api/v1/accounts/onboard", `{}`},
		{http.MethodPost, "/api/v1/imports/preview", `{"format":"json","content":"[]"}`},
	} {
		res := ops.do(call.method, call.path, call.body)
		var env struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.NewDecoder(res.Body).Decode(&env)
		res.Body.Close()
		if res.StatusCode != http.StatusForbidden || env.Error.Code != "ONBOARD_DISABLED" {
			t.Fatalf("%s %s => status=%d code=%q", call.method, call.path, res.StatusCode, env.Error.Code)
		}
	}

	// Listing accounts still works; reauthorize permission path is separate.
	if listRes := ops.do(http.MethodGet, "/api/v1/accounts?page=1&page_size=10", ""); listRes.StatusCode != http.StatusOK {
		t.Fatalf("list accounts should remain allowed: %d", listRes.StatusCode)
	} else {
		listRes.Body.Close()
	}

	res = admin.do(http.MethodPatch, "/api/v1/users/"+created.Data.ID, `{"can_onboard":true,"expected_version":`+strconv.Itoa(created.Data.Version)+`}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("re-enable onboard status=%d", res.StatusCode)
	}
	_ = jsonDecode(res, &created)
	if !created.Data.CanOnboard {
		t.Fatalf("re-enabled can_onboard=false")
	}
	if createRes := ops.do(http.MethodPost, "/api/v1/accounts", `{"account":"allowed@example.test","plan":"claude/cc-pro"}`); createRes.StatusCode != http.StatusCreated {
		t.Fatalf("create after re-enable status=%d", createRes.StatusCode)
	} else {
		createRes.Body.Close()
	}
}

func TestUserCanOnboardMigrationAddsColumn(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/can-onboard.db?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE users(id TEXT PRIMARY KEY,username TEXT UNIQUE NOT NULL,password_hash TEXT NOT NULL,enabled INTEGER NOT NULL DEFAULT 1,version INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,last_login_at TEXT)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := migrateUserCanOnboardV17(tx); err != nil {
			tx.Rollback()
			t.Fatalf("pass %d: %v", i, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('users') WHERE name='can_onboard'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("can_onboard present=%d err=%v", n, err)
	}
}
