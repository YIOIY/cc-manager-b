package supplier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// WebClient reads the supplier's web console with a console account. It is
// read-only by construction: every method here issues GET requests except the
// two login calls. The session token lives in memory only and is renewed when
// it expires or the console answers 401.
type WebClient struct {
	base     *url.URL
	username string
	password string
	http     *http.Client

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

type WebSession struct {
	SupplierName string
	ExpiresIn    int
}

// WebProfile is the account data the console keeps in auth_config. The
// console masks the password, so it is never carried.
type WebProfile struct {
	Plan, PlanStart, PlanEnd, RegisterTime, RegisterLocation string
	Balance, PaymentMethod, RegisterDevice, RechargeTime     string
	WarrantyPeriod                                           string
	Tags                                                     []string
}

// WebUsage is the console's "账号用量" block. CostMicro is in millionths of a
// yuan; the percentages are fractions (0.07 means 7%).
type WebUsage struct {
	CostMicro, InputTokens, OutputTokens int64
	FiveHourUsed, WeekUsed               float64
	FiveHourResetAt, WeekResetAt         time.Time
}

type WebAccount struct {
	ID, AID, A2ID                                    int64
	Email, Alias, Provider, Model                    string
	ExtStatus, A1Status, A2Status, ProxyType, Remark string
	UsageLimitJSON, CreatedAt, UpdatedAt             string
	ModelsUsable, Locked                             bool
	Profile                                          WebProfile
	Usage                                            *WebUsage
	RawJSON                                          string
}

type WebCounts map[string]int

type WebReason struct{ Text, Raw string }

// WebBillRow is one (account, day, model) cost from /bill/query.
type WebBillRow struct {
	AccountID              int64
	Date, Model, BillModel string
	CostFen                int64
}

// WebHTTPError is a console response the client could not use.
type WebHTTPError struct {
	Status        int
	Summary       string
	RangeMismatch bool
	// Login marks a failure of the login exchange itself: anything the
	// console answers there other than "come back later" means the
	// credentials did not work, whatever the status code.
	Login bool
}

func (e *WebHTTPError) Error() string {
	return fmt.Sprintf("supplier web HTTP %d: %s", e.Status, e.Summary)
}

func (e *WebHTTPError) UpstreamCode() string {
	switch {
	case e.RangeMismatch:
		return "WEB_BILL_RANGE_MISMATCH"
	case e.Login && e.Status != 429 && e.Status < 500:
		return "WEB_LOGIN_FAILED"
	case e.Status == 401:
		return "WEB_LOGIN_FAILED"
	case e.Status == 403:
		return "WEB_FORBIDDEN"
	case e.Status == 404:
		return "WEB_ENDPOINT_MISSING"
	case e.Status == 429:
		return "WEB_RATE_LIMITED"
	case e.Status >= 500:
		return "PLATFORM_UNAVAILABLE"
	default:
		return "PLATFORM_RESPONSE_INVALID"
	}
}
func (e *WebHTTPError) UpstreamStatus() int     { return e.Status }
func (e *WebHTTPError) UpstreamSummary() string { return e.Summary }

const (
	webPageSize       = 100
	webMaxPages       = 100
	webBillBatchLimit = 50
	webTokenSlack     = time.Minute
)

func NewWebClient(baseURL, username, password string) (*WebClient, error) {
	if strings.TrimSpace(username) == "" || password == "" {
		return nil, errors.New("supplier web username and password are required")
	}
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, errors.New("supplier base URL must use HTTPS")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	transport, err := newSupplierTransport(u.Host)
	if err != nil {
		return nil, err
	}
	return &WebClient{base: u, username: strings.TrimSpace(username), password: password, http: &http.Client{Timeout: 30 * time.Second, Transport: transport}}, nil
}

// Login fetches the console's public key, encrypts the password and stores
// the session token. Safe to call concurrently; callers normally rely on the
// read methods to log in on demand.
func (c *WebClient) Login(ctx context.Context) (WebSession, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loginLocked(ctx)
}

func (c *WebClient) loginLocked(ctx context.Context) (WebSession, error) {
	c.token, c.expiresAt = "", time.Time{}
	data, status, err := c.send(ctx, http.MethodPost, "/supplier-account/api/supplier/pubkey", nil, map[string]string{"username": c.username}, "")
	if err != nil {
		return WebSession{}, err
	}
	if status != 200 {
		return WebSession{}, loginErr(data, status)
	}
	var pub struct {
		PublicKey string `json:"public_key"`
	}
	if json.Unmarshal(data, &pub) != nil || pub.PublicKey == "" {
		return WebSession{}, &WebHTTPError{Status: status, Summary: "public key missing", Login: true}
	}
	enc, err := EncryptLoginPassword(pub.PublicKey, c.password)
	if err != nil {
		return WebSession{}, err
	}
	data, status, err = c.send(ctx, http.MethodPost, "/supplier-account/api/supplier/login", nil, map[string]string{"username": c.username, "enc_password": enc}, "")
	if err != nil {
		return WebSession{}, err
	}
	if status != 200 {
		return WebSession{}, loginErr(data, status)
	}
	var login struct {
		Token     string `json:"token"`
		Name      string `json:"name"`
		ExpiresIn int    `json:"expires_in"`
	}
	if json.Unmarshal(data, &login) != nil || login.Token == "" {
		return WebSession{}, &WebHTTPError{Status: status, Summary: webSummary(data, "token missing"), Login: true}
	}
	ttl := time.Duration(login.ExpiresIn) * time.Second
	if ttl <= webTokenSlack {
		ttl = 5 * time.Hour
	}
	c.token, c.expiresAt = login.Token, time.Now().Add(ttl-webTokenSlack)
	return WebSession{SupplierName: login.Name, ExpiresIn: login.ExpiresIn}, nil
}

func (c *WebClient) ensureToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.expiresAt) {
		return c.token, nil
	}
	if _, err := c.loginLocked(ctx); err != nil {
		return "", err
	}
	return c.token, nil
}

