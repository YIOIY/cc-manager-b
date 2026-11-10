package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"cc-manager/internal/errs"
	"cc-manager/internal/supplier"
)

type billSupplier interface {
	QueryBillResult(context.Context, []string, string, string) (supplier.BillQueryResult, error)
}

// billBad is a 422 bill-request problem identified by a registered code.
func billBad(code, message string) error { return errs.Adhoc(code, 422, message) }

// billField is a bill-request problem tied to one input field.
func billField(field, code, message string) error {
	return errs.New(errs.CodeValidationFailed).WithFields(errs.FieldError{Field: field, Code: code, Message: message})
}
func billFail(w http.ResponseWriter, err error) {
	var e *errs.Error
	if errors.As(err, &e) {
		writeError(w, nil, e)
	} else {
		writeError(w, nil, errs.New(errs.CodeBillInternalError).WithCause(err))
	}
}
func decodeBillRequest(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		fail(w, 400, "REQUEST_MALFORMED", "请求参数格式错误或含有不支持的字段")
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		fail(w, 400, "REQUEST_MALFORMED", "请求体只能包含一组参数")
		return false
	}
	return true
}

type billSyncRequest struct {
	ConnectionID    string   `json:"connection_id"`
	Scope           string   `json:"scope"`
	AccountIDs      []string `json:"account_ids,omitempty"`
	From            string   `json:"from"`
	To              string   `json:"to"`
	IncludeArchived bool     `json:"include_archived"`
}
type billRunSpec struct {
	billSyncRequest
	Version  int    `json:"version"`
	Timezone string `json:"timezone"`
}
type billLink struct{ Account, Upstream string }
type billItem struct {
	ID, Account, Upstream, Day, Timezone string
	Generation                           int64
	Attempts                             int
}

