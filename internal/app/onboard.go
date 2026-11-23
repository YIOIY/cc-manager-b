package app

import (
	"cc-manager/internal/errs"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"cc-manager/internal/supplier"
)

// onboardInput is the decoded, validated input of one onboarding call.
type onboardInput struct {
	in              map[string]any
	original        []byte // raw body, hashed for the idempotency key
	existingID      string
	existingVersion int
	account, plan   string
	password        string
	auth            map[string]any
	connectionID    string
	proxyID         string
}

// onboardAccount atomically saves credentials and queues delivery. Platform
// acceptance is independent of later metrics and bill synchronization. The
// steps run in order and the first *apiError ends the request.
func (a *App) onboardAccount(w http.ResponseWriter, r *http.Request, existingIDs ...string) {
	if a.keys == nil {
		fail(w, 503, "ENCRYPTION_KEY_REQUIRED", "未配置凭证加密密钥")
		return
	}
	var in map[string]any
	if !decodeJSON(w, r, &in, defaultBodyLimit) {
		return
	}
	req := &onboardInput{in: in}
	req.original, _ = json.Marshal(in)
	if len(existingIDs) > 0 {
		req.existingID = existingIDs[0]
	}
	if req.existingID != "" {
		unlock, err := a.lockAccountWrite(r.Context(), req.existingID)
		if err != nil {
			writeError(w, r, errs.New(errs.CodePlatformUnavailable).WithCause(err))
			return
		}
		defer unlock()
	}
	for _, step := range []func(*onboardInput) *apiError{a.fillOnboardDefaults, a.prepareOnboardProxy, validateOnboardInput, a.checkOnboardConnection} {
		if e := step(req); e != nil {
			writeError(w, r, e)
			return
		}
	}
	if req.existingID != "" {
		if !a.requireMutable(w, r, "accounts", req.existingID, "账号不存在") {
			return
		}
	}
	if !a.requireProxyBindable(w, r, req.proxyID) {
		return
	}
	var saved map[string]any
	if req.existingID != "" {
		saved, _ = a.loadCredential(req.existingID)
		if e := a.loadDeviceAnchor(r.Context(), req.existingID, req.auth, saved); e != nil {
			writeError(w, r, storageError(e, "设备标识读取失败"))
			return
		}
	}
	if e := applyCredentialInvariants(req.auth, saved); e != nil {
		writeError(w, r, e)
		return
	}
	resp, replayed, err := a.persistOnboard(r, req)
	if err != nil {
		respondError(w, r, err)
		return
	}
	if !replayed {
		a.auditFor(r, "account.onboard", "account", resp["account_id"].(string), "job="+resp["job_id"].(string))
	}
	write(w, 202, resp)
}

// Resolve a selected pool proxy before checking required credential fields.
// Its password stays server-side and need not be copied into the browser form.
func (a *App) prepareOnboardProxy(req *onboardInput) *apiError {
	req.proxyID = str(req.in, "proxy_id")
	if req.proxyID == "" {
		return nil
	}
	auth, err := decodeAuthInput(req.in["auth_json"])
	if err != nil {
		return apiFail(422, "VALIDATION_FAILED", err.Error())
	}
	req.auth = auth
	if e := a.applyPoolProxy(req); e != nil {
		return e
	}
	req.in["auth_json"] = req.auth
	return nil
}

