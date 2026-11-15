package app

import (
	"cc-manager/internal/errs"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	secretbox "cc-manager/internal/crypto"
	"cc-manager/internal/supplier"

	_ "modernc.org/sqlite"
)

type App struct {
	DB           *sql.DB
	cfg          Config
	keys         *secretbox.Keyring
	sessions     *sessionStore
	logins       *loginLimiter
	writes       *writeLimiter
	oauth        oauthPendingStore
	billMutex    sync.Mutex
	billFactory  func(string, string) (billSupplier, error)
	billInterval time.Duration

	webMu      sync.Mutex
	webClients map[string]*supplier.WebClient
	webSyncMu  map[string]*sync.Mutex

	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closeOnce sync.Once
	started   time.Time
	ticks     struct{ jobs, metrics, bills, schedule, janitor, webAccounts, webBills atomic.Int64 }
	http      httpStats
}

// New loads configuration from the environment, opens the database at path
// (or CC_MANAGER_DB when empty) and starts the background workers.
func New(path string) (*App, error) { return newApp(path, true) }

func newApp(path string, workers bool) (*App, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	if path != "" {
		cfg.DBPath = path
	}
	return NewWithConfig(cfg, workers)
}

// baseSchema is the authoritative fresh-database layout. Every column is NOT
// NULL with a default so a NULL can never make a row vanish from a list.
const baseSchema = `
CREATE TABLE IF NOT EXISTS accounts(id TEXT PRIMARY KEY, account TEXT UNIQUE NOT NULL, plan TEXT NOT NULL DEFAULT '', register_location TEXT NOT NULL DEFAULT '', register_time TEXT NOT NULL DEFAULT '', plan_start_time TEXT NOT NULL DEFAULT '', plan_end_time TEXT NOT NULL DEFAULT '', proxy_id TEXT NOT NULL DEFAULT '', metadata_json TEXT NOT NULL DEFAULT '{}', credentials_json TEXT NOT NULL DEFAULT '{}', version INTEGER NOT NULL DEFAULT 1, archived INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL DEFAULT '', created_by_user_id TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS proxies(id TEXT PRIMARY KEY,name TEXT NOT NULL DEFAULT '',scheme TEXT NOT NULL DEFAULT '',host TEXT NOT NULL DEFAULT '',port INTEGER NOT NULL DEFAULT 0,username TEXT NOT NULL DEFAULT '',password_secret TEXT NOT NULL DEFAULT '',enabled INTEGER NOT NULL DEFAULT 1,version INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL DEFAULT '',updated_at TEXT NOT NULL DEFAULT '',created_by_user_id TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS platform_connections(id TEXT PRIMARY KEY,name TEXT NOT NULL DEFAULT '',base_url TEXT NOT NULL DEFAULT '',token_secret TEXT NOT NULL DEFAULT '',timezone TEXT NOT NULL DEFAULT '',allowed_plans_json TEXT NOT NULL DEFAULT '[]',enabled INTEGER NOT NULL DEFAULT 1,version INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL DEFAULT '',updated_at TEXT NOT NULL DEFAULT '',web_username TEXT NOT NULL DEFAULT '',web_password_secret TEXT NOT NULL DEFAULT '',web_suspended_at TEXT NOT NULL DEFAULT '',web_suspended_reason TEXT NOT NULL DEFAULT '',usd_cny_rate REAL NOT NULL DEFAULT 7.2);
CREATE TABLE IF NOT EXISTS jobs(id TEXT PRIMARY KEY,kind TEXT NOT NULL DEFAULT '',state TEXT NOT NULL DEFAULT '',account_id TEXT NOT NULL DEFAULT '',connection_id TEXT NOT NULL DEFAULT '',payload_json TEXT NOT NULL DEFAULT '{}',result_json TEXT NOT NULL DEFAULT '{}',created_at TEXT NOT NULL DEFAULT '',updated_at TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS users(id TEXT PRIMARY KEY,username TEXT UNIQUE NOT NULL,nickname TEXT NOT NULL DEFAULT '',password_hash TEXT NOT NULL,enabled INTEGER NOT NULL DEFAULT 1,version INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,last_login_at TEXT);
CREATE TABLE IF NOT EXISTS roles(id TEXT PRIMARY KEY,name TEXT UNIQUE NOT NULL,description TEXT NOT NULL DEFAULT '',builtin INTEGER NOT NULL DEFAULT 0,permissions_json TEXT NOT NULL DEFAULT '[]',version INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS user_roles(user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,role_id TEXT NOT NULL REFERENCES roles(id),PRIMARY KEY(user_id,role_id));
CREATE TABLE IF NOT EXISTS audit_events(id INTEGER PRIMARY KEY AUTOINCREMENT,action TEXT NOT NULL DEFAULT '',resource_type TEXT NOT NULL DEFAULT '',resource_id TEXT NOT NULL DEFAULT '',actor TEXT NOT NULL DEFAULT '',detail TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS account_secrets(account_id TEXT PRIMARY KEY REFERENCES accounts(id),password_secret TEXT NOT NULL DEFAULT '',credential_version INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS deliveries(id TEXT PRIMARY KEY,account_id TEXT NOT NULL,connection_id TEXT NOT NULL,upstream_id TEXT,ingestion_state TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(account_id,connection_id));
CREATE TABLE IF NOT EXISTS idempotency_keys(key TEXT PRIMARY KEY,payload_hash TEXT NOT NULL,job_id TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS proxy_checks(id TEXT PRIMARY KEY,proxy_id TEXT NOT NULL,ok INTEGER NOT NULL,latency_ms INTEGER,exit_ip TEXT,error_code TEXT,checked_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS imports(id TEXT PRIMARY KEY,format TEXT NOT NULL,content_hash TEXT NOT NULL,rows_json TEXT NOT NULL,state TEXT NOT NULL,created_at TEXT NOT NULL,expires_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS usage_metrics(id TEXT PRIMARY KEY,account_id TEXT NOT NULL,source TEXT NOT NULL,bucket_date TEXT NOT NULL,requests INTEGER NOT NULL,prompt_tokens INTEGER NOT NULL,completion_tokens INTEGER NOT NULL,success_rate REAL,cost_fen INTEGER NOT NULL DEFAULT 0,observed_at TEXT NOT NULL,UNIQUE(account_id,source,bucket_date));
CREATE TABLE IF NOT EXISTS platform_links(id TEXT PRIMARY KEY,account_id TEXT NOT NULL,connection_id TEXT NOT NULL,upstream_id TEXT NOT NULL,upstream_aid TEXT,alias TEXT,runtime_status TEXT,runtime_status_raw TEXT,last_observed_at TEXT,ingestion_state TEXT NOT NULL DEFAULT 'accepted',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(account_id,connection_id),UNIQUE(connection_id,upstream_id));
CREATE TABLE IF NOT EXISTS platform_metric_snapshots(id TEXT PRIMARY KEY,account_id TEXT NOT NULL,connection_id TEXT NOT NULL,upstream_id TEXT NOT NULL,upstream_aid TEXT,alias TEXT,runtime_status TEXT,runtime_status_raw TEXT,bucket_key TEXT NOT NULL DEFAULT '',window_start TEXT NOT NULL,window_end TEXT NOT NULL,observed_window_end TEXT,requests INTEGER,prompt_tokens INTEGER,completion_tokens INTEGER,success_rate REAL,fetched_at TEXT NOT NULL,freshness_state TEXT NOT NULL DEFAULT 'fresh',sync_generation INTEGER NOT NULL DEFAULT 0,UNIQUE(account_id,connection_id,bucket_key));
CREATE TABLE IF NOT EXISTS platform_bill_daily(id TEXT PRIMARY KEY,account_id TEXT NOT NULL,connection_id TEXT NOT NULL,upstream_id TEXT NOT NULL,bill_date TEXT NOT NULL,request_count INTEGER NOT NULL,prompt_tokens INTEGER NOT NULL,completion_tokens INTEGER NOT NULL,cache_read_input_tokens INTEGER NOT NULL,cache_write_input_tokens INTEGER NOT NULL,cost_fen INTEGER NOT NULL,source_range_json TEXT NOT NULL,fetched_at TEXT NOT NULL,revision INTEGER NOT NULL DEFAULT 1,finalized INTEGER NOT NULL DEFAULT 0,sync_generation INTEGER NOT NULL DEFAULT 0,requested_start_ds TEXT,requested_end_ds TEXT,actual_start_ds TEXT,actual_end_ds TEXT,source_timezone TEXT,response_verified INTEGER NOT NULL DEFAULT 0,write_generation INTEGER NOT NULL DEFAULT 0,last_changed_at TEXT,UNIQUE(account_id,connection_id,bill_date));
CREATE TABLE IF NOT EXISTS sync_runs(id TEXT PRIMARY KEY,connection_id TEXT NOT NULL,kind TEXT NOT NULL,state TEXT NOT NULL,started_at TEXT NOT NULL,finished_at TEXT,last_success_at TEXT,error_message TEXT,processed INTEGER NOT NULL DEFAULT 0,failed INTEGER NOT NULL DEFAULT 0,request_json TEXT NOT NULL DEFAULT '{}',fingerprint TEXT NOT NULL DEFAULT '',idem_key TEXT,parent_id TEXT,total_items INTEGER NOT NULL DEFAULT 0,created_at TEXT,updated_at TEXT,owner TEXT NOT NULL DEFAULT '',lease_until INTEGER NOT NULL DEFAULT 0,http_requests INTEGER NOT NULL DEFAULT 0,http_retries INTEGER NOT NULL DEFAULT 0);` + webChannelTables

