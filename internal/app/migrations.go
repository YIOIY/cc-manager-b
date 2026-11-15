package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// migration steps are additive so a previous binary can still read the
// database after an upgrade. Each step runs in its own transaction and is
// recorded in schema_migrations; a failed step leaves no partial schema.
type migration struct {
	version int
	name    string
	up      func(tx *sql.Tx) error
}

var migrations = []migration{
	{1, "bill-sync", migrateBillSyncV1},
	{2, "proxy-network", migrateProxyNetworkV2},
	{3, "indexes-defaults-audit", migrateIndexesV3},
	{4, "users", migrateUsersV4},
	{5, "rbac", migrateRBACV5},
	{6, "proxy-permissions", migrateProxyPermissionsV6},
	{7, "drop-users-role", migrateDropUsersRoleV7},
	{8, "account-delete-permission", migrateAccountDeletePermissionV8},
	{9, "sync-run-error-code", migrateSyncRunErrorCodeV9},
	{10, "web-channel", migrateWebChannelV10},
	{11, "exchange-rates", migrateExchangeRatesV11},
	{12, "platform-rate", migratePlatformRateV12},
	{13, "feedback", migrateFeedbackV13},
	{14, "user-nickname-account-creator", migrateUserNicknameAccountCreatorV14},
	{15, "proxy-creator", migrateProxyCreatorV15},
}

func migrate(db *sql.DB) error { return runMigrations(db, len(migrations)) }

// runMigrations applies every step up to and including maxVersion.
func runMigrations(db *sql.DB, maxVersion int) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	for _, m := range migrations {
		if m.version > maxVersion {
			break
		}
		var applied int
		if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=?`, m.version).Scan(&applied); err != nil {
			return err
		}
		if applied != 0 {
			continue
		}
		if err := applyMigration(db, m); err != nil {
			return fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
		}
	}
	return nil
}

func applyMigration(db *sql.DB, m migration) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = m.up(tx); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,strftime('%Y-%m-%dT%H:%M:%SZ','now'))`, m.version); err != nil {
		return err
	}
	return tx.Commit()
}

// addColumns adds each "name TYPE ..." definition that the table lacks.
func addColumns(tx *sql.Tx, table string, definitions []string) error {
	rows, err := tx.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return err
	}
	existing := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var def any
		if err = rows.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		existing[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(existing) == 0 {
		return fmt.Errorf("table %s does not exist", table)
	}
	for _, definition := range definitions {
		name, _, _ := strings.Cut(definition, " ")
		if !existing[name] {
			if _, err = tx.Exec("ALTER TABLE " + table + " ADD COLUMN " + definition); err != nil {
				return err
			}
		}
	}
	return nil
}

