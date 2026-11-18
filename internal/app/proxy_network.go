package app

import (
	"bufio"
	"cc-manager/internal/errs"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	xproxy "golang.org/x/net/proxy"
)

const proxySettingsAAD = "proxy-probe-upstream"

type proxySettings struct {
	Mode               string `json:"mode"`
	UpstreamURL        string `json:"upstream_url"`
	Username           string `json:"username"`
	PasswordConfigured bool   `json:"password_configured"`
	TimeoutSeconds     int    `json:"timeout_seconds"`
	Version            int    `json:"version"`
	secret             string
}

func (a *App) loadProxySettings() (proxySettings, error) {
	var s proxySettings
	err := a.DB.QueryRow(`SELECT mode,upstream_url,username,password_secret,timeout_seconds,version FROM proxy_probe_settings WHERE id=1`).Scan(&s.Mode, &s.UpstreamURL, &s.Username, &s.secret, &s.TimeoutSeconds, &s.Version)
	s.PasswordConfigured = s.secret != ""
	return s, err
}

func proxyEndpoint(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("代理地址必须为 协议://主机:端口，不能包含路径、查询参数或片段")
	}
	if !validProxyScheme(u.Scheme) {
		return nil, errors.New("代理协议仅支持 http、https、socks5、socks5h")
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, errors.New("代理端口无效")
		}
	}
	return u, nil
}

func (a *App) proxyNetworkSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		methodNotAllowed(w, http.MethodGet, http.MethodPut)
		return
	}
	s, err := a.loadProxySettings()
	if err != nil {
		writeError(w, r, storageError(err, "代理网络设置读取失败"))
		return
	}
	if r.Method == http.MethodGet {
		write(w, 200, s)
		return
	}
	var in struct {
		Mode           string `json:"mode"`
		UpstreamURL    string `json:"upstream_url"`
		Username       string `json:"username"`
		Password       string `json:"password"`
		ClearPassword  bool   `json:"clear_password"`
		TimeoutSeconds int    `json:"timeout_seconds"`
		Version        int    `json:"expected_version"`
	}
	if !decodeJSON(w, r, &in, 16<<10) {
		return
	}
	in.UpstreamURL = strings.TrimSpace(in.UpstreamURL)
	in.Username = strings.TrimSpace(in.Username)
	var problems errs.Fields
	if in.Mode != "direct" && in.Mode != "upstream" {
		problems.Add("mode", errs.FieldUnsupportedValue, "检测模式只能是直连或前置代理")
	}
	if in.TimeoutSeconds < 5 || in.TimeoutSeconds > 60 {
		problems.Add("timeout_seconds", errs.FieldOutOfRange, "检测超时须为 5–60 秒")
	}
	if in.UpstreamURL != "" || in.Mode == "upstream" {
		u, e := proxyEndpoint(in.UpstreamURL)
		if e != nil {
			problems.Invalid("upstream_url", e.Error())
		} else if u.User != nil {
			problems.Invalid("upstream_url", "请在独立的用户名和密码字段填写认证信息")
		}
	}
	if (in.Password != "" || (s.secret != "" && !in.ClearPassword)) && in.Username == "" {
		problems.Add("username", errs.FieldRequired, "设置认证密码时必须填写用户名；取消认证请同时清除密码")
	}
	if e := problems.Err(); e != nil {
		writeError(w, r, errs.New(errs.CodeProxySettingsInvalid).WithMessage(e.Message()).WithFields(e.Fields()...))
		return
	}
	secret := s.secret
	if in.ClearPassword {
		secret = ""
	}
	if in.Password != "" {
		if a.keys == nil {
			fail(w, 503, "ENCRYPTION_KEY_REQUIRED", "未配置凭证加密密钥")
			return
		}
		var e error
		if secret, e = a.keys.Seal([]byte(in.Password), []byte(proxySettingsAAD)); e != nil {
			writeError(w, r, secretError("proxy_password", "前置代理密码加密失败", e))
			return
		}
	}
	if in.Version != s.Version {
		fail(w, 409, "VERSION_CONFLICT", "设置已变化，请重新加载后保存")
		return
	}
	result, err := a.DB.ExecContext(r.Context(), `UPDATE proxy_probe_settings SET mode=?,upstream_url=?,username=?,password_secret=?,timeout_seconds=?,version=version+1 WHERE id=1 AND version=?`, in.Mode, in.UpstreamURL, in.Username, secret, in.TimeoutSeconds, in.Version)
	if err != nil {
		writeError(w, r, storageError(err, "代理网络设置保存失败"))
		return
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		fail(w, 409, "VERSION_CONFLICT", "设置已变化，请重新加载后保存")
		return
	}
	a.auditFor(r, "proxy.network.update", "proxy_settings", "1", in.Mode)
	s, err = a.loadProxySettings()
	if err != nil {
		writeError(w, r, storageError(err, "代理网络设置读取失败"))
		return
	}
	write(w, 200, s)
}