// openDatabase applies the connection pragmas through the DSN so they hold
// for every connection the pool may open, not only the first one.
func openDatabase(path string) (*sql.DB, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)
	if err = db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// NewWithConfig builds the application from an explicit configuration.
func NewWithConfig(cfg Config, workers bool) (*App, error) {
	db, err := openDatabase(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if _, err = db.Exec(baseSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("database schema: %w", err)
	}
	if err = migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("database migration: %w", err)
	}
	var keys *secretbox.Keyring
	if cfg.EncryptionKey != nil {
		if keys, err = secretbox.NewKeyring(cfg.EncryptionKey, cfg.PreviousKeys...); err != nil {
			db.Close()
			return nil, err
		}
	}
	a := &App{DB: db, cfg: cfg, keys: keys, sessions: newSessionStore(), logins: newLoginLimiter(), writes: newWriteLimiter(cfg.WriteRatePerMinute), billInterval: cfg.BillInterval, started: time.Now()}
	if err = a.ensureBootstrapAdmin(); err != nil {
		db.Close()
		return nil, fmt.Errorf("bootstrap admin: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	if workers {
		a.startWorker(ctx)
		a.startJanitor(ctx)
	}
	return a, nil
}

// Close stops the workers, waits for in-flight supplier calls to finish,
// checkpoints the WAL and closes the database. It is safe to call twice.
func (a *App) Close() error {
	var err error
	a.closeOnce.Do(func() {
		if a.cancel != nil {
			a.cancel()
		}
		a.wg.Wait()
		if _, e := a.DB.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); e != nil {
			slog.Warn("wal checkpoint failed", "error", e)
		}
		err = a.DB.Close()
	})
	return err
}

func now() string                             { return time.Now().UTC().Format(time.RFC3339) }
func newID() string                           { return randomString() }
func str(m map[string]any, key string) string { v, _ := m[key].(string); return strings.TrimSpace(v) }
func intValue(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}

// webPasswordOf reads the console password from a request body without
// trimming it: passwords may legitimately start or end with spaces.
func webPasswordOf(m map[string]any) string { v, _ := m["web_password"].(string); return v }

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// expectedVersion returns the optimistic-lock version supplied by the client
// (0 when absent) so PATCH-style endpoints can reject stale updates.
func expectedVersion(in map[string]any) int {
	return intValue(in["expected_version"])
}

func (a *App) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	body := map[string]any{"ok": true, "version": a.cfg.Version, "uptime_seconds": int(time.Since(a.started).Seconds()), "sessions": a.sessions.len()}
	status := http.StatusOK
	if err := a.DB.PingContext(ctx); err != nil {
		body["ok"] = false
		body["database"] = "unavailable"
		status = http.StatusServiceUnavailable
	} else {
		body["database"] = "ok"
	}
	tick := func(v *atomic.Int64) any {
		if n := v.Load(); n > 0 {
			return time.Unix(n, 0).UTC().Format(time.RFC3339)
		}
		return nil
	}
	body["workers"] = map[string]any{"jobs_last_tick": tick(&a.ticks.jobs), "metrics_last_tick": tick(&a.ticks.metrics), "bills_last_tick": tick(&a.ticks.bills), "schedule_last_tick": tick(&a.ticks.schedule), "janitor_last_tick": tick(&a.ticks.janitor), "web_accounts_last_tick": tick(&a.ticks.webAccounts), "web_bills_last_tick": tick(&a.ticks.webBills)}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (a *App) dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	scope := ownershipFrom(r)
	accountScope, accountArgs := scope.sqlAnd("created_by_user_id")
	proxyScope, proxyArgs := scope.sqlAnd("created_by_user_id")
	jobScope, jobArgs := scope.jobsOwnershipSQL("account_id")
	linkScope, linkArgs := "", []any(nil)
	snapScope, snapArgs := "", []any(nil)
	billScope, billArgs := "", []any(nil)
	if !scope.Admin {
		linkScope = ` AND account_id IN (SELECT id FROM accounts WHERE created_by_user_id=?)`
		linkArgs = []any{scope.UserID}
		snapScope = linkScope
		snapArgs = linkArgs
		billScope = ` AND account_id IN (SELECT id FROM accounts WHERE created_by_user_id=?)`
		billArgs = []any{scope.UserID}
	}
	var counts struct {
		accounts, archived, ready, linked, observed, proxies, disabledProxies, jobsRunning, jobsQueued, jobsSucceeded, jobsFailed int
		billRequests, billCost                                                                                                    int64
	}
	recent := time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339)
	today := time.Now().UTC().Format("2006-01-02")
	args := []any{}
	args = append(args, accountArgs...)
	args = append(args, accountArgs...)
	args = append(args, accountArgs...)
	args = append(args, linkArgs...)
	args = append(args, recent)
	args = append(args, snapArgs...)
	args = append(args, proxyArgs...)
	args = append(args, proxyArgs...)
	args = append(args, jobArgs...)
	args = append(args, jobArgs...)
	args = append(args, jobArgs...)
	args = append(args, jobArgs...)
	args = append(args, today)
	args = append(args, billArgs...)
	args = append(args, today)
	args = append(args, billArgs...)
	err := a.DB.QueryRowContext(ctx, `SELECT
(SELECT COUNT(*) FROM accounts WHERE archived=0`+accountScope+`),
(SELECT COUNT(*) FROM accounts WHERE archived=1`+accountScope+`),
(SELECT COUNT(*) FROM accounts a WHERE a.archived=0 AND a.proxy_id<>'' AND EXISTS(SELECT 1 FROM account_secrets s WHERE s.account_id=a.id AND s.password_secret<>'') AND a.credentials_json NOT IN ('','{}')`+strings.Replace(accountScope, "created_by_user_id", "a.created_by_user_id", 1)+`),
(SELECT COUNT(*) FROM platform_links WHERE ingestion_state='accepted'`+linkScope+`),
(SELECT COUNT(DISTINCT account_id) FROM platform_metric_snapshots WHERE fetched_at>=?`+snapScope+`),
(SELECT COUNT(*) FROM proxies WHERE enabled=1`+proxyScope+`),
(SELECT COUNT(*) FROM proxies WHERE enabled=0`+proxyScope+`),
(SELECT COUNT(*) FROM jobs WHERE state='running'`+jobScope+`),
(SELECT COUNT(*) FROM jobs WHERE state='queued'`+jobScope+`),
(SELECT COUNT(*) FROM jobs WHERE state='succeeded'`+jobScope+`),
(SELECT COUNT(*) FROM jobs WHERE state='failed'`+jobScope+`),
(SELECT COALESCE(SUM(request_count),0) FROM platform_bill_daily WHERE bill_date=?`+billScope+`),
(SELECT COALESCE(SUM(cost_fen),0) FROM platform_bill_daily WHERE bill_date=?`+billScope+`)`, args...).Scan(
		&counts.accounts, &counts.archived, &counts.ready, &counts.linked, &counts.observed, &counts.proxies, &counts.disabledProxies, &counts.jobsRunning, &counts.jobsQueued, &counts.jobsSucceeded, &counts.jobsFailed, &counts.billRequests, &counts.billCost)
	if err != nil {
		writeError(w, r, storageError(err, "读取概览失败"))
		return
	}
	plansByCount := map[string]int{}
	planQuery := `SELECT CASE WHEN plan='' THEN '未设置' ELSE plan END,COUNT(*) FROM accounts WHERE archived=0` + accountScope + ` GROUP BY plan ORDER BY COUNT(*) DESC`
	rows, err := a.DB.QueryContext(ctx, planQuery, accountArgs...)
	if err != nil {
		writeError(w, r, storageError(err, "读取概览失败"))
		return
	}
	for rows.Next() {
		var plan string
		var count int
		if err = rows.Scan(&plan, &count); err != nil {
			break
		}
		plansByCount[plan] = count
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		writeError(w, r, storageError(err, "读取概览失败"))
		return
	}
	syncHealth, err := a.metricsSyncHealth(ctx)
	if err != nil {
		writeError(w, r, storageError(err, "读取概览失败"))
		return
	}
	webHealth, err := a.webDashboard(ctx, scope)
	if err != nil {
		writeError(w, r, storageError(err, "读取概览失败"))
		return
	}
	write(w, http.StatusOK, map[string]any{
		"accounts": map[string]any{"total": counts.accounts, "ready": counts.ready, "archived": counts.archived, "platform_linked": counts.linked, "observed_recently": counts.observed, "by_plan": plansByCount},
		"jobs":     map[string]any{"running": counts.jobsRunning, "queued": counts.jobsQueued, "succeeded": counts.jobsSucceeded, "failed": counts.jobsFailed},
		"proxies":  map[string]any{"healthy": counts.proxies, "enabled": counts.proxies, "disabled": counts.disabledProxies},
		"platform": mergeMaps(mergeMaps(map[string]any{"today_requests": counts.billRequests, "today_cost_fen": counts.billCost}, syncHealth), webHealth),
	})
}

