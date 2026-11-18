package app

import (
	"context"
	"net/http"
	"testing"
)

func TestCreateProxyRequiresSuccessfulProbe(t *testing.T) {
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "correct-horse-battery")
	t.Setenv("CC_MANAGER_ENCRYPTION_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	a, err := newApp(t.TempDir()+"/create-probe.db", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	a.probeCandidateProxy = func(context.Context, string) (proxyProbeResult, error) {
		return proxyProbeResult{
			OK: false, Stage: "target_proxy", ErrorCode: "TARGET_CONNECT_FAILED",
			Message: "无法直连目标代理；若代理需要境外来源 IP，请配置前置代理",
		}, nil
	}
	c := newTestClient(t, a)
	c.login("correct-horse-battery")
	assertEnvelope(t, c.do(http.MethodPost, "/api/v1/proxies", `{"name":"bad","scheme":"http","host":"127.0.0.1","port":1}`), http.StatusUnprocessableEntity, "PROXY_UNREACHABLE")
	var n int
	if err := a.DB.QueryRow(`SELECT COUNT(*) FROM proxies`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("failed create must not insert: count=%d err=%v", n, err)
	}
}

func TestCreateProxyRejectsDuplicateHostPort(t *testing.T) {
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "correct-horse-battery")
	t.Setenv("CC_MANAGER_ENCRYPTION_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	a, err := newApp(t.TempDir()+"/create-dup.db", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	c := newTestClient(t, a)
	c.login("correct-horse-battery")
	first := c.do(http.MethodPost, "/api/v1/proxies", `{"name":"one","scheme":"http","host":"Dup.Example.COM","port":9050}`)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first create=%d", first.StatusCode)
	}
	first.Body.Close()
	assertEnvelope(t, c.do(http.MethodPost, "/api/v1/proxies", `{"name":"two","scheme":"socks5","host":"dup.example.com","port":9050}`), http.StatusConflict, "PROXY_EXISTS")
	var n int
	if err := a.DB.QueryRow(`SELECT COUNT(*) FROM proxies WHERE host='dup.example.com' AND port=9050`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("expected one normalized row: count=%d err=%v", n, err)
	}
}

func TestCreateProxyRecordsSuccessfulProbe(t *testing.T) {
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "correct-horse-battery")
	t.Setenv("CC_MANAGER_ENCRYPTION_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	a, err := newApp(t.TempDir()+"/create-probe-ok.db", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	a.probeCandidateProxy = func(context.Context, string) (proxyProbeResult, error) {
		return proxyProbeResult{
			OK: true, Stage: "complete", Message: "本机检测通过", ExitIP: "203.0.113.9",
			LatencyMS: 12, RouteMode: "direct", CheckedAt: now(),
		}, nil
	}
	c := newTestClient(t, a)
	c.login("correct-horse-battery")
	res := c.do(http.MethodPost, "/api/v1/proxies", `{"name":"ok","scheme":"http","host":"proxy.example.test","port":8080}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create=%d", res.StatusCode)
	}
	var out struct {
		Data struct {
			ID          string
			LatestCheck *proxyProbeResult `json:"latest_check"`
		}
	}
	if err := jsonDecode(res, &out); err != nil || out.Data.ID == "" || out.Data.LatestCheck == nil || !out.Data.LatestCheck.OK {
		t.Fatalf("create body=%+v err=%v", out, err)
	}
	var ok int
	var exitIP string
	if err := a.DB.QueryRow(`SELECT ok,COALESCE(exit_ip,'') FROM proxy_checks WHERE proxy_id=?`, out.Data.ID).Scan(&ok, &exitIP); err != nil || ok != 1 || exitIP != "203.0.113.9" {
		t.Fatalf("stored check ok=%d ip=%q err=%v", ok, exitIP, err)
	}
}
