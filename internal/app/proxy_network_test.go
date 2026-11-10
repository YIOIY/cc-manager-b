package app

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func fixturePipe(a, b net.Conn) {
	done := make(chan struct{})
	go func() { _, _ = io.Copy(b, a); b.Close(); close(done) }()
	_, _ = io.Copy(a, b)
	a.Close()
	b.Close()
	<-done
}

func fixtureHTTPProxy(t *testing.T, secure bool, auth string, status int) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	calls := &atomic.Int64{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if auth != "" && r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte(auth)) {
			w.WriteHeader(407)
			return
		}
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		if r.Method != "CONNECT" {
			http.Error(w, "CONNECT required", 405)
			return
		}
		up, err := net.DialTimeout("tcp", r.Host, time.Second)
		if err != nil {
			http.Error(w, "unreachable", 502)
			return
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			up.Close()
			return
		}
		_, _ = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = rw.Flush()
		fixturePipe(&bufferedProxyConn{Conn: conn, reader: rw.Reader}, up)
	})
	var server *httptest.Server
	if secure {
		server = httptest.NewTLSServer(handler)
	} else {
		server = httptest.NewServer(handler)
	}
	t.Cleanup(server.Close)
	return server, calls
}

func fixtureSOCKSProxy(t *testing.T, user, password string) (string, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	calls, domains := &atomic.Int64{}, &atomic.Int64{}
	var wg sync.WaitGroup
	var active sync.Map
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, e := ln.Accept()
			if e != nil {
				return
			}
			active.Store(conn, true)
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer active.Delete(conn)
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				r := bufio.NewReader(conn)
				greeting := make([]byte, 2)
				if _, e := io.ReadFull(r, greeting); e != nil || greeting[0] != 5 {
					return
				}
				methods := make([]byte, int(greeting[1]))
				if _, e := io.ReadFull(r, methods); e != nil {
					return
				}
				if user != "" {
					_, _ = conn.Write([]byte{5, 2})
					header := make([]byte, 2)
					if _, e := io.ReadFull(r, header); e != nil {
						return
					}
					u := make([]byte, int(header[1]))
					if _, e := io.ReadFull(r, u); e != nil {
						return
					}
					n, e := r.ReadByte()
					if e != nil {
						return
					}
					p := make([]byte, int(n))
					if _, e = io.ReadFull(r, p); e != nil {
						return
					}
					if string(u) != user || string(p) != password {
						_, _ = conn.Write([]byte{1, 1})
						return
					}
					_, _ = conn.Write([]byte{1, 0})
				} else {
					_, _ = conn.Write([]byte{5, 0})
				}
				header := make([]byte, 4)
				if _, e := io.ReadFull(r, header); e != nil || header[1] != 1 {
					return
				}
				var host string
				switch header[3] {
				case 1:
					ip := make([]byte, 4)
					if _, e := io.ReadFull(r, ip); e != nil {
						return
					}
					host = net.IP(ip).String()
				case 4:
					ip := make([]byte, 16)
					if _, e := io.ReadFull(r, ip); e != nil {
						return
					}
					host = net.IP(ip).String()
				case 3:
					domains.Add(1)
					n, e := r.ReadByte()
					if e != nil {
						return
					}
					name := make([]byte, int(n))
					if _, e = io.ReadFull(r, name); e != nil {
						return
					}
					host = string(name)
				default:
					return
				}
				port := make([]byte, 2)
				if _, e := io.ReadFull(r, port); e != nil {
					return
				}
				calls.Add(1)
				up, e := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port)))), time.Second)
				if e != nil {
					_, _ = conn.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
					return
				}
				_, _ = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
				_ = conn.SetDeadline(time.Time{})
				fixturePipe(&bufferedProxyConn{Conn: conn, reader: r}, up)
			}()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		active.Range(func(key, value any) bool { key.(net.Conn).Close(); return true })
		wg.Wait()
	})
	return "socks5://" + ln.Addr().String(), calls, domains
}