// get performs one authenticated GET. A 401 is retried exactly once after a
// fresh login; a second 401 is reported as WEB_LOGIN_FAILED.
func (c *WebClient) get(ctx context.Context, path string, query url.Values) ([]byte, error) {
	token, err := c.ensureToken(ctx)
	if err != nil {
		return nil, err
	}
	data, status, err := c.send(ctx, http.MethodGet, path, query, nil, token)
	if err != nil {
		return nil, err
	}
	if status == 401 {
		c.mu.Lock()
		if c.token == token {
			_, err = c.loginLocked(ctx)
		}
		token = c.token
		c.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if data, status, err = c.send(ctx, http.MethodGet, path, query, nil, token); err != nil {
			return nil, err
		}
	}
	if status != 200 {
		return nil, webErr(data, status)
	}
	return data, nil
}

func (c *WebClient) send(ctx context.Context, method, path string, query url.Values, body any, token string) ([]byte, int, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		rd = bytes.NewReader(b)
	}
	u := *c.base
	u.Path = c.base.Path + path
	if query != nil {
		u.RawQuery = query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return nil, 0, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return data, resp.StatusCode, nil
}

func webSummary(data []byte, fallback string) string {
	var v struct {
		Detail string `json:"detail"`
		Msg    string `json:"msg"`
	}
	_ = json.Unmarshal(data, &v)
	if v.Detail != "" {
		return v.Detail
	}
	if v.Msg != "" {
		return v.Msg
	}
	return fallback
}

func webErr(data []byte, status int) error {
	return &WebHTTPError{Status: status, Summary: webSummary(data, http.StatusText(status))}
}

func loginErr(data []byte, status int) error {
	return &WebHTTPError{Status: status, Summary: webSummary(data, http.StatusText(status)), Login: true}
}

// ListAccounts pages through every account of the console's supplier and
// returns them with the status counts and the total the console reported.
func (c *WebClient) ListAccounts(ctx context.Context) ([]WebAccount, WebCounts, int, error) {
	var out []WebAccount
	counts := WebCounts{}
	total := 0
	for page := 1; page <= webMaxPages; page++ {
		q := url.Values{"status": {"all"}, "keyword": {""}, "page": {strconv.Itoa(page)}, "page_size": {strconv.Itoa(webPageSize)}, "provider": {""}, "model": {""}, "account_type": {""}}
		data, err := c.get(ctx, "/supplier-account/api/supplier/accounts", q)
		if err != nil {
			return nil, nil, 0, err
		}
		var body struct {
			Counts map[string]int    `json:"counts"`
			List   []json.RawMessage `json:"list"`
			Total  int               `json:"total"`
		}
		if err = json.Unmarshal(data, &body); err != nil {
			return nil, nil, 0, &WebHTTPError{Status: 200, Summary: "account list is not JSON"}
		}
		if page == 1 {
			for k, v := range body.Counts {
				counts[k] = v
			}
			total = body.Total
		}
		for _, raw := range body.List {
			a, err := parseWebAccount(raw)
			if err != nil {
				return nil, nil, 0, &WebHTTPError{Status: 200, Summary: "account entry invalid: " + err.Error()}
			}
			out = append(out, a)
		}
		if len(body.List) == 0 || len(out) >= total {
			break
		}
	}
	return out, counts, total, nil
}