// accounts serves GET (list) and POST (create draft).
func (a *App) accounts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.listAccounts(w, r)
	case http.MethodPost:
		a.createAccount(w, r)
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

// Latest observation per account comes from one correlated lookup on the
// newest snapshot row instead of nine separate scans per account. The
// onboarding state is derived in SQL with the same rules as
// decorateAccountDelivery so the list can filter on it server-side.
const accountListBase = `WITH latest AS (
	SELECT m.* FROM platform_metric_snapshots m
	WHERE m.rowid=(SELECT rowid FROM platform_metric_snapshots x WHERE x.account_id=m.account_id ORDER BY x.fetched_at DESC,x.rowid DESC LIMIT 1)
), link AS (
	SELECT l.* FROM platform_links l
	WHERE l.rowid=(SELECT rowid FROM platform_links x WHERE x.account_id=l.account_id ORDER BY x.updated_at DESC,x.rowid DESC LIMIT 1)
), bill AS (
	SELECT b.account_id,b.cost_fen FROM platform_bill_daily b
	WHERE b.rowid=(SELECT rowid FROM platform_bill_daily x WHERE x.account_id=b.account_id ORDER BY x.bill_date DESC,x.rowid DESC LIMIT 1)
), remote AS (
	SELECT r.connection_id,r.upstream_id,r.five_hour_used,r.last_seen_at FROM platform_remote_accounts r
)
SELECT a.id,a.account,a.plan,a.register_location,a.register_time,a.plan_start_time,a.plan_end_time,a.proxy_id,a.version,a.archived,a.created_at,a.updated_at,a.rowid AS rid,
COALESCE(NULLIF(m.upstream_aid,''),m.upstream_id,NULLIF(l.upstream_aid,''),l.upstream_id,'') AS platform_account_id,
COALESCE(m.alias,l.alias,'') AS platform_alias,
CASE WHEN COALESCE(l.last_observed_at,'')>=COALESCE(m.fetched_at,'') THEN COALESCE(l.runtime_status,m.runtime_status,'') ELSE COALESCE(m.runtime_status,l.runtime_status,'') END AS platform_status,
CASE WHEN COALESCE(l.last_observed_at,'')>=COALESCE(m.fetched_at,'') THEN COALESCE(l.last_observed_at,m.fetched_at,'') ELSE COALESCE(m.fetched_at,l.last_observed_at,'') END AS platform_observed_at,
m.requests,m.prompt_tokens,m.completion_tokens,m.success_rate,bill.cost_fen,
CASE
	WHEN EXISTS(SELECT 1 FROM platform_links pl WHERE pl.account_id=a.id AND pl.ingestion_state='accepted' AND COALESCE(pl.upstream_id,'')<>'') THEN 'linked'
	WHEN EXISTS(SELECT 1 FROM deliveries d WHERE d.account_id=a.id AND d.ingestion_state IN ('unknown','accepted')) THEN 'unknown'
	ELSE COALESCE((SELECT CASE j.state WHEN 'queued' THEN 'submitting' WHEN 'running' THEN 'submitting' WHEN 'failed' THEN 'failed' WHEN 'unknown' THEN 'unknown' WHEN 'succeeded' THEN 'unknown' ELSE 'pending' END
		FROM jobs j WHERE j.kind='account.submit' AND j.account_id=a.id ORDER BY j.created_at DESC,j.rowid DESC LIMIT 1),'pending')
END AS onboarding_state,
rem.five_hour_used AS five_hour_used,
CASE WHEN rem.upstream_id IS NULL THEN '' WHEN COALESCE(rem.last_seen_at,'')>=COALESCE(m.fetched_at,'') THEN 'web' ELSE 'openapi' END AS platform_channel,
COALESCE(a.created_by_user_id,'') AS created_by_user_id,
COALESCE(u.nickname,'') AS created_by_nickname,
COALESCE(u.username,'') AS created_by_username
FROM accounts a LEFT JOIN latest m ON m.account_id=a.id LEFT JOIN link l ON l.account_id=a.id LEFT JOIN bill ON bill.account_id=a.id LEFT JOIN remote rem ON rem.connection_id=l.connection_id AND rem.upstream_id=l.upstream_id LEFT JOIN users u ON u.id=a.created_by_user_id`

// unpagedAccountLimit caps the legacy array response; the UI always pages.
const unpagedAccountLimit = 2000

var accountOnboardingStates = map[string]bool{"pending": true, "submitting": true, "linked": true, "failed": true, "unknown": true}

// accountListFilter maps the list query parameters onto the derived columns
// of accountListBase. Keys match the frontend filter keys one to one.
func accountListFilter(w http.ResponseWriter, q url.Values) (listFilter, bool) {
	var f listFilter
	if v := strings.TrimSpace(q.Get("onboarding_state")); v != "" {
		if !accountOnboardingStates[v] {
			writeError(w, nil, queryInvalid("onboarding_state", "上号状态筛选值无效"))
			return f, false
		}
		f.equals("t.onboarding_state", v)
	}
	f.equals("t.plan", q.Get("plan"))
	switch v := strings.TrimSpace(q.Get("archived")); v {
	case "":
	case "true":
		f.where = append(f.where, "t.archived=1")
	case "false":
		f.where = append(f.where, "t.archived=0")
	default:
		writeError(w, nil, queryInvalid("archived", "库存状态筛选值无效"))
		return f, false
	}
	switch v := strings.TrimSpace(q.Get("proxy")); v {
	case "":
	case "present":
		f.where = append(f.where, "t.proxy_id<>''")
	case "absent":
		f.where = append(f.where, "t.proxy_id=''")
	default:
		writeError(w, nil, queryInvalid("proxy", "代理绑定筛选值无效"))
		return f, false
	}
	f.contains("t.platform_status", q.Get("platform_status"))
	if v := strings.TrimSpace(q.Get("five_hour_min")); v != "" {
		n, err := strconv.ParseFloat(v, 64)
		if err != nil || n < 0 || n > 100 {
			writeError(w, nil, queryInvalid("five_hour_min", "5 小时窗口筛选值必须是 0 到 100 的数字"))
			return f, false
		}
		f.where = append(f.where, "t.five_hour_used>=?")
		f.args = append(f.args, n/100)
	}
	f.dateRange("t.plan_end_time", q.Get("end_from"), q.Get("end_to"))
	f.dateRange("t.created_at", q.Get("created_from"), q.Get("created_to"))
	f.keyword(q.Get("q"), "t.account", "t.id", "t.plan", "t.register_location", "t.proxy_id", "t.platform_account_id", "t.platform_alias", "t.platform_status")
	return f, true
}