type proxyProbeError struct{ stage, code, message string }

func (e *proxyProbeError) Error() string             { return e.message }
func probeFailure(stage, code, message string) error { return &proxyProbeError{stage, code, message} }

func proxyAddress(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "1080"
		if u.Scheme == "http" {
			port = "80"
		}
		if u.Scheme == "https" {
			port = "443"
		}
	}
	return net.JoinHostPort(u.Hostname(), port)
}

// The transport still speaks the target proxy's protocol. Its TCP dial alone
// is tunneled through the upstream, so the two proxies and their credentials
// remain independent. No environment proxy is implicitly added to this chain.
func upstreamDialer(upstream *url.URL, tlsConfig *tls.Config) (func(context.Context, string, string) (net.Conn, error), error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if upstream.Scheme == "socks5" || upstream.Scheme == "socks5h" {
		d, err := xproxy.FromURL(upstream, dialer)
		if err != nil {
			return nil, probeFailure("settings", "SETTINGS_INVALID", "前置 SOCKS 代理设置无效")
		}
		cd, ok := d.(xproxy.ContextDialer)
		if !ok {
			return nil, probeFailure("settings", "SETTINGS_INVALID", "前置代理不支持超时控制")
		}
		return func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := cd.DialContext(ctx, network, addr)
			if err != nil {
				code, msg := "UPSTREAM_TUNNEL_FAILED", "前置 SOCKS 代理无法建立到目标代理的隧道，请检查前置服务、节点与访问规则"
				if strings.Contains(strings.ToLower(err.Error()), "authentication") {
					code, msg = "UPSTREAM_AUTH_FAILED", "前置 SOCKS 代理认证失败"
				}
				return nil, probeFailure("upstream", code, msg)
			}
			return conn, nil
		}, nil
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dialer.DialContext(ctx, "tcp", proxyAddress(upstream))
		if err != nil {
			return nil, probeFailure("upstream", "UPSTREAM_CONNECT_FAILED", "无法连接前置代理，请检查本机代理是否运行及端口是否正确")
		}
		success := false
		defer func() {
			if !success {
				conn.Close()
			}
		}()
		// Close a pending TLS/CONNECT handshake immediately on cancellation.
		rawConn := conn
		stop := context.AfterFunc(ctx, func() { rawConn.Close() })
		defer stop()
		if deadline, ok := ctx.Deadline(); ok {
			_ = conn.SetDeadline(deadline)
		}
		if upstream.Scheme == "https" {
			config := &tls.Config{MinVersion: tls.VersionTLS12}
			if tlsConfig != nil {
				config = tlsConfig.Clone()
			}
			config.ServerName = upstream.Hostname()
			tlsConn := tls.Client(conn, config)
			if err := tlsConn.HandshakeContext(ctx); err != nil {
				return nil, probeFailure("upstream", "UPSTREAM_TLS_FAILED", "前置 HTTPS 代理 TLS 握手失败，请检查证书和协议")
			}
			conn = tlsConn
		}
		req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: addr}, Host: addr, Header: make(http.Header)}
		if upstream.User != nil {
			password, _ := upstream.User.Password()
			req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(upstream.User.Username()+":"+password)))
		}
		if req.Write(conn) != nil {
			return nil, probeFailure("upstream", "UPSTREAM_TUNNEL_FAILED", "前置代理 CONNECT 请求发送失败")
		}
		reader := bufio.NewReader(conn)
		resp, err := http.ReadResponse(reader, req)
		if err != nil {
			return nil, probeFailure("upstream", "UPSTREAM_TUNNEL_FAILED", "前置代理 CONNECT 响应无效")
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			if resp.StatusCode == http.StatusProxyAuthRequired {
				return nil, probeFailure("upstream", "UPSTREAM_AUTH_FAILED", "前置代理认证失败")
			}
			return nil, probeFailure("upstream", "UPSTREAM_CONNECT_DENIED", "前置代理拒绝连接目标代理（HTTP "+strconv.Itoa(resp.StatusCode)+"）")
		}
		_ = conn.SetDeadline(time.Time{})
		if !stop() {
			return nil, probeFailure("upstream", "PROXY_TIMEOUT", "建立前置代理隧道超时或已取消")
		}
		success = true
		return &bufferedProxyConn{Conn: conn, reader: reader}, nil
	}, nil
}

type bufferedProxyConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedProxyConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

type proxyProbeResult struct {
	OK        bool   `json:"ok"`
	LatencyMS int64  `json:"latency_ms"`
	ExitIP    string `json:"exit_ip"`
	Stage     string `json:"stage"`
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
	RouteMode string `json:"route_mode"`
	Warning   string `json:"warning"`
	CheckedAt string `json:"checked_at"`
}

func runProxyProbe(ctx context.Context, raw string, s proxySettings, upstream *url.URL, targetURL, ipURL string, baseTransports ...*http.Transport) (result proxyProbeResult) {
	start := time.Now()
	result.RouteMode, result.CheckedAt = s.Mode, now()
	defer func() { result.LatencyMS = time.Since(start).Milliseconds() }()
	var phase atomic.Value
	phase.Store("target_proxy")
	failProbe := func(err error) {
		result.Stage, result.ErrorCode, result.Message = phase.Load().(string), "TARGET_REQUEST_FAILED", "经目标代理访问检测网站失败，请检查目标代理协议、认证和网络"
		var failure *proxyProbeError
		if errors.As(err, &failure) {
			result.Stage, result.ErrorCode, result.Message = failure.stage, failure.code, failure.message
		} else if strings.Contains(strings.ToLower(err.Error()), "authentication") {
			result.Stage, result.ErrorCode, result.Message = "target_proxy", "TARGET_AUTH_FAILED", "目标 SOCKS 代理认证失败"
		}
		if ctx.Err() != nil {
			result.ErrorCode, result.Message = "PROXY_TIMEOUT", "代理检测超时或已取消（阶段："+result.Stage+"）"
		}
	}
	u, err := proxyEndpoint(raw)
	if err != nil {
		failProbe(probeFailure("settings", "PROXY_INVALID", err.Error()))
		return
	}
	baseTransport := http.DefaultTransport.(*http.Transport)
	if len(baseTransports) > 0 {
		baseTransport = baseTransports[0]
	}
	transport := baseTransport.Clone()
	transport.Proxy = http.ProxyURL(u)
	transport.DisableKeepAlives = true
	transport.OnProxyConnectResponse = func(_ context.Context, _ *url.URL, _ *http.Request, r *http.Response) error {
		if r.StatusCode == 407 {
			return probeFailure("target_proxy", "TARGET_AUTH_FAILED", "目标代理认证失败")
		}
		if r.StatusCode != 200 {
			return probeFailure("target_proxy", "TARGET_CONNECT_DENIED", "目标代理拒绝 CONNECT（HTTP "+strconv.Itoa(r.StatusCode)+"）")
		}
		return nil
	}
	defer transport.CloseIdleConnections()
	if s.Mode == "upstream" {
		if upstream == nil {
			failProbe(probeFailure("settings", "SETTINGS_INVALID", "未配置前置代理"))
			return
		}
		transport.DialContext, err = upstreamDialer(upstream, transport.TLSClientConfig)
		if err != nil {
			failProbe(err)
			return
		}
	} else {
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, e := dialer.DialContext(ctx, network, addr)
			if e != nil {
				return nil, probeFailure("target_proxy", "TARGET_CONNECT_FAILED", "无法直连目标代理；若代理需要境外来源 IP，请配置前置代理")
			}
			return c, nil
		}
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		failProbe(err)
		return
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { phase.Store("target_request") }}))
	resp, err := client.Do(req)
	if err != nil {
		failProbe(err)
		return
	}
	_, bodyErr := io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	if bodyErr != nil {
		failProbe(probeFailure("target_request", "TARGET_RESPONSE_FAILED", "检测网站响应读取失败"))
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		failProbe(probeFailure("target_request", "TARGET_HTTP_ERROR", "检测网站返回 HTTP "+strconv.Itoa(resp.StatusCode)))
		return
	}
	result.OK, result.Stage, result.Message = true, "complete", "本机检测通过；供应商可用性以上号结果为准"
	result.Warning = "出口 IP 未获取；连通性检测已通过"
	ipCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ipReq, err := http.NewRequestWithContext(ipCtx, http.MethodGet, ipURL, nil)
	if err != nil {
		return
	}
	ipResp, err := client.Do(ipReq)
	if err != nil {
		return
	}
	defer ipResp.Body.Close()
	var echo struct {
		IP string `json:"ip"`
	}
	if ipResp.StatusCode == 200 && json.NewDecoder(io.LimitReader(ipResp.Body, 4096)).Decode(&echo) == nil && net.ParseIP(echo.IP) != nil {
		result.ExitIP, result.Warning = echo.IP, ""
	}
	return
}