var reasonKinds = map[string]string{"check-fail": "check-fail-reason", "claude-status": "claude-status-reason"}

// AccountReason reads one of the console's two explanation endpoints.
func (c *WebClient) AccountReason(ctx context.Context, id int64, kind string) (WebReason, error) {
	suffix, ok := reasonKinds[kind]
	if !ok || id <= 0 {
		return WebReason{}, errors.New("invalid reason kind or account id")
	}
	data, err := c.get(ctx, fmt.Sprintf("/supplier-account/api/supplier/accounts/%d/%s", id, suffix), nil)
	if err != nil {
		return WebReason{}, err
	}
	var any_ any
	if json.Unmarshal(data, &any_) != nil {
		return WebReason{Text: strings.TrimSpace(string(data)), Raw: string(data)}, nil
	}
	return WebReason{Text: reasonText(any_), Raw: string(data)}, nil
}

// reasonText picks the most explanation-like string in an unknown JSON body.
func reasonText(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		if s, ok := v.(string); ok {
			return s
		}
		return ""
	}
	for _, key := range []string{"reason", "message", "detail", "msg", "text"} {
		if s, ok := m[key].(string); ok && s != "" {
			return s
		}
	}
	for _, nested := range m {
		if inner, ok := nested.(map[string]any); ok {
			if s := reasonText(inner); s != "" {
				return s
			}
		}
	}
	for _, nested := range m {
		if s, ok := nested.(string); ok && s != "" {
			return s
		}
	}
	return ""
}

var dayPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// QueryModelBills returns per-day, per-model costs for up to 50 delivery ids
// between start and end (YYYY-MM-DD, inclusive). Rows outside the window are
// dropped; a range the console echoes back differently is a mismatch error.
func (c *WebClient) QueryModelBills(ctx context.Context, ids []int64, start, end string) ([]WebBillRow, error) {
	if len(ids) == 0 || len(ids) > webBillBatchLimit {
		return nil, fmt.Errorf("account ids must contain 1-%d items", webBillBatchLimit)
	}
	if !dayPattern.MatchString(start) || !dayPattern.MatchString(end) || start > end {
		return nil, errors.New("bill dates must be YYYY-MM-DD with start <= end")
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		if id <= 0 {
			return nil, errors.New("invalid account id")
		}
		parts[i] = strconv.FormatInt(id, 10)
	}
	compact := func(day string) string { return strings.ReplaceAll(day, "-", "") }
	q := url.Values{"account_ids": {strings.Join(parts, ",")}, "start_ds": {compact(start)}, "end_ds": {compact(end)}}
	data, err := c.get(ctx, "/supplier-account/api/bill/query", q)
	if err != nil {
		return nil, err
	}
	var body struct {
		List []struct {
			AccountID int64       `json:"account_id"`
			DS        json.Number `json:"ds"`
			Model     string      `json:"model_name"`
			BillModel string      `json:"bill_model_name"`
			Cost      json.Number `json:"cost"`
		} `json:"list"`
		Range struct {
			Start json.Number `json:"start_ds"`
			End   json.Number `json:"end_ds"`
		} `json:"range"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err = dec.Decode(&body); err != nil {
		return nil, &WebHTTPError{Status: 200, Summary: "bill response is not JSON"}
	}
	if strings.Trim(body.Range.Start.String(), `"`) != compact(start) || strings.Trim(body.Range.End.String(), `"`) != compact(end) {
		return nil, &WebHTTPError{Status: 200, Summary: "range mismatch", RangeMismatch: true}
	}
	rows := make([]WebBillRow, 0, len(body.List))
	for _, item := range body.List {
		ds := strings.Trim(item.DS.String(), `"`)
		ds = strings.ReplaceAll(ds, "-", "")
		if len(ds) != 8 || ds < compact(start) || ds > compact(end) {
			continue
		}
		fen, err := decimalFen(item.Cost.String())
		if err != nil {
			return nil, &WebHTTPError{Status: 200, Summary: "bill cost invalid"}
		}
		model := item.Model
		if model == "" {
			model = item.BillModel
		}
		rows = append(rows, WebBillRow{AccountID: item.AccountID, Date: ds[:4] + "-" + ds[4:6] + "-" + ds[6:], Model: model, BillModel: item.BillModel, CostFen: fen})
	}
	return rows, nil
}