func (a *App) listAccounts(w http.ResponseWriter, r *http.Request) {
	page, size, paged, ok := pageParams(w, r)
	if !ok {
		return
	}
	f, ok := accountListFilter(w, r.URL.Query())
	if !ok {
		return
	}
	ownershipFrom(r).filterCreatedBy(&f, "t.created_by_user_id")
	ctx := r.Context()
	total := 0
	limit, offset := unpagedAccountLimit, 0
	if paged {
		if err := a.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+accountListBase+`) t`+f.sql(), f.args...).Scan(&total); err != nil {
			writeError(w, r, storageError(err, "账号读取失败"))
			return
		}
		limit, offset = size, (page-1)*size
	}
	// Default: platform ID high→low (numeric). Empty IDs sink to the end so
	// paging stays stable across pages for the same sort.
	order := `(t.platform_account_id='') ASC, CAST(t.platform_account_id AS INTEGER) DESC, t.created_at DESC, t.rid DESC`
	switch strings.TrimSpace(r.URL.Query().Get("sort")) {
	case "", "platform_id_desc":
	case "created_desc":
		order = `t.created_at DESC,t.rid DESC`
	case "five_hour_desc":
		order = `t.five_hour_used DESC NULLS LAST,t.created_at DESC,t.rid DESC`
	default:
		writeError(w, r, queryInvalid("sort", "排序参数无效"))
		return
	}
	rows, err := a.DB.QueryContext(ctx, `SELECT * FROM (`+accountListBase+`) t`+f.sql()+` ORDER BY `+order+` LIMIT ? OFFSET ?`, append(append([]any{}, f.args...), limit, offset)...)
	if err != nil {
		writeError(w, r, storageError(err, "账号读取失败"))
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, account, plan, loc, rt, ps, pe, proxy, created, updated string
		var upstreamID, alias, platformStatus, observed, onboarding string
		var version, archived int
		var rid int64
		var requests, prompt, completion, cost sql.NullInt64
		var success, fiveHour sql.NullFloat64
		var channel, createdByID, createdByNickname, createdByUsername string
		if err = rows.Scan(&id, &account, &plan, &loc, &rt, &ps, &pe, &proxy, &version, &archived, &created, &updated, &rid, &upstreamID, &alias, &platformStatus, &observed, &requests, &prompt, &completion, &success, &cost, &onboarding, &fiveHour, &channel, &createdByID, &createdByNickname, &createdByUsername); err != nil {
			writeError(w, r, storageError(err, "账号读取失败"))
			return
		}
		m := map[string]any{"id": id, "account": account, "plan": plan, "register_location": loc, "register_time": rt, "plan_start_time": ps, "plan_end_time": pe, "proxy_id": proxy, "version": version, "archived": archived == 1, "created_at": created, "updated_at": updated, "platform_account_id": upstreamID, "platform_alias": alias, "platform_status": platformStatus, "platform_observed_at": observed, "onboarding_state": onboarding, "created_by_user_id": createdByID, "created_by_nickname": createdByNickname, "created_by_username": createdByUsername}
		if requests.Valid {
			m["platform_requests"] = requests.Int64
		}
		if prompt.Valid {
			m["platform_prompt_tokens"] = prompt.Int64
		}
		if completion.Valid {
			m["platform_completion_tokens"] = completion.Int64
		}
		if success.Valid {
			m["platform_success_rate_percent"] = success.Float64
		}
		if cost.Valid {
			m["platform_cost_fen"] = cost.Int64
		}
		m["platform_channel"] = channel
		out = append(out, m)
	}
	if err = rows.Err(); err != nil {
		writeError(w, r, storageError(err, "账号读取失败"))
		return
	}
	rows.Close()
	if err = a.decorateAccountDelivery(out); err != nil {
		writeError(w, r, storageError(err, "上号状态读取失败"))
		return
	}
	if err = a.attachPlatformBlocks(ctx, out); err != nil {
		writeError(w, r, storageError(err, "平台状态读取失败"))
		return
	}
	if paged {
		write(w, http.StatusOK, pagedEnvelope(out, page, size, total))
		return
	}
	write(w, http.StatusOK, out)
}

func (a *App) createAccount(w http.ResponseWriter, r *http.Request) {
	var in map[string]any
	if !decodeJSON(w, r, &in, defaultBodyLimit) {
		return
	}
	account, plan := str(in, "account"), str(in, "plan")
	if e := (&errs.Fields{}); true {
		for _, p := range validateAccountFields(in, false) {
			e.Add(p.Field, p.Code, p.Message)
		}
		if err := e.Err(); err != nil {
			writeError(w, r, err)
			return
		}
	}
	password := str(in, "password")
	if password != "" && a.keys == nil {
		fail(w, http.StatusServiceUnavailable, "ENCRYPTION_KEY_REQUIRED", "未配置凭证加密密钥")
		return
	}
	if proxyID := str(in, "proxy_id"); proxyID != "" {
		if _, err := a.resolveProxyURL(proxyID, ""); err != nil {
			fail(w, http.StatusUnprocessableEntity, "PROXY_INVALID", err.Error())
			return
		}
		if !a.requireProxyBindable(w, r, proxyID) {
			return
		}
	}
	id, t := newID(), now()
	metadataJSON, _ := json.Marshal(pickMetadata(in))
	var sealedPassword string
	if password != "" {
		var err error
		if sealedPassword, err = a.keys.Seal([]byte(password), []byte(id)); err != nil {
			writeError(w, r, secretError("account_password", "账号密码加密失败", err))
			return
		}
	}
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, r, storageError(err, "账号保存失败"))
		return
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO accounts(id,account,plan,register_location,register_time,plan_start_time,plan_end_time,proxy_id,metadata_json,credentials_json,created_at,updated_at,created_by_user_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, account, plan, str(in, "register_location"), str(in, "register_time"), str(in, "plan_start_time"), str(in, "plan_end_time"), str(in, "proxy_id"), string(metadataJSON), `{}`, t, t, sessionUser(r).ID); err != nil {
		dbError(w, r, err, "ACCOUNT_EXISTS", "账号已存在", "账号保存失败")
		return
	}
	if sealedPassword != "" {
		if _, err = tx.Exec(`INSERT INTO account_secrets(account_id,password_secret) VALUES(?,?)`, id, sealedPassword); err != nil {
			writeError(w, r, storageError(err, "账号密码保存失败"))
			return
		}
	}
	if err = tx.Commit(); err != nil {
		writeError(w, r, storageError(err, "账号保存失败"))
		return
	}
	a.auditFor(r, "account.create", "account", id, "")
	write(w, http.StatusCreated, map[string]any{"id": id, "account": account, "version": 1})
}

func (a *App) accountGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := a.requireVisible(w, r, "accounts", id, "账号不存在"); !ok {
		return
	}
	var account, plan, credentials string
	var version int
	if err := a.DB.QueryRowContext(r.Context(), `SELECT account,plan,credentials_json,version FROM accounts WHERE id=?`, id).Scan(&account, &plan, &credentials, &version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusNotFound, "NOT_FOUND", "账号不存在")
			return
		}
		writeError(w, r, storageError(err, "账号读取失败"))
		return
	}
	detail := map[string]any{"id": id, "account": account, "plan": plan, "version": version, "credentials_configured": credentials != "{}" && credentials != ""}
	if err := a.attachPlatformBlocks(r.Context(), []any{detail}); err != nil {
		writeError(w, r, storageError(err, "平台状态读取失败"))
		return
	}
	write(w, http.StatusOK, detail)
}