func buildProxyURL(scheme, host string, port int, username, password string) (string, error) {
	u := &url.URL{Scheme: scheme, Host: net.JoinHostPort(host, strconv.Itoa(port))}
	if username != "" {
		u.User = url.UserPassword(username, password)
	}
	if _, err := proxyEndpoint(u.String()); err != nil {
		return "", err
	}
	return u.String(), nil
}

func (a *App) probeProxyCandidate(ctx context.Context, raw string) (proxyProbeResult, error) {
	if a.probeCandidateProxy != nil {
		return a.probeCandidateProxy(ctx, raw)
	}
	return a.probeProxyURL(ctx, raw)
}

func (a *App) probeProxyURL(ctx context.Context, raw string) (proxyProbeResult, error) {
	s, err := a.loadProxySettings()
	if err != nil {
		return proxyProbeResult{}, err
	}
	var upstream *url.URL
	if s.Mode == "upstream" {
		upstream, err = proxyEndpoint(s.UpstreamURL)
		if err != nil {
			return proxyProbeResult{}, errors.New("前置代理设置无效")
		}
		if s.Username != "" {
			password := ""
			if s.secret != "" {
				plain, e := a.openSecret(s.secret, proxySettingsAAD)
				if e != nil {
					return proxyProbeResult{}, errors.New("前置代理认证信息无法解密")
				}
				password = string(plain)
			}
			upstream.User = url.UserPassword(s.Username, password)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.TimeoutSeconds)*time.Second)
	defer cancel()
	return runProxyProbe(ctx, raw, s, upstream, "https://example.com/", "https://api.ipify.org?format=json"), nil
}

func recordProxyCheck(tx *sql.Tx, proxyID string, result proxyProbeResult) error {
	checkID := newID()
	if _, err := tx.Exec(`INSERT INTO proxy_checks(id,proxy_id,ok,latency_ms,exit_ip,error_code,checked_at) VALUES(?,?,?,?,?,?,?)`, checkID, proxyID, boolInt(result.OK), result.LatencyMS, result.ExitIP, result.ErrorCode, result.CheckedAt); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO proxy_check_details(check_id,stage,message,route_mode,warning) VALUES(?,?,?,?,?)`, checkID, result.Stage, result.Message, result.RouteMode, result.Warning)
	return err
}

func (a *App) checkProxy(ctx context.Context, id string) (proxyProbeResult, error) {
	raw, err := a.resolveProxyURL(id, "")
	if err != nil {
		return proxyProbeResult{}, err
	}
	result, err := a.probeProxyURL(ctx, raw)
	if err != nil {
		return result, err
	}
	tx, err := a.DB.BeginTx(context.WithoutCancel(ctx), nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if err = recordProxyCheck(tx, id, result); err != nil {
		return result, err
	}
	return result, tx.Commit()
}
