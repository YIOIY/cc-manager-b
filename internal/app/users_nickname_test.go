package app

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestUserNicknameCreateUpdateAndSession(t *testing.T) {
	a, admin := rbacApp(t)

	var created struct {
		Data struct {
			ID       string `json:"id"`
			Username string `json:"username"`
			Nickname string `json:"nickname"`
			Version  int    `json:"version"`
		} `json:"data"`
	}
	res := admin.do(http.MethodPost, "/api/v1/users", `{"username":"ops-nick","nickname":"小李","password":"operator-password","role_ids":["`+roleOperatorID+`"]}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d", res.StatusCode)
	}
	_ = jsonDecode(res, &created)
	if created.Data.Nickname != "小李" || created.Data.Username != "ops-nick" {
		t.Fatalf("create payload=%+v", created.Data)
	}

	res = admin.do(http.MethodPatch, "/api/v1/users/"+created.Data.ID, `{"nickname":"阿李","expected_version":`+strconv.Itoa(created.Data.Version)+`}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("patch nickname status=%d", res.StatusCode)
	}
	_ = jsonDecode(res, &created)
	if created.Data.Nickname != "阿李" {
		t.Fatalf("patched nickname=%q", created.Data.Nickname)
	}

	oversized := `{"nickname":"` + strings.Repeat("昵", nicknameMaxRunes+1) + `"}`
	if res := admin.do(http.MethodPatch, "/api/v1/users/"+created.Data.ID, oversized); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("long nickname status=%d", res.StatusCode)
	}

	ops := newTestClient(t, a)
	if res := ops.loginAs("ops-nick", "operator-password"); res.StatusCode != http.StatusOK {
		t.Fatalf("login=%d", res.StatusCode)
	}
	var session struct {
		Data struct {
			User struct {
				Username string `json:"username"`
				Nickname string `json:"nickname"`
			} `json:"user"`
		} `json:"data"`
	}
	_ = jsonDecode(ops.do(http.MethodGet, "/api/v1/auth/session", ""), &session)
	if session.Data.User.Nickname != "阿李" || session.Data.User.Username != "ops-nick" {
		t.Fatalf("session user=%+v", session.Data.User)
	}
}

func TestAccountCreateBindsCurrentUserAsCreator(t *testing.T) {
	a, admin := rbacApp(t)
	var me sessionEnvelope
	_ = jsonDecode(admin.do(http.MethodGet, "/api/v1/auth/session", ""), &me)

	if res := admin.do(http.MethodPatch, "/api/v1/users/"+me.Data.User.ID, `{"nickname":"总管"}`); res.StatusCode != http.StatusOK {
		t.Fatalf("set admin nickname=%d", res.StatusCode)
	}

	res := admin.do(http.MethodPost, "/api/v1/accounts", `{"account":"creator@example.test","plan":"claude/cc-pro"}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create account=%d", res.StatusCode)
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = jsonDecode(res, &created)

	var creatorID string
	if err := a.DB.QueryRow(`SELECT created_by_user_id FROM accounts WHERE id=?`, created.Data.ID).Scan(&creatorID); err != nil {
		t.Fatal(err)
	}
	if creatorID != me.Data.User.ID {
		t.Fatalf("created_by_user_id=%q want %q", creatorID, me.Data.User.ID)
	}

	var list struct {
		Data struct {
			Items []struct {
				ID                string `json:"id"`
				CreatedByUserID   string `json:"created_by_user_id"`
				CreatedByNickname string `json:"created_by_nickname"`
				CreatedByUsername string `json:"created_by_username"`
			} `json:"items"`
		} `json:"data"`
	}
	_ = jsonDecode(admin.do(http.MethodGet, "/api/v1/accounts?page=1&page_size=20&q=creator@example.test", ""), &list)
	if len(list.Data.Items) != 1 {
		t.Fatalf("list items=%d", len(list.Data.Items))
	}
	row := list.Data.Items[0]
	if row.CreatedByUserID != me.Data.User.ID || row.CreatedByNickname != "总管" || row.CreatedByUsername != "admin" {
		t.Fatalf("list creator=%+v", row)
	}
}

func TestUserNicknameMigrationAddsColumns(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/nick.db?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{
		`CREATE TABLE users(id TEXT PRIMARY KEY,username TEXT UNIQUE NOT NULL,password_hash TEXT NOT NULL,enabled INTEGER NOT NULL DEFAULT 1,version INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,last_login_at TEXT)`,
		`CREATE TABLE accounts(id TEXT PRIMARY KEY, account TEXT UNIQUE NOT NULL, plan TEXT NOT NULL DEFAULT '', register_location TEXT NOT NULL DEFAULT '', register_time TEXT NOT NULL DEFAULT '', plan_start_time TEXT NOT NULL DEFAULT '', plan_end_time TEXT NOT NULL DEFAULT '', proxy_id TEXT NOT NULL DEFAULT '', metadata_json TEXT NOT NULL DEFAULT '{}', credentials_json TEXT NOT NULL DEFAULT '{}', version INTEGER NOT NULL DEFAULT 1, archived INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL DEFAULT '')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := migrateUserNicknameAccountCreatorV14(tx); err != nil {
			tx.Rollback()
			t.Fatalf("pass %d: %v", i, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	for _, check := range []struct{ table, column string }{
		{"users", "nickname"},
		{"accounts", "created_by_user_id"},
	} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('`+check.table+`') WHERE name=?`, check.column).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s.%s present=%d err=%v", check.table, check.column, n, err)
		}
	}
}