func probeFixtureOrigin(t *testing.T) (*httptest.Server, *http.Transport) {
	t.Helper()
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy credentials leaked to origin")
		}
		switch r.URL.Path {
		case "/ip":
			w.Write([]byte(`{"ip":"203.0.113.42"}`))
		case "/deny":
			w.WriteHeader(403)
		case "/redirect":
			http.Redirect(w, r, "/", 302)
		case "/bad-ip":
			w.Write([]byte(`{"ip":"not-an-ip"}`))
		default:
			w.Write([]byte("ok"))
		}
	}))
	t.Cleanup(origin.Close)
	pool := x509.NewCertPool()
	pool.AddCert(origin.Certificate())
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	return origin, transport
}

func TestProxyProtocolAndChainMatrix(t *testing.T) {
	for _, targetScheme := range []string{"http", "https", "socks5", "socks5h"} {
		for _, route := range []string{"direct", "http", "https", "socks5", "socks5h"} {
			t.Run(targetScheme+"_via_"+route, func(t *testing.T) {
				origin, base := probeFixtureOrigin(t)
				var target string
				var targetCalls *atomic.Int64
				if strings.HasPrefix(targetScheme, "socks") {
					raw, calls, _ := fixtureSOCKSProxy(t, "target-user", "target-pass")
					targetCalls = calls
					target = strings.Replace(raw, "socks5://", targetScheme+"://", 1)
				} else {
					server, calls := fixtureHTTPProxy(t, targetScheme == "https", "target-user:target-pass", 0)
					targetCalls = calls
					target = server.URL
					if targetScheme == "https" {
						base.TLSClientConfig.RootCAs.AddCert(server.Certificate())
					}
				}
				u, _ := url.Parse(target)
				u.User = url.UserPassword("target-user", "target-pass")
				target = u.String()
				s := proxySettings{Mode: "direct", TimeoutSeconds: 3}
				var upstream *url.URL
				var upstreamCalls *atomic.Int64
				if route != "direct" {
					s.Mode = "upstream"
					var raw string
					if strings.HasPrefix(route, "socks") {
						proxy, calls, _ := fixtureSOCKSProxy(t, "front-user", "front-pass")
						raw = strings.Replace(proxy, "socks5://", route+"://", 1)
						upstreamCalls = calls
					} else {
						server, calls := fixtureHTTPProxy(t, route == "https", "front-user:front-pass", 0)
						raw = server.URL
						upstreamCalls = calls
						if route == "https" {
							base.TLSClientConfig.RootCAs.AddCert(server.Certificate())
						}
					}
					upstream, _ = url.Parse(raw)
					upstream.User = url.UserPassword("front-user", "front-pass")
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				result := runProxyProbe(ctx, target, s, upstream, origin.URL, origin.URL+"/ip", base)
				if !result.OK || result.ExitIP != "203.0.113.42" || result.Warning != "" {
					t.Fatalf("probe failed: %+v", result)
				}
				if targetCalls.Load() != 2 {
					t.Fatalf("target hop calls=%d, want 2", targetCalls.Load())
				}
				if upstreamCalls != nil && upstreamCalls.Load() != 2 {
					t.Fatalf("upstream hop bypassed: %d", upstreamCalls.Load())
				}
			})
		}
	}
}

func TestSOCKSRemoteDNS(t *testing.T) {
	origin, base := probeFixtureOrigin(t)
	raw, _, domains := fixtureSOCKSProxy(t, "", "")
	targetURL := strings.Replace(origin.URL, "127.0.0.1", "localhost", 1)
	base.TLSClientConfig.ServerName = "127.0.0.1"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := runProxyProbe(ctx, raw, proxySettings{Mode: "direct"}, nil, targetURL, targetURL+"/ip", base)
	if !result.OK || domains.Load() != 2 {
		t.Fatalf("remote DNS not used: %+v domains=%d", result, domains.Load())
	}
}

func TestProxyFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		name, scheme, route, code, stage, path string
		status                                 int
	}{
		{name: "target_auth", scheme: "http", code: "TARGET_AUTH_FAILED", stage: "target_proxy", status: 407},
		{name: "target_denied", scheme: "http", code: "TARGET_CONNECT_DENIED", stage: "target_proxy", status: 403},
		{name: "upstream_auth", route: "http", code: "UPSTREAM_AUTH_FAILED", stage: "upstream", status: 407},
		{name: "upstream_denied", route: "http", code: "UPSTREAM_CONNECT_DENIED", stage: "upstream", status: 403},
		{name: "website_403", scheme: "http", code: "TARGET_HTTP_ERROR", stage: "target_request", path: "/deny"},
		{name: "redirect_is_not_success", scheme: "http", code: "TARGET_HTTP_ERROR", stage: "target_request", path: "/redirect"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			origin, base := probeFixtureOrigin(t)
			target, _ := fixtureHTTPProxy(t, false, "", func() int {
				if tc.route != "" {
					return 0
				}
				return tc.status
			}())
			s := proxySettings{Mode: "direct"}
			var upstream *url.URL
			if tc.route != "" {
				front, _ := fixtureHTTPProxy(t, false, "", tc.status)
				upstream, _ = url.Parse(front.URL)
				s.Mode = "upstream"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			result := runProxyProbe(ctx, target.URL, s, upstream, origin.URL+tc.path, origin.URL+"/ip", base)
			if result.OK || result.ErrorCode != tc.code || result.Stage != tc.stage {
				t.Fatalf("classification: %+v", result)
			}
		})
	}
	for _, route := range []bool{false, true} {
		t.Run("socks_auth_"+strconv.FormatBool(route), func(t *testing.T) {
			origin, base := probeFixtureOrigin(t)
			raw, _, _ := fixtureSOCKSProxy(t, "user", "password")
			u, _ := url.Parse(raw)
			u.User = url.UserPassword("user", "wrong-secret")
			target := u.String()
			var front *url.URL
			s := proxySettings{Mode: "direct"}
			code := "TARGET_AUTH_FAILED"
			if route {
				front = u
				s.Mode = "upstream"
				target = raw
				code = "UPSTREAM_AUTH_FAILED"
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result := runProxyProbe(ctx, target, s, front, origin.URL, origin.URL+"/ip", base)
			if result.OK || result.ErrorCode != code || strings.Contains(result.Message, "wrong-secret") {
				t.Fatalf("auth classification: %+v", result)
			}
		})
	}
}

