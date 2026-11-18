package app

import (
	"cc-manager/internal/errs"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// decorateAccountDelivery exposes delivery state without exposing credentials.
// A platform link is authoritative even when usage queries are unavailable.
func (a *App) decorateAccountDelivery(items []any) error {
	if len(items) == 0 {
		return nil
	}
	accounts := map[string]map[string]any{}
	ids := make([]any, 0, len(items))
	for _, item := range items {
		m := item.(map[string]any)
		accounts[m["id"].(string)] = m
		ids = append(ids, m["id"])
		m["platform_links"] = []any{}
		m["onboarding_state"] = "pending"
	}
	// Every lookup is scoped to the ids on this page; the list is paged, so
	// scanning the whole deliveries/jobs history per request would dominate.
	in := " IN (?" + strings.Repeat(",?", len(ids)-1) + ")"
	rows, err := a.DB.Query(`SELECT a.id,COALESCE(a.metadata_json,'{}'),COALESCE(a.credentials_json,'') NOT IN ('','{}'),EXISTS(SELECT 1 FROM account_secrets s WHERE s.account_id=a.id AND s.password_secret<>'') FROM accounts a WHERE a.id`+in, ids...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, metadata string
		var auth, password bool
		if err = rows.Scan(&id, &metadata, &auth, &password); err != nil {
			rows.Close()
			return err
		}
		if m := accounts[id]; m != nil {
			m["credentials_configured"] = auth
			m["password_configured"] = password
			var optional map[string]any
			if json.Unmarshal([]byte(metadata), &optional) == nil {
				for _, key := range []string{"payment_method", "register_device", "recharge_time", "warranty_period", "balance", "tags", "remark"} {
					if value, ok := optional[key]; ok {
						m[key] = value
					}
				}
			}
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = a.DB.Query(`SELECT l.account_id,l.connection_id,COALESCE(c.name,''),COALESCE(l.upstream_id,''),COALESCE(l.upstream_aid,''),COALESCE(l.alias,''),COALESCE(l.runtime_status,''),l.ingestion_state FROM platform_links l LEFT JOIN platform_connections c ON c.id=l.connection_id WHERE l.account_id`+in+` ORDER BY l.updated_at DESC,l.id DESC`, ids...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, conn, name, upstream, aid, alias, status, state string
		if err = rows.Scan(&id, &conn, &name, &upstream, &aid, &alias, &status, &state); err != nil {
			rows.Close()
			return err
		}
		if m := accounts[id]; m != nil {
			m["platform_links"] = append(m["platform_links"].([]any), map[string]any{"connection_id": conn, "connection_name": name, "upstream_id": upstream, "platform_account_id": aid, "alias": alias, "status": status, "ingestion_state": state})
			if state == "accepted" && upstream != "" {
				m["onboarding_state"] = "linked"
			}
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = a.DB.Query(`SELECT account_id,ingestion_state FROM deliveries WHERE account_id`+in+` ORDER BY updated_at DESC,id DESC`, ids...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, state string
		if err = rows.Scan(&id, &state); err != nil {
			rows.Close()
			return err
		}
		if m := accounts[id]; m != nil && m["onboarding_state"] != "linked" && (state == "unknown" || state == "accepted") {
			m["onboarding_state"] = "unknown"
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = a.DB.Query(`SELECT id,account_id,COALESCE(connection_id,''),state,COALESCE(result_json,'{}'),COALESCE(updated_at,'') FROM jobs WHERE kind='account.submit' AND account_id`+in+` ORDER BY created_at DESC,rowid DESC`, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var id, account, conn, state, result, updated string
		if err = rows.Scan(&id, &account, &conn, &state, &result, &updated); err != nil {
			return err
		}
		m := accounts[account]
		if m == nil || seen[account] {
			continue
		}
		seen[account] = true
		var data struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			Hint      string `json:"hint"`
			Action    string `json:"action"`
			Retryable bool   `json:"retryable"`
		}
		_ = json.Unmarshal([]byte(result), &data)
		// Older rows stored only the code; the registry supplies the rest.
		if def, ok := errs.Lookup(data.Code); ok {
			if data.Message == "" {
				data.Message = def.Message
			}
			if data.Hint == "" {
				data.Hint = def.Hint
			}
			if data.Action == "" {
				data.Action = string(def.Action)
				data.Retryable = def.Retryable
			}
		}
		m["submission_job"] = map[string]any{"id": id, "connection_id": conn, "state": state, "code": data.Code, "message": data.Message, "hint": data.Hint, "action": data.Action, "retryable": data.Retryable, "updated_at": updated, "allowed_actions": allowedJobActions(state)}
		if m["onboarding_state"] == "linked" || m["onboarding_state"] == "unknown" {
			continue
		}
		switch state {
		case "queued", "running":
			m["onboarding_state"] = "submitting"
		case "failed":
			m["onboarding_state"] = "failed"
		case "unknown", "succeeded":
			m["onboarding_state"] = "unknown"
		}
	}
	return rows.Err()
}

// allowedJobActions says what a user can do about a submission job in this
// state; the frontend renders buttons from this, never from error text.
func allowedJobActions(state string) []string {
	switch state {
	case "failed":
		return []string{"retry"}
	case "unknown":
		return []string{"check_platform", "associate_platform_id"}
	default:
		return []string{}
	}
}

func deliveryBlocked(tx interface{ QueryRow(string, ...any) *sql.Row }, accountID, connectionID string) (string, string, error) {
	var exists int
	err := tx.QueryRow(`SELECT 1 WHERE EXISTS(SELECT 1 FROM platform_links WHERE account_id=? AND connection_id=? AND ingestion_state='accepted') OR EXISTS(SELECT 1 FROM deliveries WHERE account_id=? AND connection_id=? AND ingestion_state='accepted')`, accountID, connectionID, accountID, connectionID).Scan(&exists)
	if err == nil {
		return "ALREADY_LINKED", "账号已在该平台上号，请查看详情", nil
	}
	if err != sql.ErrNoRows {
		return "", "", err
	}
	err = tx.QueryRow(`SELECT 1 WHERE EXISTS(SELECT 1 FROM jobs WHERE account_id=? AND kind='account.submit' AND state IN ('queued','running'))`, accountID).Scan(&exists)
	if err == nil {
		return "SUBMIT_IN_PROGRESS", "该账号正在上号，请等待当前任务结束", nil
	}
	if err != sql.ErrNoRows {
		return "", "", err
	}
	err = tx.QueryRow(`SELECT 1 WHERE EXISTS(SELECT 1 FROM deliveries WHERE account_id=? AND ingestion_state='unknown') OR EXISTS(SELECT 1 FROM jobs WHERE account_id=? AND kind='account.submit' AND state='unknown')`, accountID, accountID).Scan(&exists)
	if err == nil {
		return "SUBMIT_OUTCOME_UNKNOWN", "上次上号结果待确认，请先在平台核实，避免重复提交", nil
	}
	if err != sql.ErrNoRows {
		return "", "", err
	}
	return "", "", nil
}

func (a *App) submitExistingAccount(w http.ResponseWriter, r *http.Request, accountID string) {
	if !a.requireMutable(w, r, "accounts", accountID, "账号不存在") {
		return
	}
	var in struct {
		ConnectionID string `json:"platform_connection_id"`
		Version      int    `json:"expected_version"`
	}
	if !decodeJSON(w, r, &in, 16<<10) {
		return
	}
	if in.ConnectionID == "" {
		writeError(w, r, fieldErr("platform_connection_id", errs.FieldRequired, "缺少平台连接"))
		return
	}
	var version int
	if a.DB.QueryRow(`SELECT version FROM accounts WHERE id=?`, accountID).Scan(&version) != nil {
		fail(w, 404, "NOT_FOUND", "账号不存在")
		return
	}
	if in.Version != 0 && in.Version != version {
		fail(w, 409, "VERSION_CONFLICT", "账号资料已更新，请刷新后重新检查")
		return
	}
	payload, _ := json.Marshal(submitJob{ConnectionID: in.ConnectionID, Version: version})
	hash := fmt.Sprintf("%x", sha256.Sum256(append([]byte(accountID+":"), payload...)))
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, r, storageError(err, "上号任务创建失败"))
		return
	}
	defer tx.Rollback()
	if key != "" {
		var oldHash, job, state string
		if tx.QueryRow(`SELECT i.payload_hash,i.job_id,j.state FROM idempotency_keys i JOIN jobs j ON j.id=i.job_id WHERE i.key=?`, key).Scan(&oldHash, &job, &state) == nil {
			if oldHash != hash {
				fail(w, 409, "IDEMPOTENCY_CONFLICT", "该请求已用于其他账号或平台")
				return
			}
			write(w, 202, map[string]any{"job_id": job, "state": state})
			return
		}
	}
	if code, message, e := deliveryBlocked(tx, accountID, in.ConnectionID); e != nil {
		writeError(w, r, storageError(e, "上号状态读取失败"))
		return
	} else if code != "" {
		fail(w, 409, code, message)
		return
	}
	// End this read transaction before preflight uses the single DB connection.
	tx.Rollback()
	pre := a.preflight(accountID, in.ConnectionID)
	if !pre["can_submit"].(bool) {
		// A concurrent submit may have queued this account after the check
		// above; report that conflict, not a misleading "incomplete" error.
		if code, message, e := deliveryBlocked(a.DB, accountID, in.ConnectionID); e == nil && code != "" {
			fail(w, 409, code, message)
			return
		}
		fail(w, 422, "PREFLIGHT_BLOCKED", "资料或凭证未完善，请先按预检提示补充")
		return
	}
	tx, err = a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, r, storageError(err, "上号任务创建失败"))
		return
	}
	defer tx.Rollback()
	var current int
	if tx.QueryRow(`SELECT version FROM accounts WHERE id=?`, accountID).Scan(&current) != nil || current != version {
		fail(w, 409, "VERSION_CONFLICT", "账号资料已更新，请重新检查")
		return
	}
	if code, message, e := deliveryBlocked(tx, accountID, in.ConnectionID); e != nil {
		writeError(w, r, storageError(e, "上号状态读取失败"))
		return
	} else if code != "" {
		fail(w, 409, code, message)
		return
	}
	job, stamp := newID(), now()
	if _, err = tx.Exec(`INSERT INTO jobs(id,kind,state,account_id,connection_id,payload_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, job, "account.submit", "queued", accountID, in.ConnectionID, string(payload), stamp, stamp); err != nil {
		writeError(w, r, storageError(err, "上号任务创建失败"))
		return
	}
	if _, err = tx.Exec(`INSERT INTO deliveries(id,account_id,connection_id,ingestion_state,created_at,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(account_id,connection_id) DO UPDATE SET ingestion_state='queued',updated_at=excluded.updated_at`, newID(), accountID, in.ConnectionID, "queued", stamp, stamp); err != nil {
		writeError(w, r, storageError(err, "交付记录保存失败"))
		return
	}
	if key != "" {
		if _, err = tx.Exec(`INSERT INTO idempotency_keys(key,payload_hash,job_id,created_at) VALUES(?,?,?,?)`, key, hash, job, stamp); err != nil {
			dbError(w, r, err, "IDEMPOTENCY_CONFLICT", "请求已提交，请刷新查看任务", "请求记录保存失败")
			return
		}
	}
	if err = tx.Commit(); err != nil {
		writeError(w, r, storageError(err, "上号任务创建失败"))
		return
	}
	a.auditFor(r, "account.submit", "account", accountID, "job="+job)
	write(w, 202, map[string]any{"job_id": job, "state": "queued"})
}