func (a *App) accountPatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !a.requireMutable(w, r, "accounts", id, "账号不存在") {
		return
	}
	var in map[string]any
	if !decodeJSON(w, r, &in, defaultBodyLimit) {
		return
	}
	sets := []string{}
	args := []any{}
	var problems errs.Fields
	for _, key := range []string{"plan", "register_location", "register_time", "plan_start_time", "plan_end_time", "proxy_id"} {
		raw, ok := in[key]
		if !ok {
			continue
		}
		value, isString := raw.(string)
		if !isString {
			problems.Invalid(key, errs.Label(key)+"必须是文本")
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "plan":
			if !validPlan(value) {
				problems.Add("plan", errs.FieldUnsupportedValue, "套餐不在允许范围内")
			}
		case "register_location":
			if value != "" && value != supportedRegisterLocation {
				problems.Add("register_location", errs.FieldUnsupportedValue, "注册地区当前仅支持美国（US）")
			}
		case "register_time", "plan_start_time", "plan_end_time":
			if value != "" && !validDate(value) {
				problems.Invalid(key, errs.Label(key)+"必须是 YYYY-MM-DD 格式的日期")
			}
		case "proxy_id":
			if value != "" {
				if _, err := a.resolveProxyURL(value, ""); err != nil {
					problems.Add("proxy_id", errs.FieldUnsupportedValue, err.Error())
				}
			}
		}
		sets = append(sets, key+"=?")
		args = append(args, value)
	}
	if err := problems.Err(); err != nil {
		writeError(w, r, err)
		return
	}
	if raw, ok := in["proxy_id"]; ok {
		if proxyID, _ := raw.(string); !a.requireProxyBindable(w, r, strings.TrimSpace(proxyID)) {
			return
		}
	}
	if len(sets) == 0 {
		fail(w, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "没有可更新字段")
		return
	}
	expected := expectedVersion(in)
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, r, storageError(err, "账号更新失败"))
		return
	}
	defer tx.Rollback()
	var current struct {
		registered, start, end string
		version                int
	}
	if err = tx.QueryRow(`SELECT register_time,plan_start_time,plan_end_time,version FROM accounts WHERE id=?`, id).Scan(&current.registered, &current.start, &current.end, &current.version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusNotFound, "NOT_FOUND", "账号不存在")
			return
		}
		writeError(w, r, storageError(err, "账号更新失败"))
		return
	}
	if expected != 0 && expected != current.version {
		fail(w, http.StatusConflict, "VERSION_CONFLICT", "账号资料已更新，请刷新后重试")
		return
	}
	merged := map[string]string{"register_time": current.registered, "plan_start_time": current.start, "plan_end_time": current.end}
	for i, set := range sets {
		key := strings.TrimSuffix(set, "=?")
		if _, ok := merged[key]; ok {
			merged[key] = args[i].(string)
		}
	}
	if dates := accountDateFields(merged["register_time"], merged["plan_start_time"], merged["plan_end_time"], false); len(dates) > 0 {
		writeError(w, r, errs.New(errs.CodeValidationFailed).WithFields(dates...))
		return
	}
	sets = append(sets, "version=version+1", "updated_at=?")
	args = append(args, now(), id, current.version)
	if _, err = tx.Exec(`UPDATE accounts SET `+strings.Join(sets, ",")+` WHERE id=? AND version=?`, args...); err != nil {
		writeError(w, r, storageError(err, "账号更新失败"))
		return
	}
	if err = tx.Commit(); err != nil {
		writeError(w, r, storageError(err, "账号更新失败"))
		return
	}
	a.auditFor(r, "account.update", "account", id, "")
	write(w, http.StatusOK, map[string]any{"id": id, "updated": true, "version": current.version + 1})
}

func (a *App) accountArchive(archived bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !a.requireMutable(w, r, "accounts", id, "账号不存在") {
			return
		}
		res, err := a.DB.ExecContext(r.Context(), `UPDATE accounts SET archived=?,version=version+1,updated_at=? WHERE id=?`, boolInt(archived), now(), id)
		if err != nil {
			writeError(w, r, storageError(err, "账号状态更新失败"))
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			fail(w, http.StatusNotFound, "NOT_FOUND", "账号不存在")
			return
		}
		action := "account.restore"
		if archived {
			action = "account.archive"
		}
		a.auditFor(r, action, "account", id, "")
		write(w, http.StatusOK, map[string]any{"id": id, "archived": archived})
	}
}

