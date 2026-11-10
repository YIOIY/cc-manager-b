package supplier

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestCertificatePinsAreExactAndEndpointScoped(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	digest := sha256.Sum256(server.Certificate().Raw)
	for _, tc := range []struct {
		name, host, pin string
		wantOK          bool
	}{
		{"matching certificate", u.Host, hex.EncodeToString(digest[:]), true},
		{"wrong certificate", u.Host, strings.Repeat("0", 64), false},
		{"different endpoint", "other.example:8000", hex.EncodeToString(digest[:]), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CC_MANAGER_SUPPLIER_INSECURE_TLS", "")
			pins, _ := json.Marshal(map[string]string{tc.host: tc.pin})
			t.Setenv("CC_MANAGER_SUPPLIER_TLS_PINS", string(pins))
			client, err := New(server.URL, "test-token")
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = client.request(context.Background(), "GET", "/", nil)
			if (err == nil) != tc.wantOK {
				t.Fatalf("request error = %v, want success %v", err, tc.wantOK)
			}
		})
	}
}

func TestInvalidCertificatePinFailsClosed(t *testing.T) {
	for _, value := range []string{`invalid`, `{"example.com:443":"invalid"}`} {
		t.Setenv("CC_MANAGER_SUPPLIER_TLS_PINS", value)
		if _, err := New("https://example.com:443", "test-token"); err == nil {
			t.Fatal("invalid pin configuration was accepted")
		}
	}
}
