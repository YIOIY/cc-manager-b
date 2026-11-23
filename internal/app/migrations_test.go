package app

import (
	"database/sql"
	"net/http"
	"testing"
)

func TestLegacyBillMigrationPreservesDataAndIsIdempotent(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/legacy.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE platform_metric_snapshots(account_id TEXT,connection_id TEXT);
CREATE TABLE platform_bill_daily(id TEXT,account_id TEXT,connection_id TEXT,upstream_id TEXT,bill_date TEXT,request_count INTEGER,prompt_tokens INTEGER,completion_tokens INTEGER,cache_read_input_tokens INTEGER,cache_write_input_tokens INTEGER,cost_fen INTEGER,source_range_json TEXT,fetched_at TEXT,revision INTEGER,finalized INTEGER,sync_generation INTEGER,UNIQUE(account_id,connection_id,bill_date));
CREATE TABLE sync_runs(id TEXT PRIMARY KEY,connection_id TEXT,kind TEXT,state TEXT,started_at TEXT,finished_at TEXT,last_success_at TEXT,error_message TEXT,processed INTEGER DEFAULT 0,failed INTEGER DEFAULT 0);
INSERT INTO platform_bill_daily VALUES('row','account','conn','42','2026-11-04',0,0,0,0,0,1234,'{}','2026-11-05T00:00:00Z',17,0,1793836800000000000);`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = runMigrations(db, 1); err != nil {
			t.Fatal(err)
		}
	}
	var amount, oldGeneration int64
	var revision, verified, generation int
	err = db.QueryRow(`SELECT cost_fen,revision,response_verified,write_generation,sync_generation FROM platform_bill_daily`).Scan(&amount, &revision, &verified, &generation, &oldGeneration)
	if err != nil {
		t.Fatal(err)
	}
	if amount != 1234 || revision != 17 || verified != 0 || generation != 0 || oldGeneration != 1793836800000000000 {
		t.Fatalf("migration changed historical values: %d %d %d %d %d", amount, revision, verified, generation, oldGeneration)
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=1`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("versions=%d err=%v", count, err)
	}
}

func TestBillMigrationFailureRollsBackSchema(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/invalid.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE platform_metric_snapshots(account_id TEXT,connection_id TEXT);CREATE TABLE platform_bill_daily(account_id TEXT);CREATE TABLE sync_runs(id TEXT);`); err != nil {
		t.Fatal(err)
	}
	if err = runMigrations(db, 1); err == nil {
		t.Fatal("accepted invalid legacy schema")
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed migration was committed: %d %v", count, err)
	}
}

// A database whose users table came from migration 4 has role NOT NULL with
// no default, which made every user INSERT fail after RBAC moved roles out.
func TestLegacyUsersRoleColumnIsDroppedSoUsersCanBeCreated(t *testing.T) {
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "correct-horse-battery")
	path := t.TempDir() + "/legacy-users.db"
	db, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE users(id TEXT PRIMARY KEY,username TEXT UNIQUE NOT NULL,password_hash TEXT NOT NULL,role TEXT NOT NULL,enabled INTEGER NOT NULL DEFAULT 1,version INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,last_login_at TEXT)`,
		`INSERT INTO users(id,username,password_hash,role,enabled,version,created_at,updated_at) VALUES('u-admin','admin','$argon2id$v=19$m=65536,t=3,p=4$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA','admin',1,1,'2026-11-08T00:00:00Z','2026-11-08T00:00:00Z')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	a, err := newApp(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	var hasRole int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('users') WHERE name='role'`).Scan(&hasRole)
	if hasRole != 0 {
		t.Fatal("legacy role column still present")
	}
	var bound int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM user_roles WHERE user_id='u-admin' AND role_id=?`, roleAdminID).Scan(&bound)
	if bound != 1 {
		t.Fatal("legacy admin lost its role binding")
	}
	// The stored hash above is a placeholder; reset so we can log in.
	if err := a.ResetPassword("admin", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	c := newTestClient(t, a)
	c.login("correct-horse-battery")
	if res := c.do(http.MethodPost, "/api/v1/users", `{"username":"anzhente","password":"anzhente-password-1","role_ids":["`+roleOperatorID+`"]}`); res.StatusCode != http.StatusCreated {
		var body struct {
			Error struct{ Code, Message string }
		}
		_ = jsonDecode(res, &body)
		t.Fatalf("create user status=%d %+v", res.StatusCode, body.Error)
	}
}

// A database that ran the first cut of v19 holds dispatch values the settings
// endpoint refuses (probe cache 120 s, preheat 0/0 and 300–900 s). v21 moves
// only those fields to the documented defaults and leaves in-range values an
// operator saved untouched.
func TestOAuthDispatchDefaultsMigrationRepairsOutOfRangeRow(t *testing.T) {
	for name, tc := range map[string]struct {
		row  string
		want [8]int
	}{
		"first-cut defaults": {"1,3,2,3,120,0,0,300,900,'[]',1", [8]int{3, 2, 3, 30, 1, 3, 5, 15}},
		"operator values":    {"1,5,1,10,10,2,2,6,9,'[]',4", [8]int{5, 1, 10, 10, 2, 2, 6, 9}},
	} {
		t.Run(name, func(t *testing.T) {
			db, err := sql.Open("sqlite", t.TempDir()+"/dispatch.db")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err = db.Exec(baseSchema); err != nil {
				t.Fatal(err)
			}
			if err = runMigrations(db, 20); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`DELETE FROM oauth_dispatch_settings; INSERT INTO oauth_dispatch_settings VALUES(` + tc.row + `)`); err != nil {
				t.Fatal(err)
			}
			if err = runMigrations(db, 21); err != nil {
				t.Fatal(err)
			}
			var got [8]int
			if err = db.QueryRow(`SELECT global_concurrency,exit_concurrency,exchange_interval_seconds,probe_cache_seconds,manual_preheat_seconds_min,manual_preheat_seconds_max,batch_preheat_seconds_min,batch_preheat_seconds_max FROM oauth_dispatch_settings WHERE id=1`).Scan(&got[0], &got[1], &got[2], &got[3], &got[4], &got[5], &got[6], &got[7]); err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("after v21: %v want %v", got, tc.want)
			}
		})
	}
}