// fillOnboardDefaults completes a re-onboard of an existing account with the
// stored profile, credential and password so callers only send changes.
func (a *App) fillOnboardDefaults(req *onboardInput) *apiError {
	if req.existingID == "" {
		return nil
	}
	in := req.in
	var oldAccount, plan, location, registered, start, end, proxy string
	if a.DB.QueryRow(`SELECT account,COALESCE(plan,''),COALESCE(register_location,''),COALESCE(register_time,''),COALESCE(plan_start_time,''),COALESCE(plan_end_time,''),COALESCE(proxy_id,''),version FROM accounts WHERE id=?`, req.existingID).Scan(&oldAccount, &plan, &location, &registered, &start, &end, &proxy, &req.existingVersion) != nil {
		return apiFail(404, "NOT_FOUND", "账号不存在")
	}
	if value := str(in, "account"); value != "" && value != oldAccount {
		return errs.New(errs.CodeValidationFailed).WithFields(errs.FieldError{Field: "account", Code: errs.FieldMismatch, Message: "账号身份不能修改"})
	}
	for k, v := range map[string]string{"account": oldAccount, "plan": plan, "register_location": location, "register_time": registered, "plan_start_time": start, "plan_end_time": end, "proxy_id": proxy} {
		if _, ok := in[k]; !ok {
			in[k] = v
		}
	}
	if in["auth_json"] == nil {
		auth, e := a.loadCredential(req.existingID)
		if e != nil {
			return apiFail(422, "CREDENTIALS_REQUIRED", "请补充 Claude 凭证")
		}
		in["auth_json"] = auth
	}
	if str(in, "password") == "" {
		var encoded string
		if a.DB.QueryRow(`SELECT password_secret FROM account_secrets WHERE account_id=?`, req.existingID).Scan(&encoded) != nil {
			return apiFail(422, "PASSWORD_REQUIRED", "请补充账号密码")
		}
		plain, e := a.openSecret(encoded, req.existingID)
		if e != nil {
			return apiFail(422, "PASSWORD_REQUIRED", "请重新填写账号密码")
		}
		in["password"] = string(plain)
	}
	return nil
}

// validateOnboardInput checks the profile, password and credential fields,
// collecting every problem so one submit shows all of them.
func validateOnboardInput(req *onboardInput) *apiError {
	in := req.in
	req.account, req.plan = str(in, "account"), str(in, "plan")
	var f errs.Fields
	for _, p := range validateAccountFields(in, true) {
		f.Add(p.Field, p.Code, p.Message)
	}
	req.password = str(in, "password")
	if req.password == "" {
		f.Required("password")
	}
	auth, err := decodeAuthInput(in["auth_json"])
	if err != nil {
		f.Invalid("auth_json", err.Error())
	} else {
		for _, p := range validateAuthFields(auth) {
			f.Add(p.Field, p.Code, p.Message)
		}
		if proxy, ok := auth["proxy_url"].(string); ok {
			if normalized, err := normalizeProxyURL(proxy); err == nil {
				auth["proxy_url"] = normalized
			}
		}
		req.auth = auth
	}
	req.connectionID = str(in, "platform_connection_id")
	req.proxyID = str(in, "proxy_id")
	if req.connectionID == "" {
		f.Required("platform_connection_id")
	}
	if e := f.Err(); e != nil {
		return e
	}
	return nil
}

// checkOnboardConnection proves the platform connection is enabled and its
// token still decrypts before anything is written.
func (a *App) checkOnboardConnection(req *onboardInput) *apiError {
	var base, tokenSecret string
	var enabled int
	if err := a.DB.QueryRow(`SELECT base_url,token_secret,enabled FROM platform_connections WHERE id=?`, req.connectionID).Scan(&base, &tokenSecret, &enabled); err != nil || enabled == 0 {
		return apiFail(422, "CONNECTION_DISABLED", "平台连接不存在或已禁用")
	}
	decodedToken, err := a.openSecret(tokenSecret, req.connectionID)
	if err != nil {
		return secretError("connection_token", "平台令牌无法解密", err)
	}
	if _, err = supplier.New(base, string(decodedToken)); err != nil {
		return apiFail(422, "CONNECTION_INVALID", "平台连接无效")
	}
	return nil
}

// applyPoolProxy makes a selected pool entry the authoritative proxy. The
// supplier derives its remote proxy identity from auth_json.proxy_url;
// leaving the imported URL untouched can silently submit a different,
// already-occupied proxy.
func (a *App) applyPoolProxy(req *onboardInput) *apiError {
	if req.proxyID == "" {
		return nil
	}
	proxyURL, err := a.resolveProxyURL(req.proxyID, "")
	if err != nil {
		return apiFail(422, "PROXY_INVALID", err.Error())
	}
	req.auth["proxy_url"] = proxyURL
	return nil
}