func uniqueSorted(ids []string) []string {
	set := map[string]bool{}
	for _, id := range ids {
		if id != "" {
			set[id] = true
		}
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
func marks(n int) string { return strings.TrimRight(strings.Repeat("?,", n), ",") }
func loadBillLocation(zone string) (*time.Location, error) {
	if zone == "" {
		return nil, billField("timezone", errs.FieldRequired, "请先配置平台连接时区")
	}
	if zone == "Local" {
		return nil, billField("timezone", errs.FieldUnsupportedValue, "请配置明确的平台 IANA 时区")
	}
	loc, e := time.LoadLocation(zone)
	if e != nil {
		return nil, billField("timezone", errs.FieldInvalidFormat, "平台连接时区无效")
	}
	return loc, nil
}
func (a *App) newBillClient(base, token string) (billSupplier, error) {
	if a.billFactory != nil {
		return a.billFactory(base, token)
	}
	return supplier.New(base, token)
}

// rowQuerier is satisfied by *sql.DB and *sql.Tx so the idempotency replay
// can run both before and inside the write transaction.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// billConnection is the resolved platform connection a bill run targets.
type billConnection struct {
	zone    string
	loc     *time.Location
	version int
}

// billRunPlan is the frozen scope of a run: every linked account and day it
// will fetch, after all validation passed.
type billRunPlan struct {
	input      billSyncRequest
	conn       billConnection
	links      []billLink
	start, end time.Time
	days       int
}

// createBillRun validates the request, freezes its scope and queues the run.
// Each step returns a *billAPIError for client faults; anything else is a
// storage failure.
func (a *App) createBillRun(ctx context.Context, input billSyncRequest, key, parent string) (map[string]any, error) {
	input, keyHash, err := normalizeBillRequest(input, key)
	if err != nil {
		return nil, err
	}
	if key != "" {
		if resp, found, e := replayBillRun(ctx, a.DB, key, keyHash); e != nil || found {
			return resp, e
		}
	}
	conn, err := a.resolveBillConnection(ctx, input.ConnectionID)
	if err != nil {
		return nil, err
	}
	dates, err := supplier.ValidateBillDates(input.From, input.To, conn.loc, time.Now(), true)
	if err != nil {
		return nil, billBad("BILL_WINDOW_INVALID", "同步日期必须在平台时区今天及此前 90 天内，且起止顺序正确")
	}
	input.From, input.To = dates.StartDate, dates.EndDate
	links, err := a.collectBillLinks(ctx, input)
	if err != nil {
		return nil, err
	}
	plan, err := a.planBillRun(input, conn, links)
	if err != nil {
		return nil, err
	}
	return a.insertBillRun(ctx, plan, key, keyHash, parent)
}

// normalizeBillRequest applies the pure request checks and returns the hash
// that ties an idempotency key to this exact scope.
func normalizeBillRequest(input billSyncRequest, key string) (billSyncRequest, string, error) {
	var problems errs.Fields
	if len(key) > 200 {
		problems.Add("idempotency_key", errs.FieldOutOfRange, "重复提交保护键过长")
	}
	if input.ConnectionID == "" {
		problems.Required("connection_id")
	}
	input.AccountIDs = uniqueSorted(input.AccountIDs)
	if len(input.AccountIDs) > 10000 {
		return input, "", billBad("SYNC_SCOPE_TOO_LARGE", "单次最多选择 10000 个账号，请缩小范围")
	}
	if input.Scope != "selected" && input.Scope != "all_linked" {
		problems.Add("scope", errs.FieldUnsupportedValue, "请选择指定账号或全部关联账号")
	}
	if input.Scope == "selected" && len(input.AccountIDs) == 0 {
		problems.Required("accounts")
	}
	if input.Scope == "all_linked" && len(input.AccountIDs) > 0 {
		problems.Add("scope", errs.FieldMismatch, "全部账号模式不能同时指定账号")
	}
	if e := problems.Err(); e != nil {
		return input, "", e
	}
	normalized, err := supplier.ValidateBillDates(input.From, input.To, nil, time.Time{}, false)
	if err != nil {
		return input, "", billBad("BILL_RANGE_INVALID", "账单起止日期无效")
	}
	input.From, input.To = normalized.StartDate, normalized.EndDate
	publicPayload, _ := json.Marshal(input)
	return input, fmt.Sprintf("%x", sha256.Sum256(publicPayload)), nil
}

// replayBillRun returns the earlier run bound to key. found is false when the
// key is unused; a hash mismatch is a 409.
func replayBillRun(ctx context.Context, q rowQuerier, key, keyHash string) (map[string]any, bool, error) {
	var id, hash, state string
	err := q.QueryRowContext(ctx, `SELECT r.id,k.payload_hash,r.state FROM bill_run_keys k JOIN sync_runs r ON r.id=k.run_id WHERE k.key=?`, "bills:"+key).Scan(&id, &hash, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	if hash != keyHash {
		return nil, true, errs.New(errs.CodeIdempotencyConflict).WithMessage("相同的重复提交保护键已用于不同的同步范围")
	}
	return map[string]any{"run_id": id, "state": state, "reused": true}, true, nil
}

// resolveBillConnection loads the connection and proves its token and zone
// are usable before any rows are written.
func (a *App) resolveBillConnection(ctx context.Context, connectionID string) (billConnection, error) {
	var conn billConnection
	var base, secret string
	var enabled int
	if err := a.DB.QueryRowContext(ctx, `SELECT timezone,base_url,token_secret,version,enabled FROM platform_connections WHERE id=?`, connectionID).Scan(&conn.zone, &base, &secret, &conn.version, &enabled); err != nil || enabled != 1 {
		return conn, billBad("CONNECTION_DISABLED", "平台连接不存在或未启用")
	}
	loc, err := loadBillLocation(conn.zone)
	if err != nil {
		return conn, err
	}
	conn.loc = loc
	if a.keys == nil {
		return conn, billBad("ENCRYPTION_KEY_REQUIRED", "未配置加密密钥")
	}
	token, err := a.openSecret(secret, connectionID)
	if err != nil {
		return conn, secretError("connection_token", "平台令牌无法解密", err)
	}
	if _, err = a.newBillClient(base, string(token)); err != nil {
		return conn, billBad("CONNECTION_INVALID", "平台地址或令牌配置无效")
	}
	return conn, nil
}

// collectBillLinks resolves the accepted platform links in scope.
func (a *App) collectBillLinks(ctx context.Context, input billSyncRequest) ([]billLink, error) {
	query := `SELECT l.account_id,l.upstream_id FROM platform_links l JOIN accounts a ON a.id=l.account_id WHERE l.connection_id=? AND l.ingestion_state='accepted'`
	args := []any{input.ConnectionID}
	if !input.IncludeArchived {
		query += ` AND a.archived=0`
	}
	if input.Scope == "selected" {
		query += ` AND l.account_id IN (` + marks(len(input.AccountIDs)) + `)`
		for _, id := range input.AccountIDs {
			args = append(args, id)
		}
	}
	query += ` ORDER BY l.account_id`
	rows, err := a.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	links := []billLink{}
	for rows.Next() {
		var l billLink
		if err = rows.Scan(&l.Account, &l.Upstream); err != nil {
			return nil, err
		}
		if n, e := strconv.ParseInt(l.Upstream, 10, 64); e != nil || n <= 0 {
			return nil, billBad("UPSTREAM_ID_INVALID", "账号缺少有效供应商账号 ID")
		}
		links = append(links, l)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(links) == 0 {
		return nil, billBad("NO_LINKED_ACCOUNTS", "所选范围没有可同步的关联账号")
	}
	if input.Scope == "selected" && len(links) != len(input.AccountIDs) {
		return nil, billBad("ACCOUNT_LINK_MISSING", "部分账号未关联该连接或已归档，请检查选择范围")
	}
	return links, nil
}

// planBillRun sizes the run against the configured ceiling and freezes the
// resolved account list, even when the request said all_linked.
func (a *App) planBillRun(input billSyncRequest, conn billConnection, links []billLink) (billRunPlan, error) {
	start, _ := time.Parse("2006-01-02", input.From)
	end, _ := time.Parse("2006-01-02", input.To)
	days := int(end.Sub(start)/(24*time.Hour)) + 1
	limit := a.cfg.BillMaxItems
	if limit <= 0 {
		limit = defaultBillMaxItems
	}
	if len(links) > limit/days {
		return billRunPlan{}, billBad("SYNC_SCOPE_TOO_LARGE", fmt.Sprintf("本次同步 %d 个账号日，超过上限 %d，请缩小范围", len(links)*days, limit))
	}
	input.AccountIDs = make([]string, 0, len(links))
	for _, l := range links {
		input.AccountIDs = append(input.AccountIDs, l.Account)
	}
	return billRunPlan{input: input, conn: conn, links: links, start: start, end: end, days: days}, nil
}

// insertBillRun writes the run and its items in one transaction, reusing an
// identical queued/running run instead of creating a duplicate.
func (a *App) insertBillRun(ctx context.Context, plan billRunPlan, key, keyHash, parent string) (map[string]any, error) {
	spec := billRunSpec{plan.input, plan.conn.version, plan.conn.zone}
	payload, _ := json.Marshal(spec)
	digest := fmt.Sprintf("%x", sha256.Sum256(payload))
	total := len(plan.links) * plan.days
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if key != "" {
		if resp, found, e := replayBillRun(ctx, tx, key, keyHash); e != nil || found {
			return resp, e
		}
	}
	var existing, state string
	e := tx.QueryRow(`SELECT id,state FROM sync_runs WHERE kind='bill' AND fingerprint=? AND state IN ('queued','running') ORDER BY created_at LIMIT 1`, digest).Scan(&existing, &state)
	if e == nil {
		if key != "" {
			if _, err = tx.Exec(`INSERT INTO bill_run_keys(key,payload_hash,run_id) VALUES(?,?,?)`, "bills:"+key, keyHash, existing); err != nil {
				return nil, err
			}
			if err = tx.Commit(); err != nil {
				return nil, err
			}
		}
		return map[string]any{"run_id": existing, "state": state, "reused": true}, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	id, stamp := newID(), now()
	var idem any
	if key != "" {
		idem = "bills:" + key
	}
	if _, err = tx.Exec(`INSERT INTO sync_runs(id,connection_id,kind,state,started_at,request_json,fingerprint,idem_key,parent_id,total_items,created_at,updated_at) VALUES(?,?,'bill','queued','',?,?,?,?,?,?,?)`, id, plan.input.ConnectionID, string(payload), digest, idem, parent, total, stamp, stamp); err != nil {
		return nil, err
	}
	for day := plan.start; !day.After(plan.end); day = day.AddDate(0, 0, 1) {
		for _, l := range plan.links {
			if _, err = tx.Exec(`INSERT INTO bill_sync_items(id,run_id,connection_id,account_id,upstream_id,source_timezone,bill_date,updated_at) VALUES(?,?,?,?,?,?,?,?)`, newID(), id, plan.input.ConnectionID, l.Account, l.Upstream, plan.conn.zone, day.Format("2006-01-02"), stamp); err != nil {
				return nil, err
			}
		}
	}
	if key != "" {
		if _, err = tx.Exec(`INSERT INTO bill_run_keys(key,payload_hash,run_id) VALUES(?,?,?)`, "bills:"+key, keyHash, id); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"run_id": id, "state": "queued", "reused": false, "total_items": total, "account_count": len(plan.links), "planned_requests": ((len(plan.links) + 99) / 100) * plan.days, "from": plan.input.From, "to": plan.input.To}, nil
}

func reserveBillGeneration(tx *sql.Tx, conn, account, day string) (int64, error) {
	var gen int64
	err := tx.QueryRow(`INSERT INTO bill_sync_buckets(connection_id,account_id,bill_date,generation,last_state,last_attempt_at) VALUES(?,?,?,1,'running',?) ON CONFLICT(connection_id,account_id,bill_date) DO UPDATE SET generation=generation+1,last_state='running',last_attempt_at=excluded.last_attempt_at RETURNING generation`, conn, account, day, now()).Scan(&gen)
	return gen, err
}

func storeVerifiedBill(tx *sql.Tx, conn, account, day, zone string, generation int64, b supplier.Bill, result supplier.BillQueryResult) (string, error) {
	var current int64
	if err := tx.QueryRow(`SELECT generation FROM bill_sync_buckets WHERE connection_id=? AND account_id=? AND bill_date=?`, conn, account, day).Scan(&current); err != nil {
		return "", err
	}
	if current != generation {
		return "superseded", nil
	}
	state := "updated"
	var old supplier.Bill
	err := tx.QueryRow(`SELECT request_count,prompt_tokens,completion_tokens,cache_read_input_tokens,cache_write_input_tokens,cost_fen FROM platform_bill_daily WHERE connection_id=? AND account_id=? AND bill_date=?`, conn, account, day).Scan(&old.RequestCount, &old.PromptTokens, &old.CompletionTokens, &old.CacheReadInputTokens, &old.CacheWriteInputTokens, &old.CostFen)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	old.ID = b.ID
	if err == nil && old == b {
		state = "unchanged"
	}
	raw, _ := json.Marshal(result.ActualRange)
	stamp := now()
	_, err = tx.Exec(`INSERT INTO platform_bill_daily(id,account_id,connection_id,upstream_id,bill_date,request_count,prompt_tokens,completion_tokens,cache_read_input_tokens,cache_write_input_tokens,cost_fen,source_range_json,fetched_at,revision,finalized,sync_generation,requested_start_ds,requested_end_ds,actual_start_ds,actual_end_ds,source_timezone,response_verified,write_generation,last_changed_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,1,0,0,?,?,?,?,?,1,?,?) ON CONFLICT(account_id,connection_id,bill_date) DO UPDATE SET
upstream_id=excluded.upstream_id,request_count=excluded.request_count,prompt_tokens=excluded.prompt_tokens,completion_tokens=excluded.completion_tokens,cache_read_input_tokens=excluded.cache_read_input_tokens,cache_write_input_tokens=excluded.cache_write_input_tokens,cost_fen=excluded.cost_fen,source_range_json=excluded.source_range_json,fetched_at=excluded.fetched_at,
revision=platform_bill_daily.revision+CASE WHEN platform_bill_daily.request_count!=excluded.request_count OR platform_bill_daily.prompt_tokens!=excluded.prompt_tokens OR platform_bill_daily.completion_tokens!=excluded.completion_tokens OR platform_bill_daily.cache_read_input_tokens!=excluded.cache_read_input_tokens OR platform_bill_daily.cache_write_input_tokens!=excluded.cache_write_input_tokens OR platform_bill_daily.cost_fen!=excluded.cost_fen THEN 1 ELSE 0 END,
last_changed_at=CASE WHEN ?='updated' THEN excluded.last_changed_at ELSE COALESCE(platform_bill_daily.last_changed_at,platform_bill_daily.fetched_at) END,
requested_start_ds=excluded.requested_start_ds,requested_end_ds=excluded.requested_end_ds,actual_start_ds=excluded.actual_start_ds,actual_end_ds=excluded.actual_end_ds,source_timezone=excluded.source_timezone,response_verified=1,write_generation=excluded.write_generation,finalized=0`, newID(), account, conn, strconv.FormatInt(b.ID, 10), day, b.RequestCount, b.PromptTokens, b.CompletionTokens, b.CacheReadInputTokens, b.CacheWriteInputTokens, b.CostFen, string(raw), stamp, result.RequestedRange.StartDate, result.RequestedRange.EndDate, result.ActualRange.StartDate, result.ActualRange.EndDate, zone, generation, stamp, state)
	return state, err
}

func billErrorCode(err error) string {
	var httpErr *supplier.BillHTTPError
	if errors.As(err, &httpErr) {
		return fmt.Sprintf("UPSTREAM_HTTP_%d", httpErr.Status)
	}
	for _, code := range []string{"BILL_RANGE_INVALID", "BILL_RANGE_MISMATCH", "BILL_RESPONSE_INVALID", "BILL_ID_MISMATCH", "BILL_ROW_INVALID"} {
		if err.Error() == code {
			return code
		}
	}
	return "UPSTREAM_UNAVAILABLE"
}

const (
	billLeaseSeconds   = 120
	billMaxAttempts    = 3
	billBatchSize      = 100
	billMaxRetryDelay  = 30 * time.Second
	billRetryJitterMax = 250 * time.Millisecond
)

// billRun is one claimed sync_runs row being processed by this worker.
type billRun struct {
	a       *App
	ctx     context.Context
	id      string
	conn    string
	owner   string
	spec    billRunSpec
	zone    string
	version int
	loc     *time.Location
	client  billSupplier
}

// runBillJob claims at most one queued (or lease-expired) bill run and drives
// it to completion. The steps are: claim → prepare connection → per day:
// reserve items, fetch, persist → finish.
func (a *App) runBillJob(ctx context.Context) error {
	if !a.billMutex.TryLock() {
		return nil
	}
	defer a.billMutex.Unlock()
	run, claimed, err := a.claimBillRun(ctx)
	if err != nil || !claimed {
		return err
	}
	if code := run.prepare(); code != "" {
		return run.failPending(code)
	}
	return run.process()
}

func (a *App) claimBillRun(ctx context.Context) (*billRun, bool, error) {
	run := &billRun{a: a, ctx: ctx, owner: newID()}
	var payload string
	timestamp := time.Now().Unix()
	err := a.DB.QueryRowContext(ctx, `UPDATE sync_runs SET state='running',owner=?,lease_until=?,started_at=CASE WHEN started_at='' THEN ? ELSE started_at END,updated_at=? WHERE id=(SELECT r.id FROM sync_runs r WHERE r.kind='bill' AND (r.state='queued' OR (r.state='running' AND r.lease_until<?)) AND NOT EXISTS(SELECT 1 FROM sync_runs other WHERE other.kind='bill' AND other.connection_id=r.connection_id AND other.state='running' AND other.lease_until>=?) ORDER BY r.created_at,r.id LIMIT 1) RETURNING id,connection_id,request_json`, run.owner, timestamp+billLeaseSeconds, now(), now(), timestamp, timestamp).Scan(&run.id, &run.conn, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	// Items left running by a crashed owner go back to the queue.
	if _, err = a.DB.ExecContext(ctx, `UPDATE bill_sync_items SET state='queued' WHERE run_id=? AND state='running'`, run.id); err != nil {
		return nil, false, err
	}
	if json.Unmarshal([]byte(payload), &run.spec) != nil {
		return run, true, run.failPending("TASK_INVALID")
	}
	return run, true, nil
}

// prepare loads the connection and builds the supplier client. It returns a
// failure code when the whole run must be failed before any request is made.
func (r *billRun) prepare() string {
	var base, secret string
	var enabled int
	if r.a.DB.QueryRowContext(r.ctx, `SELECT base_url,token_secret,timezone,version,enabled FROM platform_connections WHERE id=?`, r.conn).Scan(&base, &secret, &r.zone, &r.version, &enabled) != nil || enabled != 1 {
		return "CONNECTION_DISABLED"
	}
	if r.zone != r.spec.Timezone || r.version != r.spec.Version {
		return "CONNECTION_CHANGED"
	}
	loc, err := loadBillLocation(r.zone)
	if err != nil {
		return "TIMEZONE_UNUSABLE"
	}
	r.loc = loc
	token, err := r.a.openSecret(secret, r.conn)
	if err != nil {
		return "CONNECTION_UNREADABLE"
	}
	if r.client, err = r.a.newBillClient(base, string(token)); err != nil {
		return "CONNECTION_INVALID"
	}
	return ""
}

func (r *billRun) failPending(code string) error {
	if err := r.a.failBillPending(r.ctx, r.id, r.owner, code); err != nil {
		return err
	}
	return r.a.finishBillRun(r.ctx, r.id, r.owner)
}

func (r *billRun) process() error {
	for {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		day, more, err := r.nextDay()
		if err != nil {
			return err
		}
		if !more {
			return r.a.finishBillRun(r.ctx, r.id, r.owner)
		}
		items, err := r.loadQueuedItems(day)
		if err != nil {
			return err
		}
		if _, err = supplier.ValidateBillDates(day, day, r.loc, time.Now(), true); err != nil {
			for _, item := range items {
				if err = r.a.failQueuedBillItem(r.ctx, r.conn, item, "WINDOW_EXPIRED"); err != nil {
					return err
				}
			}
			continue
		}
		valid, err := r.reserve(day, items)
		if err != nil {
			return err
		}
		if len(valid) == 0 {
			continue
		}
		result, requestErr, err := r.fetch(day, valid)
		if err != nil {
			return err
		}
		changed, err := r.persist(day, valid, result, requestErr)
		if err != nil {
			return err
		}
		if changed {
			return r.failPending("CONNECTION_CHANGED")
		}
		var httpErr *supplier.BillHTTPError
		if errors.As(requestErr, &httpErr) && (httpErr.Status == 401 || httpErr.Status == 403) {
			return r.failPending(fmt.Sprintf("UPSTREAM_HTTP_%d", httpErr.Status))
		}
	}
}

func (r *billRun) nextDay() (string, bool, error) {
	var day string
	err := r.a.DB.QueryRowContext(r.ctx, `SELECT bill_date FROM bill_sync_items WHERE run_id=? AND state='queued' ORDER BY bill_date LIMIT 1`, r.id).Scan(&day)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return day, err == nil, err
}

func (r *billRun) loadQueuedItems(day string) ([]billItem, error) {
	rows, err := r.a.DB.QueryContext(r.ctx, `SELECT id,account_id,upstream_id,source_timezone,attempts FROM bill_sync_items WHERE run_id=? AND state='queued' AND bill_date=? ORDER BY account_id LIMIT ?`, r.id, day, billBatchSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []billItem{}
	for rows.Next() {
		item := billItem{Day: day}
		if err = rows.Scan(&item.ID, &item.Account, &item.Upstream, &item.Timezone, &item.Attempts); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// reserve fails exhausted or re-linked items and marks the rest running with
// a fresh generation, all in one transaction.
func (r *billRun) reserve(day string, items []billItem) ([]billItem, error) {
	tx, err := r.a.DB.BeginTx(r.ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	valid := []billItem{}
	for _, item := range items {
		if item.Attempts >= billMaxAttempts {
			if err = failQueuedBillItemTx(tx, r.conn, item, "RETRY_EXHAUSTED"); err != nil {
				return nil, err
			}
			continue
		}
		var upstream string
		linkErr := tx.QueryRow(`SELECT upstream_id FROM platform_links WHERE connection_id=? AND account_id=? AND ingestion_state='accepted'`, r.conn, item.Account).Scan(&upstream)
		if linkErr != nil || upstream != item.Upstream {
			if err = failQueuedBillItemTx(tx, r.conn, item, "ACCOUNT_LINK_CHANGED"); err != nil {
				return nil, err
			}
			continue
		}
		if item.Generation, err = reserveBillGeneration(tx, r.conn, item.Account, day); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(`UPDATE bill_sync_items SET state='running',generation=?,updated_at=? WHERE id=?`, item.Generation, now(), item.ID); err != nil {
			return nil, err
		}
		valid = append(valid, item)
	}
	return valid, tx.Commit()
}

// fetch calls the supplier with bounded retries. requestErr is the final
// upstream error (if any); err is a local failure that aborts the run.
func (r *billRun) fetch(day string, valid []billItem) (result supplier.BillQueryResult, requestErr error, err error) {
	ids := make([]string, 0, len(valid))
	maxAttempts := billMaxAttempts
	for _, item := range valid {
		ids = append(ids, item.Upstream)
		if remaining := billMaxAttempts - item.Attempts; remaining < maxAttempts {
			maxAttempts = remaining
		}
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err = r.a.renewBillLease(r.ctx, r.id, r.owner); err != nil {
			return result, nil, err
		}
		if r.a.billInterval > 0 {
			if err = waitBill(r.ctx, r.a.billInterval); err != nil {
				return result, nil, err
			}
		}
		if _, err = r.a.DB.ExecContext(r.ctx, `UPDATE sync_runs SET http_requests=http_requests+1,http_retries=http_retries+? WHERE id=? AND owner=?`, boolInt(attempt > 0), r.id, r.owner); err != nil {
			return result, nil, err
		}
		if _, err = r.a.DB.ExecContext(r.ctx, `UPDATE bill_sync_items SET attempts=attempts+1 WHERE run_id=? AND state='running'`, r.id); err != nil {
			return result, nil, err
		}
		requestCtx, cancel := context.WithTimeout(r.ctx, supplierRequestTimeout)
		result, requestErr = r.client.QueryBillResult(requestCtx, ids, day, day)
		cancel()
		if requestErr == nil {
			return result, nil, nil
		}
		delay, retry := billRetryDelay(requestErr, attempt)
		if !retry || attempt == maxAttempts-1 {
			break
		}
		if err = waitBill(r.ctx, delay); err != nil {
			return result, nil, err
		}
	}
	return result, requestErr, nil
}

// billRetryDelay decides whether an upstream error is retryable and how long
// to wait: exponential backoff with jitter, honouring Retry-After, capped.
func billRetryDelay(requestErr error, attempt int) (time.Duration, bool) {
	var httpErr *supplier.BillHTTPError
	var netErr net.Error
	retry := errors.As(requestErr, &netErr) || errors.Is(requestErr, context.DeadlineExceeded)
	delay := time.Duration(1<<attempt)*time.Second + time.Duration(rand.IntN(int(billRetryJitterMax/time.Millisecond)))*time.Millisecond
	if errors.As(requestErr, &httpErr) {
		retry = httpErr.Status == 408 || httpErr.Status == 429 || httpErr.Status >= 500
		if httpErr.RetryAfter > delay {
			delay = httpErr.RetryAfter
		}
	}
	if delay > billMaxRetryDelay {
		delay = billMaxRetryDelay
	}
	return delay, retry
}

// persist writes the outcome for every reserved item. It reports whether the
// connection changed underneath the run (which fails the remaining items).
func (r *billRun) persist(day string, valid []billItem, result supplier.BillQueryResult, requestErr error) (bool, error) {
	tx, err := r.a.DB.BeginTx(r.ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var currentOwner string
	if err = tx.QueryRow(`SELECT owner FROM sync_runs WHERE id=?`, r.id).Scan(&currentOwner); err != nil || currentOwner != r.owner {
		return false, errors.New("bill lease lost")
	}
	var currentVersion int
	err = tx.QueryRow(`SELECT version FROM platform_connections WHERE id=? AND enabled=1 AND timezone=?`, r.conn, r.zone).Scan(&currentVersion)
	changed := err != nil || currentVersion != r.version
	byID := map[string]supplier.Bill{}
	for _, b := range result.Bills {
		byID[strconv.FormatInt(b.ID, 10)] = b
	}
	for _, item := range valid {
		state, code := "no_data", ""
		var upstream string
		linkErr := tx.QueryRow(`SELECT upstream_id FROM platform_links WHERE connection_id=? AND account_id=? AND ingestion_state='accepted'`, r.conn, item.Account).Scan(&upstream)
		switch {
		case changed:
			state, code = "failed", "CONNECTION_CHANGED"
		case linkErr != nil || upstream != item.Upstream:
			state, code = "failed", "ACCOUNT_LINK_CHANGED"
		case requestErr != nil:
			state, code = "failed", billErrorCode(requestErr)
		case result.RowErrors[item.Upstream] != "":
			state, code = "failed", result.RowErrors[item.Upstream]
		default:
			if b, ok := byID[item.Upstream]; ok {
				if state, err = storeVerifiedBill(tx, r.conn, item.Account, day, r.zone, item.Generation, b, result); err != nil {
					return false, err
				}
			}
		}
		var latest int64
		if err = tx.QueryRow(`SELECT generation FROM bill_sync_buckets WHERE connection_id=? AND account_id=? AND bill_date=?`, r.conn, item.Account, day).Scan(&latest); err != nil {
			return false, err
		}
		if latest != item.Generation {
			state, code = "superseded", ""
		}
		if _, err = tx.Exec(`UPDATE bill_sync_items SET state=?,error_code=?,error_message=?,updated_at=? WHERE id=?`, state, code, billMessage(code), now(), item.ID); err != nil {
			return false, err
		}
		if _, err = tx.Exec(`UPDATE bill_sync_buckets SET last_state=?,last_attempt_at=? WHERE connection_id=? AND account_id=? AND bill_date=? AND generation=?`, state, now(), r.conn, item.Account, day, item.Generation); err != nil {
			return false, err
		}
	}
	return changed, tx.Commit()
}

func waitBill(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func billMessage(code string) string {
	if code == "" {
		return ""
	}
	if def, ok := errs.Lookup(code); ok {
		return def.Message
	}
	if strings.HasPrefix(code, "UPSTREAM_HTTP_") {
		return "平台返回了错误状态（" + strings.TrimPrefix(code, "UPSTREAM_HTTP_") + "）"
	}
	// Codes written by earlier versions and since renamed or merged.
	if legacy, ok := map[string]string{"TIMEZONE_INVALID": "平台连接的时区无法使用"}[code]; ok {
		return legacy
	}
	return "处理失败（" + code + "）"
}

func failQueuedBillItemTx(tx *sql.Tx, conn string, item billItem, code string) error {
	generation, err := reserveBillGeneration(tx, conn, item.Account, item.Day)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE bill_sync_items SET state='failed',generation=?,error_code=?,error_message=?,updated_at=? WHERE id=?`, generation, code, billMessage(code), now(), item.ID); err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE bill_sync_buckets SET last_state='failed',last_attempt_at=? WHERE connection_id=? AND account_id=? AND bill_date=? AND generation=?`, now(), conn, item.Account, item.Day, generation)
	return err
}

func (a *App) failQueuedBillItem(ctx context.Context, conn string, item billItem, code string) error {
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = failQueuedBillItemTx(tx, conn, item, code); err != nil {
		return err
	}
	return tx.Commit()
}
func (a *App) renewBillLease(ctx context.Context, run, owner string) error {
	r, e := a.DB.ExecContext(ctx, `UPDATE sync_runs SET lease_until=?,updated_at=? WHERE id=? AND owner=? AND state='running'`, time.Now().Unix()+billLeaseSeconds, now(), run, owner)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e == nil && n == 0 {
		return errors.New("bill lease lost")
	}
	return e
}
func (a *App) finishBillRun(ctx context.Context, run, owner string) error {
	var total, failed, good, superseded int
	err := a.DB.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(state='failed'),0),COALESCE(SUM(state IN ('updated','unchanged','no_data')),0),COALESCE(SUM(state='superseded'),0) FROM bill_sync_items WHERE run_id=?`, run).Scan(&total, &failed, &good, &superseded)
	if err != nil {
		return err
	}
	state := "succeeded"
	if failed+superseded > 0 {
		if good > 0 {
			state = "partial"
		} else if failed > 0 {
			state = "failed"
		} else {
			state = "interrupted"
		}
	}
	_, err = a.DB.ExecContext(ctx, `UPDATE sync_runs SET state=?,processed=?,failed=?,finished_at=?,last_success_at=CASE WHEN ?='succeeded' THEN ? ELSE last_success_at END,updated_at=?,lease_until=0 WHERE id=? AND owner=?`, state, good, failed, now(), state, now(), now(), run, owner)
	return err
}

func (a *App) failBillPending(ctx context.Context, run, owner, code string) error {
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var actualOwner string
	if err = tx.QueryRow(`SELECT owner FROM sync_runs WHERE id=?`, run).Scan(&actualOwner); err != nil {
		return err
	}
	if actualOwner != owner {
		return errors.New("bill lease lost")
	}
	rows, err := tx.Query(`SELECT id,connection_id,account_id,bill_date,generation FROM bill_sync_items WHERE run_id=? AND state IN ('queued','running')`, run)
	if err != nil {
		return err
	}
	type pending struct {
		id, conn, account, day string
		generation             int64
	}
	items := []pending{}
	for rows.Next() {
		var item pending
		if err = rows.Scan(&item.id, &item.conn, &item.account, &item.day, &item.generation); err != nil {
			break
		}
		items = append(items, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.generation == 0 {
			item.generation, err = reserveBillGeneration(tx, item.conn, item.account, item.day)
			if err != nil {
				return err
			}
		}
		_, err = tx.Exec(`UPDATE bill_sync_buckets SET last_state='failed',last_attempt_at=? WHERE connection_id=? AND account_id=? AND bill_date=? AND generation=?`, now(), item.conn, item.account, item.day, item.generation)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE bill_sync_items SET state='failed',error_code=?,error_message=?,updated_at=? WHERE id=?`, code, billMessage(code), now(), item.id)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
