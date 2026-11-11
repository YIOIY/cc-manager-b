package supplier

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func proxyFor(t *testing.T, c *Client) *url.URL {
	t.Helper()
	tr, ok := c.http.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("unexpected transport %T", c.http.Transport)
	}
	if tr.Proxy == nil {
		return nil
	}
	req, _ := http.NewRequest(http.MethodGet, "https://supplier.example.com/x", nil)
	u, err := tr.Proxy(req)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestClientIgnoresEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("CC_MANAGER_SUPPLIER_PROXY", "")
	c, err := New("https://supplier.example.com", "token")
	if err != nil {
		t.Fatal(err)
	}
	if u := proxyFor(t, c); u != nil {
		t.Fatalf("supplier client must not inherit env proxy, got %v", u)
	}
}

func TestClientUsesExplicitSupplierProxy(t *testing.T) {
	t.Setenv("CC_MANAGER_SUPPLIER_PROXY", "http://10.0.0.9:3128")
	c, err := New("https://supplier.example.com", "token")
	if err != nil {
		t.Fatal(err)
	}
	if u := proxyFor(t, c); u == nil || u.Host != "10.0.0.9:3128" {
		t.Fatalf("explicit proxy not used: %v", u)
	}
	for _, bad := range []string{"ftp://10.0.0.9:21", "not a url", "http://"} {
		t.Setenv("CC_MANAGER_SUPPLIER_PROXY", bad)
		if _, err := New("https://supplier.example.com", "token"); err == nil {
			t.Fatalf("invalid proxy %q accepted", bad)
		}
	}
}

func TestQueryMetricsClassifiesHTTPErrors(t *testing.T) {
	t.Setenv("CC_MANAGER_SUPPLIER_INSECURE_TLS", "1")
	t.Setenv("CC_MANAGER_SUPPLIER_TLS_PINS", "")
	status := 404
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"detail":"账号不存在或无权访问"}`))
	}))
	defer server.Close()
	c, err := New(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, tc := range []struct {
		status int
		code   string
	}{{404, "PLATFORM_QUERY_DENIED"}, {403, "PLATFORM_AUTH_FAILED"}, {401, "PLATFORM_AUTH_FAILED"}, {503, "PLATFORM_UNAVAILABLE"}, {429, "PLATFORM_UNAVAILABLE"}, {400, "PLATFORM_RESPONSE_INVALID"}} {
		status = tc.status
		_, err := c.QueryMetrics(context.Background(), []string{"385753"}, now.Add(-time.Hour), now)
		var me *MetricsHTTPError
		if !errors.As(err, &me) || me.UpstreamCode() != tc.code || me.UpstreamStatus() != tc.status {
			t.Fatalf("status %d: err=%v", tc.status, err)
		}
		if !strings.Contains(err.Error(), "supplier metrics HTTP") {
			t.Fatalf("error text changed: %q", err.Error())
		}
	}
}