// persistOnboard seals the secrets and writes account, password, job and
// delivery in one transaction. replayed is true when an Idempotency-Key
// matched an earlier submission and its result was returned instead.
func (a *App) persistOnboard(r *http.Request, req *onboardInput) (resp map[string]any, replayed bool, err error) {
	accountID, jobID, stamp := newID(), newID(), now()
	if req.existingID != "" {
		accountID = req.existingID
	}
	sealedPassword, err := a.keys.Seal([]byte(req.password), []byte(accountID))
	if err != nil {
		return nil, false, secretError("account_password", "账号密码加密失败", err)
	}
	authJSON, _ := json.Marshal(req.auth)
	sealedAuth, err := a.keys.Seal(authJSON, []byte(accountID))
	if err != nil {
		return nil, false, secretError("account_credentials", "凭证加密失败", err)
	}
	payloadHash := fmt.Sprintf("%x", sha256.Sum256(append([]byte(req.existingID+":"), req.original...)))
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return nil, false, dbStep(err, "账号保存失败")
	}
	defer tx.Rollback()
	if key != "" {
		if resp, found, e := replayOnboard(tx, key, payloadHash); found {
			return resp, true, e
		}
	}
	version, err := a.writeOnboardAccount(tx, req, accountID, stamp, sealedAuth, sessionUser(r).ID)
	if err != nil {
		return nil, false, err
	}
	if _, err = tx.Exec(`INSERT INTO account_secrets(account_id,password_secret) VALUES(?,?) ON CONFLICT(account_id) DO UPDATE SET password_secret=excluded.password_secret`, accountID, sealedPassword); err != nil {
		return nil, false, dbStep(err, "密码保存失败")
	}
	payload, _ := json.Marshal(submitJob{ConnectionID: req.connectionID, Version: version})
	if _, err = tx.Exec(`INSERT INTO jobs(id,kind,state,account_id,connection_id,payload_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, jobID, "account.submit", "queued", accountID, req.connectionID, string(payload), stamp, stamp); err != nil {
		return nil, false, dbStep(err, "上号任务保存失败")
	}
	if _, err = tx.Exec(`INSERT INTO deliveries(id,account_id,connection_id,ingestion_state,created_at,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(account_id,connection_id) DO UPDATE SET ingestion_state='queued',updated_at=excluded.updated_at`, newID(), accountID, req.connectionID, "queued", stamp, stamp); err != nil {
		return nil, false, dbStep(err, "交付记录保存失败")
	}
	if key != "" {
		if _, err = tx.Exec(`INSERT INTO idempotency_keys(key,payload_hash,job_id,created_at) VALUES(?,?,?,?)`, key, payloadHash, jobID, stamp); err != nil {
			return nil, false, dbStepConflict(err, "IDEMPOTENCY_CONFLICT", "请求已提交，请刷新查看任务", "请求记录保存失败")
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, false, dbStep(err, "账号保存失败")
	}
	return map[string]any{"account_id": accountID, "job_id": jobID, "state": "queued"}, false, nil
}

// replayOnboard returns the job an Idempotency-Key was first used for. A
// different payload under the same key is a conflict.
func replayOnboard(tx *sql.Tx, key, payloadHash string) (map[string]any, bool, error) {
	var oldHash, oldJob, oldAccount, state string
	if tx.QueryRow(`SELECT i.payload_hash,i.job_id,j.account_id,j.state FROM idempotency_keys i JOIN jobs j ON j.id=i.job_id WHERE i.key=?`, key).Scan(&oldHash, &oldJob, &oldAccount, &state) != nil {
		return nil, false, nil
	}
	if oldHash != payloadHash {
		return nil, true, apiFail(409, "IDEMPOTENCY_CONFLICT", "该请求已用于其他账号资料，请刷新后重试")
	}
	return map[string]any{"account_id": oldAccount, "job_id": oldJob, "state": state}, true, nil
}

// writeOnboardAccount inserts a new account or bumps an existing one after
// the delivery, archive and version guards; it returns the new version.
func (a *App) writeOnboardAccount(tx *sql.Tx, req *onboardInput, accountID, stamp, sealedAuth, createdByUserID string) (int, error) {
	in := req.in
	metadataJSON, _ := json.Marshal(pickMetadata(in))
	if req.existingID == "" {
		var exists int
		_ = tx.QueryRow(`SELECT 1 FROM accounts WHERE account=?`, req.account).Scan(&exists)
		if exists == 1 {
			return 0, apiFail(409, "ACCOUNT_EXISTS", "账号已存在，请在账号详情查看上号状态")
		}
		if _, err := tx.Exec(`INSERT INTO accounts(id,account,plan,register_location,register_time,plan_start_time,plan_end_time,proxy_id,metadata_json,credentials_json,created_at,updated_at,created_by_user_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, accountID, req.account, req.plan, str(in, "register_location"), str(in, "register_time"), str(in, "plan_start_time"), str(in, "plan_end_time"), req.proxyID, string(metadataJSON), sealedAuth, stamp, stamp, createdByUserID); err != nil {
			return 0, dbStepConflict(err, "ACCOUNT_EXISTS", "账号已存在，请在账号详情查看上号状态", "账号保存失败")
		}
		return 1, nil
	}
	if code, message, e := deliveryBlocked(tx, req.existingID, req.connectionID); e != nil {
		return 0, dbStep(e, "上号状态读取失败")
	} else if code != "" {
		return 0, apiFail(409, code, message)
	}
	var archived, current int
	if tx.QueryRow(`SELECT archived,version FROM accounts WHERE id=?`, req.existingID).Scan(&archived, &current) != nil || archived != 0 {
		return 0, apiFail(409, "ARCHIVED", "账号不存在或已归档")
	}
	if current != req.existingVersion {
		return 0, apiFail(409, "VERSION_CONFLICT", "账号资料已更新，请刷新后重试")
	}
	version := current + 1
	if _, err := tx.Exec(`UPDATE accounts SET plan=?,register_location=?,register_time=?,plan_start_time=?,plan_end_time=?,proxy_id=?,metadata_json=?,credentials_json=?,version=?,updated_at=? WHERE id=?`, req.plan, str(in, "register_location"), str(in, "register_time"), str(in, "plan_start_time"), str(in, "plan_end_time"), req.proxyID, string(metadataJSON), sealedAuth, version, stamp, accountID); err != nil {
		return 0, dbStepConflict(err, "ACCOUNT_EXISTS", "账号已存在，请在账号详情查看上号状态", "账号保存失败")
	}
	return version, nil
}

func normalizeProxyURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", errors.New("proxy_url 无效")
	}
	if !validProxyScheme(u.Scheme) {
		return "", errors.New("proxy_url 协议无效")
	}
	if u.User != nil && u.User.Username() == "" {
		return "", errors.New("proxy_url 用户名无效")
	}
	return u.String(), nil
}

func decodeAuthInput(value any) (map[string]any, error) {
	var auth map[string]any
	switch v := value.(type) {
	case map[string]any:
		auth = v
	case string:
		if json.Unmarshal([]byte(v), &auth) != nil {
			return nil, errors.New("auth_json 字符串不是合法 JSON")
		}
	default:
		return nil, errors.New("auth_json 必须是对象")
	}
	if auth == nil {
		return nil, errors.New("auth_json 必须是对象")
	}
	return auth, nil
}

// openSecret decrypts a stored secret bound to aad with whichever configured key sealed it.
func (a *App) openSecret(encoded, aad string) ([]byte, error) {
	if a.keys == nil {
		return nil, errors.New("encryption key unavailable")
	}
	return a.keys.Open(encoded, []byte(aad))
}

func (a *App) resolveProxyURL(id, fallback string) (string, error) {
	if id == "" {
		return fallback, nil
	}
	var scheme, host, username, secret string
	var port int
	if err := a.DB.QueryRow(`SELECT scheme,host,port,COALESCE(username,''),COALESCE(password_secret,'') FROM proxies WHERE id=? AND enabled=1`, id).Scan(&scheme, &host, &port, &username, &secret); err != nil {
		return "", errors.New("代理不存在或已禁用")
	}
	password := ""
	if secret != "" {
		plain, err := a.openSecret(secret, id)
		if err != nil {
			return "", errors.New("代理认证信息无法解密")
		}
		password = string(plain)
	}
	u := &url.URL{Scheme: scheme, Host: net.JoinHostPort(host, fmt.Sprint(port))}
	if username != "" {
		u.User = url.UserPassword(username, password)
	}
	if _, err := proxyEndpoint(u.String()); err != nil {
		return "", err
	}
	return u.String(), nil
}