func TestProxyIPFailureDoesNotUndoConnectivity(t *testing.T) {
	origin, base := probeFixtureOrigin(t)
	target, _ := fixtureHTTPProxy(t, false, "", 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := runProxyProbe(ctx, target.URL, proxySettings{Mode: "direct"}, nil, origin.URL, origin.URL+"/bad-ip", base)
	if !result.OK || result.ExitIP != "" || result.Warning == "" {
		t.Fatalf("IP warning: %+v", result)
	}
}

func TestProxyTimeoutAndCancellation(t *testing.T) {
	for _, mode := range []string{"direct", "upstream"} {
		t.Run(mode, func(t *testing.T) {
			stall := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
			defer stall.Close()
			var front *url.URL
			target := stall.URL
			if mode == "upstream" {
				front, _ = url.Parse(stall.URL)
				target = "http://127.0.0.1:1"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
			defer cancel()
			start := time.Now()
			result := runProxyProbe(ctx, target, proxySettings{Mode: mode}, front, "https://example.test/", "https://example.test/ip")
			if result.OK || result.ErrorCode != "PROXY_TIMEOUT" || time.Since(start) > time.Second {
				t.Fatalf("timeout: %+v", result)
			}
		})
	}
}

func TestProxySettingsSecretsVersionAndValidation(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	if err := migrate(a.DB); err != nil {
		t.Fatal(err)
	}
	call := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		a.proxyNetworkSettings(w, httptest.NewRequest("PUT", "/proxy-settings", strings.NewReader(body)))
		return w
	}
	w := call(`{"mode":"upstream","upstream_url":"http://127.0.0.1:26800","username":"front","password":"do-not-echo","timeout_seconds":20,"expected_version":1}`)
	if w.Code != 200 || strings.Contains(w.Body.String(), "do-not-echo") || !strings.Contains(w.Body.String(), `"password_configured":true`) {
		t.Fatal(w.Body.String())
	}
	s, err := a.loadProxySettings()
	if err != nil || strings.Contains(s.secret, "do-not-echo") {
		t.Fatalf("secret storage: %v", err)
	}
	plain, err := a.openSecret(s.secret, proxySettingsAAD)
	if err != nil || string(plain) != "do-not-echo" {
		t.Fatal("password did not roundtrip")
	}
	w = call(`{"mode":"direct","upstream_url":"http://127.0.0.1:26800","username":"front","timeout_seconds":30,"expected_version":2}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	s, _ = a.loadProxySettings()
	if !s.PasswordConfigured {
		t.Fatal("blank password erased credentials")
	}
	w = call(`{"mode":"direct","timeout_seconds":20,"clear_password":true,"expected_version":3}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	s, _ = a.loadProxySettings()
	if s.PasswordConfigured {
		t.Fatal("clear password failed")
	}
	w = call(`{"mode":"direct","timeout_seconds":20,"expected_version":3}`)
	if w.Code != 409 {
		t.Fatal("stale setting not rejected")
	}
	for _, raw := range []string{"ftp://localhost:20", "http://user:secret@localhost:80", "http://localhost:99999", "http://localhost/path", "http://localhost?token=x"} {
		b, _ := json.Marshal(map[string]any{"mode": "upstream", "upstream_url": raw, "timeout_seconds": 20, "expected_version": 4})
		if w = call(string(b)); w.Code != 422 {
			t.Fatalf("accepted invalid setting %s: %s", raw, w.Body.String())
		}
	}
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/proxy-settings", nil))
	if w.Code != 401 {
		t.Fatalf("unauthenticated settings access: %d", w.Code)
	}
}

func TestLocalProxyFailureDoesNotBlockRemoteOnboarding(t *testing.T) {
	a, calls, _, _ := deliveryFixture(t, func(body map[string]any) {
		auth, ok := body["auth_json"].(map[string]any)
		if !ok || auth["proxy_url"] != "http://127.0.0.1:1" {
			t.Error("supplier received an incorrect target proxy")
		}
	})
	_, err := a.DB.Exec(`INSERT INTO proxies(id,name,scheme,host,port,username,enabled) VALUES('unreachable','Fixture','http','127.0.0.1',1,'',1)`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.checkProxy(context.Background(), "unreachable")
	if err != nil || result.OK {
		t.Fatalf("expected local failure: %+v %v", result, err)
	}
	if _, err = a.DB.Exec(`UPDATE proxy_probe_settings SET mode='upstream',upstream_url='http://127.0.0.1:26800'`); err != nil {
		t.Fatal(err)
	}
	body := deliveryBody()
	body["proxy_id"] = "unreachable"
	w := onboardRequest(a, body, "local-failure-is-advisory")
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	id := responseData(t, w)["account_id"].(string)
	credential, err := a.loadCredential(id)
	if err != nil || credential["proxy_url"] != "http://127.0.0.1:1" {
		t.Fatal("real target proxy was not preserved")
	}
	a.runOne(context.Background())
	if calls.Load() != 1 || deliveryAccount(t, a)["onboarding_state"] != "linked" {
		t.Fatal("local detection still blocks supplier submission")
	}
	w = httptest.NewRecorder()
	a.proxyChecks(w, httptest.NewRequest("GET", "/proxies/unreachable/checks", nil), "unreachable")
	if !strings.Contains(w.Body.String(), "TARGET_CONNECT_FAILED") || !strings.Contains(w.Body.String(), `"route_mode":"direct"`) {
		t.Fatal(w.Body.String())
	}
	w = httptest.NewRecorder()
	a.proxies(w, httptest.NewRequest("GET", "/proxies", nil))
	if !strings.Contains(w.Body.String(), `"latest_check"`) || !strings.Contains(w.Body.String(), "TARGET_CONNECT_FAILED") {
		t.Fatal(w.Body.String())
	}
	body = deliveryBody()
	body["account"] = "disabled@example.test"
	body["proxy_id"] = "missing"
	w = onboardRequest(a, body, "missing-proxy")
	if w.Code != 422 {
		t.Fatal("missing pool entry should block")
	}
}
