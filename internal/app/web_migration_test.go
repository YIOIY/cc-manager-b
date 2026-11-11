package app

import (
	"database/sql"
	"encoding/base64"
	"sort"
	"strings"
	"testing"
)

func tableColumns(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err = rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func TestWebChannelMigrationAddsTablesAndColumns(t *testing.T) {
	legacy, err := sql.Open("sqlite", t.TempDir()+"/legacy.db")
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	// A production database at migration 9: the fresh schema plus every
	// earlier migration, with one connection row.
	if _, err = legacy.Exec(baseSchema); err != nil {
		t.Fatal(err)
	}
	if err = runMigrations(legacy, 9); err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec(`INSERT INTO platform_connections(id,name,base_url,token_secret,timezone,created_at,updated_at) VALUES('conn','n','https://x','tok','Asia/Shanghai','','')`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = runMigrations(legacy, 10); err != nil {
			t.Fatal(err)
		}
	}
	cols := strings.Join(tableColumns(t, legacy, "platform_connections"), ",")
	for _, want := range []string{"web_username", "web_password_secret", "web_suspended_at", "web_suspended_reason"} {
		if !strings.Contains(cols, want) {
			t.Fatalf("platform_connections lacks %s: %s", want, cols)
		}
	}
	var username, suspended string
	if err = legacy.QueryRow(`SELECT web_username,web_suspended_at FROM platform_connections WHERE id='conn'`).Scan(&username, &suspended); err != nil || username != "" || suspended != "" {
		t.Fatalf("defaults not empty strings: %q %q %v", username, suspended, err)
	}
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "migration-test-password")
	t.Setenv("CC_MANAGER_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	fresh, err := newApp(t.TempDir()+"/fresh.db", false)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.DB.Close()
	for table, required := range map[string][]string{
		"platform_remote_accounts": {"connection_id", "upstream_id", "upstream_aid", "account_email", "ext_status", "missing_since", "superseded_by", "auto_created", "usage_cost_micro", "five_hour_used", "reason_text", "raw_json", "account_id"},
		"platform_usage_snapshots": {"connection_id", "upstream_id", "account_id", "observed_at", "usage_cost_micro", "five_hour_used", "week_reset_at"},
		"platform_bill_models":     {"connection_id", "upstream_id", "account_id", "bill_date", "model_name", "bill_model_name", "cost_fen", "generation", "final"},
	} {
		migrated := tableColumns(t, legacy, table)
		if len(migrated) == 0 {
			t.Fatalf("table %s missing after migration", table)
		}
		joined := strings.Join(migrated, ",")
		for _, c := range required {
			if !strings.Contains(joined, c) {
				t.Fatalf("%s lacks column %s: %s", table, c, joined)
			}
		}
		if got := strings.Join(tableColumns(t, fresh.DB, table), ","); got != joined {
			t.Fatalf("%s differs between fresh and migrated schema:\n fresh    %s\n migrated %s", table, got, joined)
		}
	}
	var version int
	if err = legacy.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != 10 {
		t.Fatalf("version=%d err=%v", version, err)
	}
}
