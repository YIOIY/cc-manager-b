package app

import (
	"cc-manager/internal/errs"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (a *App) importPreview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Format  string `json:"format"`
		Content string `json:"content"`
	}
	if !decodeJSON(w, r, &in, importBodyLimit) {
		return
	}
	if strings.TrimSpace(in.Content) == "" {
		fail(w, 400, "REQUEST_MALFORMED", "导入请求无效")
		return
	}
	var raw any
	dec := json.NewDecoder(strings.NewReader(in.Content))
	dec.UseNumber()
	if dec.Decode(&raw) != nil {
		writeError(w, r, fieldErr("content", errs.FieldInvalidFormat, "导入内容不是合法的数据格式"))
		return
	}
	rows := []map[string]any{}
	switch v := raw.(type) {
	case []any:
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				rows = append(rows, m)
			}
		}
	case map[string]any:
		rows = []map[string]any{v}
	default:
		writeError(w, r, fieldErr("content", errs.FieldInvalidFormat, "导入内容必须是一个对象或对象列表"))
		return
	}
	if len(rows) > 100 {
		writeError(w, r, fieldErr("content", errs.FieldOutOfRange, "单批最多导入 100 行"))
		return
	}
	masked := make([]map[string]any, 0, len(rows))
	valid := make([]map[string]any, 0, len(rows))
	id := newID()
	for i, m := range rows {
		account, _ := m["account"].(string)
		issues := []importIssue{}
		add := func(code, field string) { issues = append(issues, newImportIssue(code, field, "")) }
		password, passwordPresent := m["password"].(string)
		if _, present := m["password"]; present && !passwordPresent {
			issues = append(issues, newImportIssue("PASSWORD_INVALID", "password", "密码必须是文本"))
		}
		if passwordPresent && strings.TrimSpace(password) == "" {
			add("PASSWORD_REQUIRED", "password")
		}
		var auth map[string]any
		if value, present := m["auth_json"]; present {
			switch typed := value.(type) {
			case map[string]any:
				auth = typed
			case string:
				if json.Unmarshal([]byte(typed), &auth) != nil {
					add("AUTH_JSON_INVALID", "auth_json")
				}
			default:
				add("AUTH_JSON_INVALID", "auth_json")
			}
			if auth != nil {
				for _, p := range validateAuthFields(auth) {
					issues = append(issues, newImportIssue(errs.CodeCredentialsIncomplete, p.Field, p.Message))
				}
			}
		}
		if (passwordPresent || auth != nil) && a.keys == nil {
			add("ENCRYPTION_KEY_REQUIRED", "")
		}
		if strings.TrimSpace(account) == "" {
			add("ACCOUNT_REQUIRED", "account")
		} else if strings.ContainsAny(account, " \t\r\n") {
			add("ACCOUNT_WHITESPACE", "account")
		} else if validAccountIdentity(account) != nil {
			add("ACCOUNT_EMAIL_INVALID", "account")
		}
		if plan := str(m, "plan"); plan != "" && !validPlan(plan) {
			add("PLAN_INVALID", "plan")
		}
		if location := str(m, "register_location"); location != "" && location != supportedRegisterLocation {
			add("REGISTER_LOCATION_INVALID", "register_location")
		}
		for _, key := range []string{"register_time", "plan_start_time", "plan_end_time"} {
			if value := str(m, key); value != "" && !validDate(value) {
				add(strings.ToUpper(key)+"_INVALID", key)
			}
		}
		if start, end := str(m, "plan_start_time"), str(m, "plan_end_time"); start != "" && end != "" && validDate(start) && validDate(end) && start >= end {
			add("PLAN_DATE_RANGE_INVALID", "plan_end_time")
		}
		if registered, start := str(m, "register_time"), str(m, "plan_start_time"); registered != "" && start != "" && validDate(registered) && validDate(start) && registered > start {
			add("REGISTER_DATE_RANGE_INVALID", "register_time")
		}
		for _, p := range validOptionalMetadataFields(m) {
			issues = append(issues, newImportIssue("OPTIONAL_METADATA_INVALID", p.Field, p.Message))
		}
		masked = append(masked, map[string]any{"row_index": i + 1, "masked_identity": maskIdentity(account), "action": "create", "password_configured": passwordPresent, "auth_configured": auth != nil, "issues": issues})
		if len(issues) == 0 {
			row := map[string]any{}
			for _, key := range append([]string{"account", "plan", "register_location", "register_time", "plan_start_time", "plan_end_time", "proxy_id"}, optionalMetadataKeys...) {
				if value, ok := m[key]; ok {
					row[key] = value
				}
			}
			if passwordPresent {
				sealed, _ := a.keys.Seal([]byte(password), []byte(id+":password"))
				row["password_secret"] = sealed
			}
			if auth != nil {
				if e := applyCredentialInvariants(auth, nil); e != nil {
					writeError(w, r, e)
					return
				}
				encoded, _ := json.Marshal(auth)
				sealed, _ := a.keys.Seal(encoded, []byte(id+":credentials"))
				row["credentials_secret"] = sealed
			}
			valid = append(valid, row)
		}
	}
	b, _ := json.Marshal(valid)
	hash := sha256.Sum256([]byte(in.Content))
	nowt := now()
	expires := time.Now().Add(importPreviewTTL).UTC().Format(time.RFC3339)
	_, err := a.DB.ExecContext(r.Context(), `INSERT INTO imports(id,format,content_hash,rows_json,state,created_at,expires_at) VALUES(?,?,?,?,?,?,?)`, id, in.Format, hex.EncodeToString(hash[:]), string(b), "preview", nowt, expires)
	if err != nil {
		writeError(w, r, storageError(err, "无法保存导入预览"))
		return
	}
	write(w, 200, map[string]any{"import_id": id, "expires_at": expires, "rows": masked, "valid_count": len(valid)})
}
func maskIdentity(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 4 {
		return "****"
	}
	return s[:2] + "***" + s[len(s)-2:]
}