// accountDelete removes an account the platform never accepted, together
// with its local secrets, jobs and delivery records. Anything the platform may
// hold (an accepted link, an unresolved delivery, a queued or running
// submission) blocks deletion so inventory on the platform is never orphaned.
func (a *App) accountDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !a.requireMutable(w, r, "accounts", id, "账号不存在") {
		return
	}
	ctx := r.Context()
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		writeError(w, r, storageError(err, "账号删除失败"))
		return
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM accounts WHERE id=?`, id).Scan(&exists); err != nil {
		writeError(w, r, storageError(err, "账号删除失败"))
		return
	}
	if exists == 0 {
		fail(w, http.StatusNotFound, "NOT_FOUND", "账号不存在")
		return
	}
	var delivered, inProgress int
	if err = tx.QueryRow(`SELECT
		(SELECT COUNT(*) FROM platform_links WHERE account_id=? AND ingestion_state='accepted')
		+(SELECT COUNT(*) FROM deliveries WHERE account_id=? AND ingestion_state IN ('accepted','unknown')),
		(SELECT COUNT(*) FROM jobs WHERE account_id=? AND kind='account.submit' AND state IN ('queued','running'))`,
		id, id, id).Scan(&delivered, &inProgress); err != nil {
		writeError(w, r, storageError(err, "账号删除失败"))
		return
	}
	if delivered > 0 {
		fail(w, http.StatusConflict, "ACCOUNT_DELIVERED", "平台已接收或可能已接收该账号，请改用归档")
		return
	}
	if inProgress > 0 {
		fail(w, http.StatusConflict, "ACCOUNT_IN_PROGRESS", "账号正在上号中，请等待结果后再删除")
		return
	}
	for _, stmt := range []string{
		`DELETE FROM platform_metric_snapshots WHERE account_id=?`,
		`DELETE FROM platform_bill_daily WHERE account_id=?`,
		`DELETE FROM usage_metrics WHERE account_id=?`,
		`DELETE FROM bill_sync_items WHERE account_id=?`,
		`DELETE FROM bill_sync_buckets WHERE account_id=?`,
		`DELETE FROM platform_links WHERE account_id=?`,
		`DELETE FROM deliveries WHERE account_id=?`,
		`DELETE FROM jobs WHERE account_id=?`,
		`DELETE FROM account_secrets WHERE account_id=?`,
		`DELETE FROM accounts WHERE id=?`,
	} {
		if _, err = tx.Exec(stmt, id); err != nil {
			writeError(w, r, storageError(err, "账号删除失败"))
			return
		}
	}
	if err = tx.Commit(); err != nil {
		writeError(w, r, storageError(err, "账号删除失败"))
		return
	}
	a.auditFor(r, "account.delete", "account", id, "")
	write(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}

func (a *App) accountCredentials(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !a.requireMutable(w, r, "accounts", id, "账号不存在") {
		return
	}
	if a.keys == nil {
		fail(w, http.StatusServiceUnavailable, "ENCRYPTION_KEY_REQUIRED", "未配置凭证加密密钥")
		return
	}
	var in map[string]any
	if !decodeJSON(w, r, &in, defaultBodyLimit) {
		return
	}
	auth, err := decodeAuthInput(in["auth_json"])
	if err != nil {
		writeError(w, r, errs.New(errs.CodeValidationFailed).WithFields(errs.FieldError{Field: "auth_json", Code: errs.FieldInvalidFormat, Message: err.Error()}))
		return
	}
	if problems := validateAuthFields(auth); len(problems) > 0 {
		writeError(w, r, errs.New(errs.CodeValidationFailed).WithFields(problems...))
		return
	}
	b, _ := json.Marshal(auth)
	sealed, err := a.keys.Seal(b, []byte(id))
	if err != nil {
		writeError(w, r, secretError("account_credentials", "凭证加密失败", err))
		return
	}
	expected := expectedVersion(in)
	query := `UPDATE accounts SET credentials_json=?,version=version+1,updated_at=? WHERE id=?`
	args := []any{sealed, now(), id}
	if expected != 0 {
		query += ` AND version=?`
		args = append(args, expected)
	}
	result, err := a.DB.ExecContext(r.Context(), query, args...)
	if err != nil {
		writeError(w, r, storageError(err, "凭证保存失败"))
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		var exists int
		if a.DB.QueryRow(`SELECT 1 FROM accounts WHERE id=?`, id).Scan(&exists) == nil {
			fail(w, http.StatusConflict, "VERSION_CONFLICT", "账号资料已更新，请刷新后重试")
			return
		}
		fail(w, http.StatusNotFound, "NOT_FOUND", "账号不存在")
		return
	}
	a.auditFor(r, "account.credentials.update", "account", id, "")
	write(w, http.StatusOK, map[string]any{"configured": true})
}

func (a *App) accountPreflight(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := a.requireVisible(w, r, "accounts", id, "账号不存在"); !ok {
		return
	}
	var req struct {
		PlatformConnectionID string `json:"platform_connection_id"`
	}
	if !decodeOptionalJSON(w, r, &req, 16<<10) {
		return
	}
	write(w, http.StatusOK, a.preflight(id, req.PlatformConnectionID))
}

func (a *App) accountPlatformUsage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := a.requireVisible(w, r, "accounts", id, "账号不存在"); !ok {
		return
	}
	out := map[string]any{"account_id": id, "metrics": []any{}, "bills": []any{}}
	metrics, err := a.DB.QueryContext(r.Context(), `SELECT upstream_id,COALESCE(alias,''),COALESCE(runtime_status,''),window_start,window_end,requests,prompt_tokens,completion_tokens,success_rate,fetched_at,freshness_state FROM platform_metric_snapshots WHERE account_id=? ORDER BY fetched_at DESC LIMIT 30`, id)
	if err != nil {
		writeError(w, r, storageError(err, "读取平台指标失败"))
		return
	}
	list := []any{}
	for metrics.Next() {
		var upstream, alias, status, start, end, fetched, fresh string
		var req, prompt, completion sql.NullInt64
		var rate sql.NullFloat64
		if err = metrics.Scan(&upstream, &alias, &status, &start, &end, &req, &prompt, &completion, &rate, &fetched, &fresh); err != nil {
			break
		}
		m := map[string]any{"platform_account_id": upstream, "alias": alias, "status": status, "window_start": start, "window_end": end, "fetched_at": fetched, "freshness_state": fresh}
		if req.Valid {
			m["requests"] = req.Int64
		}
		if prompt.Valid {
			m["prompt_tokens"] = prompt.Int64
		}
		if completion.Valid {
			m["completion_tokens"] = completion.Int64
		}
		if rate.Valid {
			m["success_rate_percent"] = rate.Float64
		}
		list = append(list, m)
	}
	if err == nil {
		err = metrics.Err()
	}
	metrics.Close()
	if err != nil {
		writeError(w, r, storageError(err, "读取平台指标失败"))
		return
	}
	out["metrics"] = list
	bills, err := a.DB.QueryContext(r.Context(), `SELECT upstream_id,bill_date,request_count,prompt_tokens,completion_tokens,cache_read_input_tokens,cache_write_input_tokens,cost_fen,fetched_at,revision,finalized FROM platform_bill_daily WHERE account_id=? ORDER BY bill_date DESC LIMIT 90`, id)
	if err != nil {
		writeError(w, r, storageError(err, "读取平台账单失败"))
		return
	}
	defer bills.Close()
	list = []any{}
	for bills.Next() {
		var upstream, date, fetched string
		var req, prompt, completion, read, writeN, cost, rev int64
		var final int
		if err = bills.Scan(&upstream, &date, &req, &prompt, &completion, &read, &writeN, &cost, &fetched, &rev, &final); err != nil {
			writeError(w, r, storageError(err, "读取平台账单失败"))
			return
		}
		list = append(list, map[string]any{"platform_account_id": upstream, "bill_date": date, "request_count": req, "prompt_tokens": prompt, "completion_tokens": completion, "cache_read_input_tokens": read, "cache_write_input_tokens": writeN, "cost_fen": cost, "fetched_at": fetched, "revision": rev, "finalized": final == 1})
	}
	if err = bills.Err(); err != nil {
		writeError(w, r, storageError(err, "读取平台账单失败"))
		return
	}
	out["bills"] = list
	write(w, http.StatusOK, out)
}

func (a *App) proxies(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.listProxies(w, r)
	case http.MethodPost:
		a.createProxy(w, r)
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (a *App) listProxies(w http.ResponseWriter, r *http.Request) {
	scope := ownershipFrom(r)
	query := `SELECT p.id,p.name,p.scheme,p.host,p.port,p.enabled,p.version,p.username,p.password_secret<>'',c.ok,c.latency_ms,COALESCE(c.exit_ip,''),COALESCE(c.error_code,''),COALESCE(c.checked_at,''),COALESCE(d.route_mode,'unknown'),COALESCE(d.message,''),COALESCE(p.created_by_user_id,'') FROM proxies p LEFT JOIN proxy_checks c ON c.id=(SELECT id FROM proxy_checks WHERE proxy_id=p.id ORDER BY checked_at DESC,rowid DESC LIMIT 1) LEFT JOIN proxy_check_details d ON d.check_id=c.id`
	args := []any{}
	if !scope.Admin {
		query += ` WHERE p.created_by_user_id=?`
		args = append(args, scope.UserID)
	}
	query += ` ORDER BY p.created_at DESC,p.rowid DESC`
	rows, err := a.DB.QueryContext(r.Context(), query, args...)
	if err != nil {
		writeError(w, r, storageError(err, "代理读取失败"))
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, name, scheme, host, username, createdBy string
		var port, enabled, version int
		var hasPassword bool
		var ok, latency sql.NullInt64
		var exitIP, code, checkedAt, route, message string
		if err = rows.Scan(&id, &name, &scheme, &host, &port, &enabled, &version, &username, &hasPassword, &ok, &latency, &exitIP, &code, &checkedAt, &route, &message, &createdBy); err != nil {
			writeError(w, r, storageError(err, "代理读取失败"))
			return
		}
		var latest any
		if ok.Valid {
			if message == "" && code != "" {
				if def, found := errs.Lookup(code); found {
					message = def.Message
				}
			}
			latest = map[string]any{"ok": ok.Int64 == 1, "latency_ms": latency.Int64, "exit_ip": exitIP, "error_code": code, "checked_at": checkedAt, "route_mode": route, "message": message}
		}
		out = append(out, map[string]any{"id": id, "name": name, "scheme": scheme, "host": host, "port": port, "enabled": enabled == 1, "version": version, "username": username, "password_configured": hasPassword, "latest_check": latest, "created_by_user_id": createdBy})
	}
	if err = rows.Err(); err != nil {
		writeError(w, r, storageError(err, "代理读取失败"))
		return
	}
	write(w, http.StatusOK, out)
}

func (a *App) createProxy(w http.ResponseWriter, r *http.Request) {
	var in map[string]any
	if !decodeJSON(w, r, &in, defaultBodyLimit) {
		return
	}
	scheme := strings.ToLower(str(in, "scheme"))
	port := intValue(in["port"])
	host := str(in, "host")
	if e := proxyFieldErrors(scheme, host, port); e != nil {
		writeError(w, r, e)
		return
	}
	id, t := newID(), now()
	username, password := str(in, "username"), str(in, "password")
	var secret string
	if password != "" {
		if a.keys == nil {
			fail(w, http.StatusServiceUnavailable, "ENCRYPTION_KEY_REQUIRED", "未配置凭证加密密钥")
			return
		}
		var err error
		if secret, err = a.keys.Seal([]byte(password), []byte(id)); err != nil {
			writeError(w, r, secretError("proxy_password", "代理密码加密失败", err))
			return
		}
	}
	if _, err := a.DB.ExecContext(r.Context(), `INSERT INTO proxies(id,name,scheme,host,port,username,password_secret,created_at,updated_at,created_by_user_id) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, str(in, "name"), scheme, host, port, username, secret, t, t, sessionUser(r).ID); err != nil {
		dbError(w, r, err, "PROXY_EXISTS", "代理已存在", "代理保存失败")
		return
	}
	a.auditFor(r, "proxy.create", "proxy", id, scheme+"://"+host)
	write(w, http.StatusCreated, map[string]any{"id": id, "version": 1})
}