func execAll(tx *sql.Tx, statements ...string) error {
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

// migrationColumns lists the columns added by migration 1 so a test can
// assert the fresh-database schema in app.go declares the same columns.
var migrationColumns = map[string][]string{
	"platform_metric_snapshots": {"bucket_key TEXT NOT NULL DEFAULT ''", "observed_window_end TEXT", "upstream_aid TEXT"},
	"platform_bill_daily":       {"requested_start_ds TEXT", "requested_end_ds TEXT", "actual_start_ds TEXT", "actual_end_ds TEXT", "source_timezone TEXT", "response_verified INTEGER NOT NULL DEFAULT 0", "write_generation INTEGER NOT NULL DEFAULT 0", "last_changed_at TEXT"},
	"sync_runs":                 {"request_json TEXT NOT NULL DEFAULT '{}'", "fingerprint TEXT NOT NULL DEFAULT ''", "idem_key TEXT", "parent_id TEXT", "total_items INTEGER NOT NULL DEFAULT 0", "created_at TEXT", "updated_at TEXT", "owner TEXT NOT NULL DEFAULT ''", "lease_until INTEGER NOT NULL DEFAULT 0", "http_requests INTEGER NOT NULL DEFAULT 0", "http_retries INTEGER NOT NULL DEFAULT 0"},
}

func migrateBillSyncV1(tx *sql.Tx) error {
	for table, definitions := range migrationColumns {
		if err := addColumns(tx, table, definitions); err != nil {
			return err
		}
	}
	return execAll(tx,
		`CREATE UNIQUE INDEX IF NOT EXISTS ux_platform_metric_bucket ON platform_metric_snapshots(account_id,connection_id,bucket_key)`,
		`CREATE INDEX IF NOT EXISTS ix_bills_connection_date ON platform_bill_daily(connection_id,bill_date,account_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS ux_bill_run_idem ON sync_runs(kind,idem_key) WHERE idem_key IS NOT NULL`,
		`CREATE TABLE IF NOT EXISTS bill_sync_items(id TEXT PRIMARY KEY,run_id TEXT NOT NULL REFERENCES sync_runs(id),connection_id TEXT NOT NULL,account_id TEXT NOT NULL,upstream_id TEXT NOT NULL,source_timezone TEXT NOT NULL,bill_date TEXT NOT NULL,state TEXT NOT NULL DEFAULT 'queued',generation INTEGER NOT NULL DEFAULT 0,attempts INTEGER NOT NULL DEFAULT 0,error_code TEXT NOT NULL DEFAULT '',error_message TEXT NOT NULL DEFAULT '',updated_at TEXT NOT NULL,UNIQUE(run_id,connection_id,account_id,bill_date))`,
		`CREATE TABLE IF NOT EXISTS bill_run_keys(key TEXT PRIMARY KEY,payload_hash TEXT NOT NULL,run_id TEXT NOT NULL REFERENCES sync_runs(id))`,
		`CREATE INDEX IF NOT EXISTS ix_bill_items_run_state ON bill_sync_items(run_id,state,bill_date)`,
		`CREATE TABLE IF NOT EXISTS bill_sync_buckets(connection_id TEXT NOT NULL,account_id TEXT NOT NULL,bill_date TEXT NOT NULL,generation INTEGER NOT NULL DEFAULT 0,last_state TEXT NOT NULL DEFAULT 'queued',last_attempt_at TEXT,PRIMARY KEY(connection_id,account_id,bill_date))`,
	)
}

func migrateProxyNetworkV2(tx *sql.Tx) error {
	return execAll(tx,
		`CREATE TABLE IF NOT EXISTS proxy_probe_settings(id INTEGER PRIMARY KEY CHECK(id=1),mode TEXT NOT NULL,upstream_url TEXT NOT NULL,username TEXT NOT NULL,password_secret TEXT NOT NULL,timeout_seconds INTEGER NOT NULL,version INTEGER NOT NULL)`,
		`INSERT OR IGNORE INTO proxy_probe_settings VALUES(1,'direct','','','',20,1)`,
		`CREATE INDEX IF NOT EXISTS ix_proxy_checks_recent ON proxy_checks(proxy_id,checked_at DESC)`,
		`CREATE TABLE IF NOT EXISTS proxy_check_details(check_id TEXT PRIMARY KEY REFERENCES proxy_checks(id) ON DELETE CASCADE,stage TEXT NOT NULL,message TEXT NOT NULL,route_mode TEXT NOT NULL,warning TEXT NOT NULL)`,
	)
}

// migrateIndexesV3 adds the indexes the workers and list endpoints rely on,
// backfills NULL text columns on legacy rows so scans no longer drop them,
// and extends audit_events with actor/detail.
func migrateIndexesV3(tx *sql.Tx) error {
	if err := addColumns(tx, "audit_events", []string{"actor TEXT NOT NULL DEFAULT ''", "detail TEXT NOT NULL DEFAULT ''"}); err != nil {
		return err
	}
	if err := addColumns(tx, "jobs", []string{"result_json TEXT"}); err != nil {
		return err
	}
	return execAll(tx,
		`UPDATE accounts SET plan=COALESCE(plan,''),register_location=COALESCE(register_location,''),register_time=COALESCE(register_time,''),plan_start_time=COALESCE(plan_start_time,''),plan_end_time=COALESCE(plan_end_time,''),proxy_id=COALESCE(proxy_id,''),metadata_json=COALESCE(metadata_json,'{}'),credentials_json=COALESCE(credentials_json,'{}'),created_at=COALESCE(created_at,''),updated_at=COALESCE(updated_at,'')`,
		`UPDATE proxies SET name=COALESCE(name,''),scheme=COALESCE(scheme,''),host=COALESCE(host,''),port=COALESCE(port,0),username=COALESCE(username,''),password_secret=COALESCE(password_secret,''),created_at=COALESCE(created_at,''),updated_at=COALESCE(updated_at,'')`,
		`UPDATE platform_connections SET name=COALESCE(name,''),base_url=COALESCE(base_url,''),token_secret=COALESCE(token_secret,''),timezone=COALESCE(timezone,''),allowed_plans_json=COALESCE(allowed_plans_json,'[]'),created_at=COALESCE(created_at,''),updated_at=COALESCE(updated_at,'')`,
		`UPDATE jobs SET kind=COALESCE(kind,''),state=COALESCE(state,''),account_id=COALESCE(account_id,''),connection_id=COALESCE(connection_id,''),payload_json=COALESCE(payload_json,'{}'),result_json=COALESCE(result_json,'{}'),created_at=COALESCE(created_at,''),updated_at=COALESCE(updated_at,'')`,
		`CREATE INDEX IF NOT EXISTS ix_jobs_state_created ON jobs(state,created_at)`,
		`CREATE INDEX IF NOT EXISTS ix_jobs_account_created ON jobs(account_id,created_at)`,
		`CREATE INDEX IF NOT EXISTS ix_jobs_connection_state ON jobs(connection_id,state)`,
		`CREATE INDEX IF NOT EXISTS ix_sync_runs_kind_state ON sync_runs(kind,state,connection_id,lease_until)`,
		`CREATE INDEX IF NOT EXISTS ix_sync_runs_connection_created ON sync_runs(connection_id,kind,created_at)`,
		`CREATE INDEX IF NOT EXISTS ix_sync_runs_started ON sync_runs(started_at)`,
		`CREATE INDEX IF NOT EXISTS ix_metric_snapshots_account_fetched ON platform_metric_snapshots(account_id,fetched_at)`,
		`CREATE INDEX IF NOT EXISTS ix_metric_snapshots_fetched ON platform_metric_snapshots(fetched_at)`,
		`CREATE INDEX IF NOT EXISTS ix_bill_daily_account_date ON platform_bill_daily(account_id,bill_date)`,
		`CREATE INDEX IF NOT EXISTS ix_bill_daily_date ON platform_bill_daily(bill_date)`,
		`CREATE INDEX IF NOT EXISTS ix_bill_items_run_date_state ON bill_sync_items(run_id,bill_date,state,account_id)`,
		`CREATE INDEX IF NOT EXISTS ix_bill_buckets_connection_date ON bill_sync_buckets(connection_id,bill_date,account_id)`,
		`CREATE INDEX IF NOT EXISTS ix_deliveries_account ON deliveries(account_id,updated_at)`,
		`CREATE INDEX IF NOT EXISTS ix_platform_links_account ON platform_links(account_id,updated_at)`,
		`CREATE INDEX IF NOT EXISTS ix_platform_links_connection_state ON platform_links(connection_id,ingestion_state)`,
		`CREATE INDEX IF NOT EXISTS ix_imports_expires ON imports(expires_at)`,
		`CREATE INDEX IF NOT EXISTS ix_idempotency_created ON idempotency_keys(created_at)`,
		`CREATE INDEX IF NOT EXISTS ix_audit_created ON audit_events(created_at)`,
		`CREATE INDEX IF NOT EXISTS ix_accounts_created ON accounts(created_at)`,
		`CREATE INDEX IF NOT EXISTS ix_accounts_archived ON accounts(archived)`,
	)
}

// migrateUsersV4 introduces database-backed users and roles. The first admin
// is created from the environment at startup (see ensureBootstrapAdmin).
func migrateUsersV4(tx *sql.Tx) error {
	return execAll(tx,
		`CREATE TABLE IF NOT EXISTS users(id TEXT PRIMARY KEY,username TEXT UNIQUE NOT NULL,password_hash TEXT NOT NULL,role TEXT NOT NULL,enabled INTEGER NOT NULL DEFAULT 1,version INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,last_login_at TEXT)`,
	)
}

// migrateRBACV5 adds database roles and user-role bindings, seeds the builtin
// roles and converts the fixed users.role column into bindings.
func migrateRBACV5(tx *sql.Tx) error {
	if err := execAll(tx,
		`CREATE TABLE IF NOT EXISTS roles(id TEXT PRIMARY KEY,name TEXT UNIQUE NOT NULL,description TEXT NOT NULL DEFAULT '',builtin INTEGER NOT NULL DEFAULT 0,permissions_json TEXT NOT NULL DEFAULT '[]',version INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL,updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS user_roles(user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,role_id TEXT NOT NULL REFERENCES roles(id),PRIMARY KEY(user_id,role_id))`,
		`CREATE INDEX IF NOT EXISTS ix_user_roles_role ON user_roles(role_id)`,
	); err != nil {
		return err
	}
	if err := seedBuiltinRoles(tx); err != nil {
		return err
	}
	// Legacy fixed roles (migration 4) become bindings; the column stays but is unused.
	if err := addColumns(tx, "users", []string{"role TEXT NOT NULL DEFAULT ''"}); err != nil {
		return err
	}
	for legacy, id := range map[string]string{"admin": roleAdminID, "operator": roleOperatorID, "viewer": roleViewerID} {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO user_roles(user_id,role_id) SELECT id,? FROM users WHERE role=?`, id, legacy); err != nil {
			return err
		}
	}
	return nil
}

// seedBuiltinRoles inserts the builtin roles when missing. Their permission
// sets are only written on first creation so operator edits survive upgrades.
func seedBuiltinRoles(tx *sql.Tx) error {
	stamp := strftimeNow()
	for _, role := range builtinRoles() {
		encoded, _ := json.Marshal(role.permissions)
		if _, err := tx.Exec(`INSERT OR IGNORE INTO roles(id,name,description,builtin,permissions_json,version,created_at,updated_at) VALUES(?,?,?,1,?,1,?,?)`, role.id, role.name, role.description, string(encoded), stamp, stamp); err != nil {
			return err
		}
	}
	return nil
}

func strftimeNow() string { return time.Now().UTC().Format(time.RFC3339) }

// migrateProxyPermissionsV6 adds proxies.update / proxies.delete to the
// builtin operator role, which was seeded before those actions existed.
// Admin is a superuser and custom roles are left to their owners.
func migrateProxyPermissionsV6(tx *sql.Tx) error {
	return grantBuiltinRolePermissions(tx, roleOperatorID, string(PermProxiesUpdate), string(PermProxiesDelete))
}

// migrateSyncRunErrorCodeV9 stores the classified failure code next to the
// sanitized message so the dashboard can explain a failing sync.
func migrateSyncRunErrorCodeV9(tx *sql.Tx) error {
	_, err := tx.Exec(`ALTER TABLE sync_runs ADD COLUMN error_code TEXT NOT NULL DEFAULT ''`)
	return err
}

// migrateAccountDeletePermissionV8 lets the builtin operator role delete
// accounts that were never accepted by the platform.
func migrateAccountDeletePermissionV8(tx *sql.Tx) error {
	return grantBuiltinRolePermissions(tx, roleOperatorID, string(PermAccountsDelete))
}

// grantBuiltinRolePermissions unions extra permissions into a stored role.
func grantBuiltinRolePermissions(tx *sql.Tx, roleID string, extra ...string) error {
	var encoded string
	err := tx.QueryRow(`SELECT permissions_json FROM roles WHERE id=?`, roleID).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var current []string
	_ = json.Unmarshal([]byte(encoded), &current)
	have := map[string]bool{}
	for _, p := range current {
		have[p] = true
	}
	changed := false
	for _, p := range extra {
		if !have[p] {
			current = append(current, p)
			have[p] = true
			changed = true
		}
	}
	if !changed {
		return nil
	}
	out, _ := json.Marshal(current)
	_, err = tx.Exec(`UPDATE roles SET permissions_json=?,version=version+1,updated_at=? WHERE id=?`, string(out), strftimeNow(), roleID)
	return err
}

// migrateDropUsersRoleV7 removes the legacy users.role column. Databases that
// created the table through migration 4 have it as NOT NULL without a
// default, so every INSERT since RBAC failed with a constraint error; roles
// live in user_roles since migration 5 and nothing reads the column.
func migrateDropUsersRoleV7(tx *sql.Tx) error {
	has, err := hasColumn(tx, "users", "role")
	if err != nil || !has {
		return err
	}
	_, err = tx.Exec(`ALTER TABLE users DROP COLUMN role`)
	return err
}

func hasColumn(tx *sql.Tx, table, column string) (bool, error) {
	rows, err := tx.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var def any
		if err = rows.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// webChannelColumns are the platform_connections columns the supplier web
// (console) channel adds. They live here so the migration and baseSchema
// agree; text columns use ” for "not set" so lists never hit NULL.
var webChannelColumns = []string{
	"web_username TEXT NOT NULL DEFAULT ''",
	"web_password_secret TEXT NOT NULL DEFAULT ''",
	"web_suspended_at TEXT NOT NULL DEFAULT ''",
	"web_suspended_reason TEXT NOT NULL DEFAULT ''",
}

// webChannelTables hold what the web channel reads from the supplier console:
// a snapshot of every remote account, a history of its usage block, and the
// per-model daily bills. Shared by baseSchema and migration 10.
const webChannelTables = `
CREATE TABLE IF NOT EXISTS platform_remote_accounts(connection_id TEXT NOT NULL,upstream_id TEXT NOT NULL,upstream_aid TEXT NOT NULL DEFAULT '',account_email TEXT NOT NULL DEFAULT '',alias TEXT NOT NULL DEFAULT '',provider TEXT NOT NULL DEFAULT '',model TEXT NOT NULL DEFAULT '',models_json TEXT NOT NULL DEFAULT '[]',ext_status TEXT NOT NULL DEFAULT '',a1_status TEXT NOT NULL DEFAULT '',a2_status TEXT NOT NULL DEFAULT '',is_locked INTEGER NOT NULL DEFAULT 0,proxy_type TEXT NOT NULL DEFAULT '',usage_limit_json TEXT NOT NULL DEFAULT '{}',remark TEXT NOT NULL DEFAULT '',remote_created_at TEXT NOT NULL DEFAULT '',remote_updated_at TEXT NOT NULL DEFAULT '',first_seen_at TEXT NOT NULL DEFAULT '',last_seen_at TEXT NOT NULL DEFAULT '',missing_since TEXT NOT NULL DEFAULT '',superseded_by TEXT NOT NULL DEFAULT '',reason_kind TEXT NOT NULL DEFAULT '',reason_text TEXT NOT NULL DEFAULT '',reason_raw TEXT NOT NULL DEFAULT '',reason_fetched_at TEXT NOT NULL DEFAULT '',reason_status TEXT NOT NULL DEFAULT '',account_id TEXT NOT NULL DEFAULT '',auto_created INTEGER NOT NULL DEFAULT 0,plan TEXT NOT NULL DEFAULT '',plan_start_time TEXT NOT NULL DEFAULT '',plan_end_time TEXT NOT NULL DEFAULT '',register_time TEXT NOT NULL DEFAULT '',register_location TEXT NOT NULL DEFAULT '',balance TEXT NOT NULL DEFAULT '',payment_method TEXT NOT NULL DEFAULT '',register_device TEXT NOT NULL DEFAULT '',recharge_time TEXT NOT NULL DEFAULT '',warranty_period TEXT NOT NULL DEFAULT '',tags_json TEXT NOT NULL DEFAULT '[]',usage_cost_micro INTEGER,usage_input_tokens INTEGER,usage_output_tokens INTEGER,five_hour_used REAL,five_hour_reset_at TEXT NOT NULL DEFAULT '',week_used REAL,week_reset_at TEXT NOT NULL DEFAULT '',usage_observed_at TEXT NOT NULL DEFAULT '',raw_json TEXT NOT NULL DEFAULT '{}',PRIMARY KEY(connection_id,upstream_id));
CREATE INDEX IF NOT EXISTS ix_remote_accounts_account ON platform_remote_accounts(account_id);
CREATE INDEX IF NOT EXISTS ix_remote_accounts_email ON platform_remote_accounts(connection_id,account_email);
CREATE TABLE IF NOT EXISTS platform_usage_snapshots(id TEXT PRIMARY KEY,connection_id TEXT NOT NULL,upstream_id TEXT NOT NULL,account_id TEXT NOT NULL DEFAULT '',observed_at TEXT NOT NULL,usage_cost_micro INTEGER,usage_input_tokens INTEGER,usage_output_tokens INTEGER,five_hour_used REAL,five_hour_reset_at TEXT NOT NULL DEFAULT '',week_used REAL,week_reset_at TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS ix_usage_snapshots_account ON platform_usage_snapshots(account_id,observed_at);
CREATE INDEX IF NOT EXISTS ix_usage_snapshots_observed ON platform_usage_snapshots(observed_at);
CREATE TABLE IF NOT EXISTS platform_bill_models(id TEXT PRIMARY KEY,connection_id TEXT NOT NULL,upstream_id TEXT NOT NULL,account_id TEXT NOT NULL DEFAULT '',bill_date TEXT NOT NULL,model_name TEXT NOT NULL,bill_model_name TEXT NOT NULL DEFAULT '',cost_fen INTEGER NOT NULL DEFAULT 0,source_range_json TEXT NOT NULL DEFAULT '{}',fetched_at TEXT NOT NULL,generation INTEGER NOT NULL DEFAULT 0,final INTEGER NOT NULL DEFAULT 0,UNIQUE(connection_id,upstream_id,bill_date,model_name));
CREATE INDEX IF NOT EXISTS ix_bill_models_connection_date ON platform_bill_models(connection_id,bill_date);
CREATE INDEX IF NOT EXISTS ix_bill_models_account_date ON platform_bill_models(account_id,bill_date);
`

// migrateWebChannelV10 adds the supplier web channel: console credentials and
// suspension state on the connection, plus the three read-only snapshot tables.
func migrateWebChannelV10(tx *sql.Tx) error {
	if err := addColumns(tx, "platform_connections", webChannelColumns); err != nil {
		return err
	}
	_, err := tx.Exec(webChannelTables)
	return err
}

// migrateExchangeRatesV11 added a table for a market reference rate. It was
// the wrong rate (the platform bills at its own fixed rate) and version 12
// removes the table again; the step stays so applied versions line up.
func migrateExchangeRatesV11(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS exchange_rates(pair TEXT PRIMARY KEY,rate REAL NOT NULL,source TEXT NOT NULL DEFAULT '',rate_date TEXT NOT NULL DEFAULT '',fetched_at TEXT NOT NULL)`)
	return err
}

// platformRateColumns is shared with baseSchema's platform_connections.
var platformRateColumns = []string{"usd_cny_rate REAL NOT NULL DEFAULT 7.2"}

// migratePlatformRateV12 gives every connection the platform's fixed
// USD→RMB rate and drops the market-rate table of version 11.
func migratePlatformRateV12(tx *sql.Tx) error {
	if err := addColumns(tx, "platform_connections", platformRateColumns); err != nil {
		return err
	}
	_, err := tx.Exec(`DROP TABLE IF EXISTS exchange_rates`)
	return err
}

// migrateFeedbackV13 adds the table behind 用户之声.
func migrateFeedbackV13(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS feedback(id TEXT PRIMARY KEY,user_id TEXT NOT NULL DEFAULT '',username TEXT NOT NULL DEFAULT '',kind TEXT NOT NULL DEFAULT 'problem',title TEXT NOT NULL DEFAULT '',body TEXT NOT NULL DEFAULT '',page TEXT NOT NULL DEFAULT '',context_json TEXT NOT NULL DEFAULT '{}',app_version TEXT NOT NULL DEFAULT '',status TEXT NOT NULL DEFAULT 'open',reply TEXT NOT NULL DEFAULT '',handled_by TEXT NOT NULL DEFAULT '',handled_at TEXT NOT NULL DEFAULT '',version INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS ix_feedback_status_created ON feedback(status,created_at);
CREATE INDEX IF NOT EXISTS ix_feedback_user ON feedback(user_id,created_at);`)
	return err
}

// migrateUserNicknameAccountCreatorV14 adds display nicknames for system
// users and records which login user created each Claude account.
func migrateUserNicknameAccountCreatorV14(tx *sql.Tx) error {
	if err := addColumns(tx, "users", []string{"nickname TEXT NOT NULL DEFAULT ''"}); err != nil {
		return err
	}
	return addColumns(tx, "accounts", []string{"created_by_user_id TEXT NOT NULL DEFAULT ''"})
}

// migrateProxyCreatorV15 records which login user created each proxy so the
// pool can be private per operator.
func migrateProxyCreatorV15(tx *sql.Tx) error {
	return addColumns(tx, "proxies", []string{"created_by_user_id TEXT NOT NULL DEFAULT ''"})
}