// importCommit writes the whole batch in one transaction: either every valid
// row is created or none is, so a mid-batch failure leaves nothing to clean up.
func (a *App) importCommit(w http.ResponseWriter, r *http.Request, id string) {
	var in struct {
		Strategy string `json:"strategy"`
	}
	if !decodeOptionalJSON(w, r, &in, 16<<10) {
		return
	}
	// Load and validate proxies before BeginTx: SQLite uses MaxOpenConns(1), so
	// requireProxyBindable (a.DB) would deadlock if called while a tx is held.
	var rows, expires, state string
	if err := a.DB.QueryRowContext(r.Context(), `SELECT rows_json,expires_at,state FROM imports WHERE id=?`, id).Scan(&rows, &expires, &state); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 404, "NOT_FOUND", "导入不存在")
			return
		}
		writeError(w, r, storageError(err, "导入失败"))
		return
	}
	if state != "preview" || time.Now().After(parseTime(expires)) {
		fail(w, 409, "IMPORT_EXPIRED", "导入预览已过期")
		return
	}
	var items []map[string]any
	if json.Unmarshal([]byte(rows), &items) != nil {
		writeError(w, r, storageError(errors.New("rows_json is not a JSON array"), "导入数据损坏"))
		return
	}
	seenProxy := map[string]bool{}
	for _, m := range items {
		if proxyID := str(m, "proxy_id"); proxyID != "" && !seenProxy[proxyID] {
			seenProxy[proxyID] = true
			if !a.requireProxyBindable(w, r, proxyID) {
				return
			}
		}
	}
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, r, storageError(err, "导入失败"))
		return
	}
	defer tx.Rollback()
	if err = tx.QueryRow(`SELECT rows_json,expires_at,state FROM imports WHERE id=?`, id).Scan(&rows, &expires, &state); err != nil {
		writeError(w, r, storageError(err, "导入失败"))
		return
	}
	if state != "preview" || time.Now().After(parseTime(expires)) {
		fail(w, 409, "IMPORT_EXPIRED", "导入预览已过期")
		return
	}
	if json.Unmarshal([]byte(rows), &items) != nil {
		writeError(w, r, storageError(errors.New("rows_json is not a JSON array"), "导入数据损坏"))
		return
	}
	reseal := func(encoded, aad, accountID string) (string, error) {
		plain, e := a.keys.Open(encoded, []byte(aad))
		if e != nil {
			return "", e
		}
		return a.keys.Seal(plain, []byte(accountID))
	}
	created, skipped := 0, 0
	for _, m := range items {
		account, _ := m["account"].(string)
		meta := pickMetadata(m)
		meta["source"] = "batch_import"
		metadataJSON, _ := json.Marshal(meta)
		accountID := newID()
		credentials := "{}"
		if encoded, ok := m["credentials_secret"].(string); ok && a.keys != nil {
			if credentials, err = reseal(encoded, id+":credentials", accountID); err != nil {
				writeError(w, r, secretError("import", "凭证导入数据无法解密", err))
				return
			}
		}
		var exists int
		if err = tx.QueryRow(`SELECT 1 FROM accounts WHERE account=?`, account).Scan(&exists); err == nil {
			if in.Strategy == "skip_existing" {
				skipped++
				continue
			}
			fail(w, 409, "ACCOUNT_EXISTS", "账号已存在："+maskIdentity(account))
			return
		} else if !errors.Is(err, sql.ErrNoRows) {
			writeError(w, r, storageError(err, "导入失败"))
			return
		}
		stamp := now()
		if _, err = tx.Exec(`INSERT INTO accounts(id,account,plan,register_location,register_time,plan_start_time,plan_end_time,proxy_id,metadata_json,credentials_json,created_at,updated_at,created_by_user_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, accountID, account, str(m, "plan"), str(m, "register_location"), str(m, "register_time"), str(m, "plan_start_time"), str(m, "plan_end_time"), str(m, "proxy_id"), string(metadataJSON), credentials, stamp, stamp, sessionUser(r).ID); err != nil {
			dbError(w, r, err, "ACCOUNT_EXISTS", "账号已存在", "导入失败")
			return
		}
		if encoded, ok := m["password_secret"].(string); ok && a.keys != nil {
			stored, e := reseal(encoded, id+":password", accountID)
			if e != nil {
				writeError(w, r, secretError("import", "密码导入数据无法解密", e))
				return
			}
			if _, e = tx.Exec(`INSERT INTO account_secrets(account_id,password_secret) VALUES(?,?)`, accountID, stored); e != nil {
				writeError(w, r, storageError(e, "密码保存失败"))
				return
			}
		}
		created++
	}
	if _, err = tx.Exec(`UPDATE imports SET state='committed' WHERE id=?`, id); err != nil {
		writeError(w, r, storageError(err, "导入失败"))
		return
	}
	if err = tx.Commit(); err != nil {
		writeError(w, r, storageError(err, "导入失败"))
		return
	}
	a.auditFor(r, "import.commit", "import", id, fmt.Sprintf("created=%d skipped=%d", created, skipped))
	write(w, 200, map[string]any{"import_id": id, "created": created, "skipped": skipped})
}
func parseTime(s string) time.Time { t, _ := time.Parse(time.RFC3339, s); return t }

const importPreviewTTL = 30 * time.Minute

func (a *App) proxyArchive(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !a.requireMutable(w, r, "proxies", id, "代理不存在") {
		return
	}
	res, err := a.DB.ExecContext(r.Context(), `UPDATE proxies SET enabled=0,version=version+1,updated_at=? WHERE id=?`, now(), id)
	if err != nil {
		writeError(w, r, storageError(err, "代理归档失败"))
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		fail(w, 404, "NOT_FOUND", "代理不存在")
		return
	}
	a.auditFor(r, "proxy.archive", "proxy", id, "")
	write(w, 200, map[string]any{"id": id, "archived": true})
}

func (a *App) proxyTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := a.requireVisible(w, r, "proxies", id, "代理不存在"); !ok {
		return
	}
	var enabled int
	if a.DB.QueryRow(`SELECT enabled FROM proxies WHERE id=?`, id).Scan(&enabled) != nil || enabled == 0 {
		fail(w, 404, "NOT_FOUND", "代理不存在")
		return
	}
	result, err := a.checkProxy(r.Context(), id)
	if err != nil {
		serverError(w, r, "PROXY_CHECK_FAILED", "代理检测设置或结果记录失败", err)
		return
	}
	a.auditFor(r, "proxy.test", "proxy", id, result.ErrorCode)
	write(w, 200, result)
}

func (a *App) proxyChecks(w http.ResponseWriter, r *http.Request, id string) {
	if _, ok := a.requireVisible(w, r, "proxies", id, "代理不存在"); !ok {
		return
	}
	rows, err := a.DB.QueryContext(r.Context(), `SELECT c.id,c.ok,c.latency_ms,COALESCE(c.exit_ip,''),COALESCE(c.error_code,''),c.checked_at,COALESCE(d.stage,''),COALESCE(d.message,''),COALESCE(d.route_mode,'unknown'),COALESCE(d.warning,'') FROM proxy_checks c LEFT JOIN proxy_check_details d ON d.check_id=c.id WHERE c.proxy_id=? ORDER BY c.checked_at DESC,c.rowid DESC LIMIT 100`, id)
	if err != nil {
		writeError(w, r, storageError(err, "读取检测记录失败"))
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var checkID string
		var result proxyProbeResult
		if err = rows.Scan(&checkID, &result.OK, &result.LatencyMS, &result.ExitIP, &result.ErrorCode, &result.CheckedAt, &result.Stage, &result.Message, &result.RouteMode, &result.Warning); err != nil {
			writeError(w, r, storageError(err, "读取检测记录失败"))
			return
		}
		if result.Message == "" && result.ErrorCode != "" {
			if def, ok := errs.Lookup(result.ErrorCode); ok {
				result.Message = def.Message
			}
		}
		out = append(out, map[string]any{"id": checkID, "ok": result.OK, "latency_ms": result.LatencyMS, "exit_ip": result.ExitIP, "error_code": result.ErrorCode, "checked_at": result.CheckedAt, "stage": result.Stage, "message": result.Message, "route_mode": result.RouteMode, "warning": result.Warning})
	}
	if err = rows.Err(); err != nil {
		writeError(w, r, storageError(err, "读取检测记录失败"))
		return
	}
	write(w, 200, out)
}

// proxyUpdate changes a proxy in place. Only the keys present in the body are
// applied; the password is replaced when non-empty and removed when
// clear_password is true. expected_version guards concurrent edits.
func (a *App) proxyUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !a.requireMutable(w, r, "proxies", id, "代理不存在") {
		return
	}
	var in map[string]any
	if !decodeJSON(w, r, &in, defaultBodyLimit) {
		return
	}
	var name, scheme, host, username, secret string
	var port, enabled, version int
	if err := a.DB.QueryRowContext(r.Context(), `SELECT name,scheme,host,port,username,password_secret,enabled,version FROM proxies WHERE id=?`, id).Scan(&name, &scheme, &host, &port, &username, &secret, &enabled, &version); err != nil {
		fail(w, http.StatusNotFound, "NOT_FOUND", "代理不存在")
		return
	}
	if expected, ok := in["expected_version"]; ok && intValue(expected) != version {
		fail(w, http.StatusConflict, "VERSION_CONFLICT", "代理已被修改，请刷新后重试")
		return
	}
	if _, ok := in["name"]; ok {
		name = str(in, "name")
	}
	if _, ok := in["scheme"]; ok {
		scheme = strings.ToLower(str(in, "scheme"))
	}
	if _, ok := in["host"]; ok {
		host = normalizeProxyHost(str(in, "host"))
	} else {
		host = normalizeProxyHost(host)
	}
	if _, ok := in["port"]; ok {
		port = intValue(in["port"])
	}
	if _, ok := in["username"]; ok {
		username = str(in, "username")
	}
	if v, ok := in["enabled"].(bool); ok {
		enabled = 0
		if v {
			enabled = 1
		}
	}
	if e := proxyFieldErrors(scheme, host, port); e != nil {
		writeError(w, r, e)
		return
	}
	if taken, err := a.proxyHostPortTaken(r.Context(), host, port, id); err != nil {
		writeError(w, r, storageError(err, "代理查重失败"))
		return
	} else if taken {
		writeError(w, r, errs.New(errs.CodeProxyExists))
		return
	}
	if clear, _ := in["clear_password"].(bool); clear {
		secret = ""
	}
	if password := str(in, "password"); password != "" {
		if a.keys == nil {
			fail(w, http.StatusServiceUnavailable, "ENCRYPTION_KEY_REQUIRED", "未配置凭证加密密钥")
			return
		}
		sealed, err := a.keys.Seal([]byte(password), []byte(id))
		if err != nil {
			writeError(w, r, secretError("proxy_password", "代理密码加密失败", err))
			return
		}
		secret = sealed
	}
	res, err := a.DB.ExecContext(r.Context(), `UPDATE proxies SET name=?,scheme=?,host=?,port=?,username=?,password_secret=?,enabled=?,version=version+1,updated_at=? WHERE id=? AND version=?`, name, scheme, host, port, username, secret, enabled, now(), id, version)
	if err != nil {
		dbError(w, r, err, "PROXY_EXISTS", "相同主机和端口的代理已存在", "代理保存失败")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		fail(w, http.StatusConflict, "VERSION_CONFLICT", "代理已被修改，请刷新后重试")
		return
	}
	a.auditFor(r, "proxy.update", "proxy", id, scheme+"://"+host+":"+strconv.Itoa(port))
	write(w, http.StatusOK, map[string]any{"id": id, "version": version + 1})
}

// proxyDelete removes a proxy and its check history. Proxies still bound to
// live accounts are refused so a delete never silently changes which exit an
// account submits through; archived accounts are unbound.
func (a *App) proxyDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !a.requireMutable(w, r, "proxies", id, "代理不存在") {
		return
	}
	ctx := r.Context()
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		writeError(w, r, storageError(err, "代理删除失败"))
		return
	}
	defer tx.Rollback()
	var bound int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM accounts WHERE proxy_id=? AND archived=0`, id).Scan(&bound); err != nil {
		writeError(w, r, storageError(err, "代理删除失败"))
		return
	}
	if bound > 0 {
		fail(w, http.StatusConflict, "PROXY_IN_USE", fmt.Sprintf("仍有 %d 个账号绑定该代理，请先在账号中解绑或归档账号", bound))
		return
	}
	res, err := tx.Exec(`DELETE FROM proxies WHERE id=?`, id)
	if err != nil {
		writeError(w, r, storageError(err, "代理删除失败"))
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		fail(w, http.StatusNotFound, "NOT_FOUND", "代理不存在")
		return
	}
	for _, stmt := range []string{
		`DELETE FROM proxy_check_details WHERE check_id IN (SELECT id FROM proxy_checks WHERE proxy_id=?)`,
		`DELETE FROM proxy_checks WHERE proxy_id=?`,
		`UPDATE accounts SET proxy_id='' WHERE proxy_id=?`,
	} {
		if _, err = tx.Exec(stmt, id); err != nil {
			writeError(w, r, storageError(err, "代理删除失败"))
			return
		}
	}
	if err = tx.Commit(); err != nil {
		writeError(w, r, storageError(err, "代理删除失败"))
		return
	}
	a.auditFor(r, "proxy.delete", "proxy", id, "")
	write(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}

// importIssue is one problem on one import row, with the registry message so
// the preview can show it without a client-side table.
type importIssue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

func newImportIssue(code, field, message string) importIssue {
	if message == "" {
		if def, ok := errs.Lookup(code); ok {
			message = def.Message
		} else {
			message = code
		}
	}
	return importIssue{Code: code, Message: message, Field: field}
}