func (a *App) platformConnections(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.listPlatformConnections(w, r)
	case http.MethodPost:
		a.createPlatformConnection(w, r)
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (a *App) listPlatformConnections(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.QueryContext(r.Context(), `SELECT id,name,base_url,timezone,enabled,version,token_secret,web_username,web_password_secret,web_suspended_at,web_suspended_reason,usd_cny_rate FROM platform_connections ORDER BY created_at,rowid`)
	if err != nil {
		writeError(w, r, storageError(err, "平台连接读取失败"))
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, name, base, timezone, token, webUser, webSecret, suspendedAt, suspendedReason string
		var enabled, version int
		var usdRate float64
		if err = rows.Scan(&id, &name, &base, &timezone, &enabled, &version, &token, &webUser, &webSecret, &suspendedAt, &suspendedReason, &usdRate); err != nil {
			writeError(w, r, storageError(err, "平台连接读取失败"))
			return
		}
		out = append(out, map[string]any{"id": id, "name": name, "base_url": base, "timezone": timezone, "enabled": enabled == 1, "version": version, "token_configured": token != "",
			"web_username": webUser, "web_configured": webUser != "" && webSecret != "", "web_suspended_at": suspendedAt, "web_suspended_reason": suspendedReason, "usd_cny_rate": usdRate})
	}
	if err = rows.Err(); err != nil {
		writeError(w, r, storageError(err, "平台连接读取失败"))
		return
	}
	write(w, http.StatusOK, out)
}

func validTimezone(zone string) bool {
	if zone == "" || zone == "Local" {
		return false
	}
	_, err := time.LoadLocation(zone)
	return err == nil
}

func (a *App) createPlatformConnection(w http.ResponseWriter, r *http.Request) {
	if a.keys == nil {
		fail(w, http.StatusServiceUnavailable, "ENCRYPTION_KEY_REQUIRED", "未配置凭证加密密钥")
		return
	}
	var in map[string]any
	if !decodeJSON(w, r, &in, defaultBodyLimit) {
		return
	}
	var problems errs.Fields
	token := str(in, "token")
	if token == "" {
		problems.Required("token")
	}
	base, err := url.Parse(str(in, "base_url"))
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil {
		problems.Invalid("base_url", "平台地址必须是 https:// 开头的完整地址")
	}
	timezone := str(in, "timezone")
	if timezone != "" && !validTimezone(timezone) {
		problems.Invalid("timezone", "时区必须是有效的 IANA 时区，例如 Asia/Shanghai")
	}
	webUser, webPassword := str(in, "web_username"), webPasswordOf(in)
	if (webUser == "") != (webPassword == "") {
		problems.Invalid("web_username", "网页登录账号和密码需要一起填写")
	}
	usdRate, hasRate := usdRateInput(in, &problems)
	if !hasRate {
		usdRate = defaultUSDCNYRate
	}
	if e := problems.Err(); e != nil {
		writeError(w, r, e)
		return
	}
	id, t := newID(), now()
	sealed, err := a.keys.Seal([]byte(token), []byte(id))
	if err != nil {
		writeError(w, r, secretError("connection_token", "平台令牌加密失败", err))
		return
	}
	webSecret := ""
	if webPassword != "" {
		if webSecret, err = a.keys.Seal([]byte(webPassword), []byte(id+":web")); err != nil {
			writeError(w, r, secretError("web_password", "网页登录密码加密失败", err))
			return
		}
	}
	if _, err = a.DB.ExecContext(r.Context(), `INSERT INTO platform_connections(id,name,base_url,token_secret,timezone,allowed_plans_json,enabled,created_at,updated_at,web_username,web_password_secret,usd_cny_rate) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, str(in, "name"), base.String(), sealed, timezone, `[]`, 1, t, t, webUser, webSecret, usdRate); err != nil {
		dbError(w, r, err, "CONNECTION_EXISTS", "平台连接已存在", "平台连接保存失败")
		return
	}
	a.auditFor(r, "connection.create", "platform_connection", id, base.Host)
	if webSecret != "" {
		a.auditFor(r, "connection.web_credentials_set", "platform_connection", id, webUser)
	}
	write(w, http.StatusCreated, map[string]any{"id": id, "version": 1})
}

func (a *App) platformConnectionDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	switch r.Method {
	case http.MethodPut, http.MethodPatch:
		a.updatePlatformConnection(w, r, id)
	case http.MethodDelete:
		a.deletePlatformConnection(w, r, id)
	default:
		methodNotAllowed(w, http.MethodPut, http.MethodPatch, http.MethodDelete)
	}
}

func (a *App) updatePlatformConnection(w http.ResponseWriter, r *http.Request, id string) {
	if a.keys == nil {
		fail(w, http.StatusServiceUnavailable, "ENCRYPTION_KEY_REQUIRED", "未配置凭证加密密钥")
		return
	}
	var in map[string]any
	if !decodeJSON(w, r, &in, defaultBodyLimit) {
		return
	}
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, r, storageError(err, "平台连接更新失败"))
		return
	}
	defer tx.Rollback()
	var oldName, oldBase, oldTimezone, oldToken, oldWebUser, oldWebSecret string
	var oldEnabled, version int
	var oldRate float64
	if err = tx.QueryRow(`SELECT name,base_url,timezone,token_secret,enabled,version,web_username,web_password_secret,usd_cny_rate FROM platform_connections WHERE id=?`, id).Scan(&oldName, &oldBase, &oldTimezone, &oldToken, &oldEnabled, &version, &oldWebUser, &oldWebSecret, &oldRate); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusNotFound, "NOT_FOUND", "平台连接不存在")
			return
		}
		writeError(w, r, storageError(err, "平台连接更新失败"))
		return
	}
	if expected := expectedVersion(in); expected != 0 && expected != version {
		fail(w, http.StatusConflict, "VERSION_CONFLICT", "平台连接已被修改，请刷新后重试")
		return
	}
	baseText := str(in, "base_url")
	if baseText == "" {
		baseText = oldBase
	}
	var problems errs.Fields
	base, err := url.Parse(baseText)
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil {
		problems.Invalid("base_url", "平台地址必须是 https:// 开头的完整地址")
	}
	name := str(in, "name")
	if name == "" {
		name = oldName
	}
	timezone := str(in, "timezone")
	if timezone == "" {
		timezone = oldTimezone
	} else if !validTimezone(timezone) {
		problems.Invalid("timezone", "时区必须是有效的 IANA 时区，例如 Asia/Shanghai")
	}
	enabled := oldEnabled
	if raw, ok := in["enabled"]; ok {
		v, ok := raw.(bool)
		if !ok {
			problems.Invalid("enabled", "启用状态必须是开或关")
		} else {
			enabled = boolInt(v)
		}
	}
	usdRate, hasRate := usdRateInput(in, &problems)
	if !hasRate {
		usdRate = oldRate
	}
	// Web (console) credentials: web_clear drops them; a new password (with an
	// optional new username) replaces them; a username alone renames an existing
	// login. Any change lifts a suspension and forgets the cached client.
	webUser, webSecret, webEvent := oldWebUser, oldWebSecret, ""
	webClear, _ := in["web_clear"].(bool)
	newWebUser, newWebPassword := str(in, "web_username"), webPasswordOf(in)
	switch {
	case webClear:
		webUser, webSecret, webEvent = "", "", "connection.web_credentials_cleared"
	case newWebPassword != "":
		if newWebUser != "" {
			webUser = newWebUser
		}
		if webUser == "" {
			problems.Invalid("web_username", "网页登录账号和密码需要一起填写")
		}
		webEvent = "connection.web_credentials_set"
	case newWebUser != "" && newWebUser != oldWebUser:
		if oldWebSecret == "" {
			problems.Invalid("web_password", "网页登录账号和密码需要一起填写")
		}
		webUser, webEvent = newWebUser, "connection.web_credentials_set"
	}
	if e := problems.Err(); e != nil {
		writeError(w, r, e)
		return
	}
	if newWebPassword != "" && !webClear {
		if webSecret, err = a.keys.Seal([]byte(newWebPassword), []byte(id+":web")); err != nil {
			writeError(w, r, secretError("web_password", "网页登录密码加密失败", err))
			return
		}
	}
	secret := oldToken
	if token := str(in, "token"); token != "" {
		var e error
		if secret, e = a.keys.Seal([]byte(token), []byte(id)); e != nil {
			writeError(w, r, secretError("connection_token", "平台令牌加密失败", e))
			return
		}
	}
	suspension := ""
	if webEvent != "" {
		suspension = ",web_suspended_at='',web_suspended_reason=''"
	}
	if _, err = tx.Exec(`UPDATE platform_connections SET name=?,base_url=?,token_secret=?,timezone=?,enabled=?,web_username=?,web_password_secret=?,usd_cny_rate=?`+suspension+`,version=version+1,updated_at=? WHERE id=? AND version=?`, name, base.String(), secret, timezone, enabled, webUser, webSecret, usdRate, now(), id, version); err != nil {
		dbError(w, r, err, "CONNECTION_EXISTS", "平台连接已存在", "平台连接更新失败")
		return
	}
	if err = tx.Commit(); err != nil {
		writeError(w, r, storageError(err, "平台连接更新失败"))
		return
	}
	a.dropWebClient(id)
	a.auditFor(r, "connection.update", "platform_connection", id, base.Host)
	if webEvent != "" {
		a.auditFor(r, webEvent, "platform_connection", id, webUser)
	}
	if usdRate != oldRate {
		a.auditFor(r, "connection.usd_rate_set", "platform_connection", id, formatRate(oldRate)+" → "+formatRate(usdRate))
	}
	write(w, http.StatusOK, map[string]any{"id": id, "version": version + 1})
}

func (a *App) deletePlatformConnection(w http.ResponseWriter, r *http.Request, id string) {
	var linked, queued, billHistory int
	err := a.DB.QueryRowContext(r.Context(), `SELECT (SELECT COUNT(*) FROM platform_links WHERE connection_id=?),(SELECT COUNT(*) FROM jobs WHERE connection_id=? AND state IN ('queued','running')),(SELECT COUNT(*) FROM platform_bill_daily WHERE connection_id=?)+(SELECT COUNT(*) FROM sync_runs WHERE connection_id=? AND kind='bill')`, id, id, id, id).Scan(&linked, &queued, &billHistory)
	if err != nil {
		writeError(w, r, storageError(err, "平台连接删除失败"))
		return
	}
	if linked > 0 || queued > 0 || billHistory > 0 {
		fail(w, http.StatusConflict, "CONNECTION_IN_USE", "平台连接仍有关联账号、任务或历史账单，请保留连接并停用")
		return
	}
	result, err := a.DB.ExecContext(r.Context(), `DELETE FROM platform_connections WHERE id=?`, id)
	if err != nil {
		writeError(w, r, storageError(err, "平台连接删除失败"))
		return
	}
	if count, _ := result.RowsAffected(); count == 0 {
		fail(w, http.StatusNotFound, "NOT_FOUND", "平台连接不存在")
		return
	}
	a.dropWebClient(id)
	a.auditFor(r, "connection.delete", "platform_connection", id, "")
	write(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}

// jobs lists submission jobs newest first. With page/page_size it filters and
// paginates server-side; without them it returns the legacy bounded array.
func (a *App) jobs(w http.ResponseWriter, r *http.Request) {
	page, size, paged, ok := pageParams(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	var f listFilter
	f.equals("state", q.Get("state"))
	f.equals("kind", q.Get("kind"))
	f.contains("account_id", q.Get("account_id"))
	f.contains("connection_id", q.Get("connection_id"))
	f.dateRange("created_at", q.Get("created_from"), q.Get("created_to"))
	f.keyword(q.Get("q"), "id", "account_id", "kind", "state", "connection_id")
	if frag, args := ownershipFrom(r).jobsOwnershipSQL("account_id"); frag != "" {
		f.where = append(f.where, strings.TrimPrefix(frag, " AND "))
		f.args = append(f.args, args...)
	}
	total := 0
	limit, offset := 2000, 0
	if paged {
		if err := a.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM jobs`+f.sql(), f.args...).Scan(&total); err != nil {
			writeError(w, r, storageError(err, "任务读取失败"))
			return
		}
		limit, offset = size, (page-1)*size
	}
	rows, err := a.DB.QueryContext(r.Context(), `SELECT id,kind,state,account_id,connection_id,result_json,created_at,updated_at FROM jobs`+f.sql()+` ORDER BY created_at DESC,rowid DESC LIMIT ? OFFSET ?`, append(append([]any{}, f.args...), limit, offset)...)
	if err != nil {
		writeError(w, r, storageError(err, "任务读取失败"))
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, kind, state, account, connection, result, created, updated string
		if err = rows.Scan(&id, &kind, &state, &account, &connection, &result, &created, &updated); err != nil {
			writeError(w, r, storageError(err, "任务读取失败"))
			return
		}
		var data struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal([]byte(result), &data)
		out = append(out, map[string]any{"id": id, "kind": kind, "state": state, "account_id": account, "connection_id": connection, "code": data.Code, "message": data.Message, "created_at": created, "updated_at": updated})
	}
	if err = rows.Err(); err != nil {
		writeError(w, r, storageError(err, "任务读取失败"))
		return
	}
	if paged {
		write(w, http.StatusOK, pagedEnvelope(out, page, size, total))
		return
	}
	write(w, http.StatusOK, out)
}