// parseWebAccount decodes one list entry. auth_config and extra arrive as
// JSON strings; both are optional and never fatal when malformed. RawJSON
// keeps the entry for diagnostics minus every secret-bearing field.
func parseWebAccount(raw json.RawMessage) (WebAccount, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return WebAccount{}, err
	}
	id, ok := numberOf(m["id"])
	if !ok || id <= 0 {
		return WebAccount{}, errors.New("account id missing")
	}
	a := WebAccount{ID: id}
	a.AID, _ = numberOf(m["a1_id"])
	a.A2ID, _ = numberOf(m["a2_id"])
	a.Email = stringOf(m["project_id"])
	a.Alias = stringOf(m["account_alias"])
	a.Provider = stringOf(m["provider"])
	a.Model = stringOf(m["model"])
	a.ExtStatus = stringOf(m["ext_status"])
	a.A1Status = stringOf(m["a1_status"])
	a.A2Status = stringOf(m["a2_status"])
	a.ProxyType = stringOf(m["proxy_type"])
	a.Remark = stringOf(m["remark"])
	a.CreatedAt = stringOf(m["created_at"])
	a.UpdatedAt = stringOf(m["updated_at"])
	if locked, _ := numberOf(m["is_locked"]); locked != 0 {
		a.Locked = true
	}
	limit := map[string]any{}
	for _, k := range []string{"usage_limit_type", "usage_limit_period", "usage_limit_value"} {
		limit[k] = m[k]
	}
	if b, err := json.Marshal(limit); err == nil {
		a.UsageLimitJSON = string(b)
	}
	if models, ok := m["models"].([]any); ok {
		for _, entry := range models {
			if em, ok := entry.(map[string]any); ok && em["usable"] == true {
				a.ModelsUsable = true
			}
		}
	}
	if cfg := objectOf(m["auth_config"]); cfg != nil {
		a.Profile = WebProfile{
			Plan: stringOf(cfg["plan"]), PlanStart: stringOf(cfg["plan_start_time"]), PlanEnd: stringOf(cfg["plan_end_time"]),
			RegisterTime: stringOf(cfg["register_time"]), RegisterLocation: stringOf(cfg["register_location"]),
			Balance: stringOf(cfg["balance"]), PaymentMethod: stringOf(cfg["payment_method"]), RegisterDevice: stringOf(cfg["register_device"]),
			RechargeTime: stringOf(cfg["recharge_time"]), WarrantyPeriod: stringOf(cfg["warranty_period"]),
		}
		if tags, ok := cfg["tags"].([]any); ok {
			for _, tag := range tags {
				if s := stringOf(tag); s != "" {
					a.Profile.Tags = append(a.Profile.Tags, s)
				}
			}
		}
		if a.Email == "" {
			a.Email = stringOf(cfg["account"])
		}
		delete(cfg, "password")
		m["auth_config"] = cfg
	}
	if extra := objectOf(m["extra"]); extra != nil {
		if usage := objectOf(extra["cpa_usage"]); usage != nil {
			a.Usage = parseWebUsage(usage)
		}
	}
	for _, secret := range []string{"api_key", "api_secret", "account_md5"} {
		delete(m, secret)
	}
	if b, err := json.Marshal(m); err == nil {
		a.RawJSON = string(b)
	}
	return a, nil
}

func parseWebUsage(u map[string]any) *WebUsage {
	cost, hasCost := numberOf(u["cost"])
	in, hasIn := numberOf(u["input_tokens"])
	out, hasOut := numberOf(u["output_tokens"])
	util := objectOf(u["utilization"])
	if !hasCost && !hasIn && !hasOut && util == nil {
		return nil
	}
	w := &WebUsage{CostMicro: cost, InputTokens: in, OutputTokens: out}
	if util != nil {
		w.FiveHourUsed = floatOf(util["five_hour_used_percent"])
		w.WeekUsed = floatOf(util["week_used_percent"])
		if ts, ok := numberOf(util["five_hour_reset_at"]); ok && ts > 0 {
			w.FiveHourResetAt = time.Unix(ts, 0).UTC()
		}
		if ts, ok := numberOf(util["week_reset_at"]); ok && ts > 0 {
			w.WeekResetAt = time.Unix(ts, 0).UTC()
		}
	}
	return w
}

// objectOf accepts an object, or a string holding a JSON object; anything
// else (empty, garbage, wrong type) is nil.
func objectOf(v any) map[string]any {
	switch t := v.(type) {
	case map[string]any:
		return t
	case string:
		var m map[string]any
		if json.Unmarshal([]byte(t), &m) == nil {
			return m
		}
	}
	return nil
}

func stringOf(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	}
	return ""
}

func numberOf(v any) (int64, bool) {
	switch t := v.(type) {
	case float64:
		return int64(t), true
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return n, err == nil
	case json.Number:
		n, err := t.Int64()
		return n, err == nil
	}
	return 0, false
}

func floatOf(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f
	}
	return 0
}