func mergeMaps(a, b map[string]any) map[string]any {
	for k, v := range b {
		a[k] = v
	}
	return a
}

// metricsSyncHealth summarises the platform metrics sync for the dashboard.
// Only platform-usage runs of enabled connections count (bill runs have their
// own view; a deleted or disabled connection never retries). A failing
// connection always wins over a healthy one, and among failing ones the
// longest streak is reported, so a working connection can never hide a
// broken one.
func (a *App) metricsSyncHealth(ctx context.Context) (map[string]any, error) {
	return a.syncHealth(ctx, "platform-usage")
}

// syncHealth is metricsSyncHealth for any sync kind (platform-usage,
// web-accounts, web-bills).
func (a *App) syncHealth(ctx context.Context, kind string) (map[string]any, error) {
	out := map[string]any{"sync_status": "never", "last_sync_at": "", "last_sync_state": "", "error_code": "", "error_message": "", "error_hint": "", "consecutive_failures": 0, "next_attempt_at": ""}
	rows, err := a.DB.QueryContext(ctx, `SELECT id FROM platform_connections WHERE enabled=1`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	type latestRun struct{ at, state, code, message string }
	var okRun *latestRun
	var worst *latestRun
	worstCount, worstNext := 0, time.Time{}
	worstSkip := false
	now := time.Now()
	for _, id := range ids {
		var r latestRun
		err = a.DB.QueryRowContext(ctx, `SELECT COALESCE(finished_at,started_at),state,COALESCE(error_code,''),COALESCE(error_message,'') FROM sync_runs WHERE connection_id=? AND kind=? AND state<>'running' ORDER BY started_at DESC,rowid DESC LIMIT 1`, id, kind).Scan(&r.at, &r.state, &r.code, &r.message)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if r.state == "succeeded" {
			if okRun == nil || r.at > okRun.at {
				rr := r
				okRun = &rr
			}
			continue
		}
		n, next, skip, err := a.syncBackoff(ctx, id, kind, now)
		if err != nil {
			return nil, err
		}
		if worst == nil || n > worstCount {
			rr := r
			worst, worstCount, worstNext, worstSkip = &rr, n, next, skip
		}
	}
	switch {
	case worst != nil:
		code := worst.code
		if code == "" {
			code = legacyMetricsCode(worst.message)
		}
		out["sync_status"], out["last_sync_at"], out["last_sync_state"], out["error_code"] = "failing", worst.at, worst.state, code
		if def, ok := errs.Lookup(code); ok {
			out["error_message"], out["error_hint"] = def.Message, def.Hint
		}
		out["consecutive_failures"] = worstCount
		// Only a retry that is still ahead is worth showing.
		if worstSkip {
			out["next_attempt_at"] = worstNext.Format(time.RFC3339)
		}
	case okRun != nil:
		out["sync_status"], out["last_sync_at"], out["last_sync_state"] = "ok", okRun.at, okRun.state
	}
	return out, nil
}

// legacyMetricsCode classifies runs recorded before error_code existed.
func legacyMetricsCode(message string) string {
	switch {
	case strings.Contains(message, "HTTP 404"):
		return errs.CodePlatformQueryDenied
	case strings.Contains(message, "HTTP 401"), strings.Contains(message, "HTTP 403"):
		return errs.CodePlatformAuthFailed
	case strings.Contains(message, "proxyconnect"), strings.Contains(message, "dial tcp"), strings.Contains(message, "HTTP 5"), strings.Contains(message, "timeout"):
		return errs.CodePlatformUnavailable
	default:
		return errs.CodeInternalError
	}
}
