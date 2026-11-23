package app

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestOAuthStartEnforcesProxyBinding covers the binding semantics: accounts
// with an existing proxy binding reject a different proxy AND a downgrade to
// direct; unbound accounts pick freely and the pick is adopted on success.
func TestOAuthStartEnforcesProxyBinding(t *testing.T) {
	setup := func(t *testing.T) (*App, *testClient, string) {
		t.Helper()
		a, _, admin := routesFixture(t)
		a.probeOAuthReach = func(context.Context, *http.Client) error { return nil }
		if _, err := a.DB.Exec(`INSERT INTO proxies(id,scheme,host,port,enabled) VALUES('px-a','http','127.0.0.1',9001,1),('px-b','http','127.0.0.1',9002,1)`); err != nil {
			t.Fatal(err)
		}
		id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
		return a, admin, id
	}

	t.Run("bound px-a, start px-b -> 409", func(t *testing.T) {
		a, admin, id := setup(t)
		if _, err := a.DB.Exec(`UPDATE accounts SET proxy_id='px-a' WHERE id=?`, id); err != nil {
			t.Fatal(err)
		}
		res := admin.do("POST", "/api/v1/claude-oauth/sessions", `{"account_id":"`+id+`","proxy_id":"px-b"}`)
		defer res.Body.Close()
		if res.StatusCode != http.StatusConflict {
			t.Fatalf("status=%d body=%s", res.StatusCode, readBody(res))
		}
		if body := readBody(res); !strings.Contains(body, "PROXY_BINDING_CHANGED") {
			t.Fatalf("body=%s", body)
		}
	})

	t.Run("bound px-a, start px-a -> 201", func(t *testing.T) {
		a, admin, id := setup(t)
		if _, err := a.DB.Exec(`UPDATE accounts SET proxy_id='px-a' WHERE id=?`, id); err != nil {
			t.Fatal(err)
		}
		res := admin.do("POST", "/api/v1/claude-oauth/sessions", `{"account_id":"`+id+`","proxy_id":"px-a"}`)
		defer res.Body.Close()
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("status=%d body=%s", res.StatusCode, readBody(res))
		}
	})

	t.Run("bound px-a, start direct -> 409", func(t *testing.T) {
		a, admin, id := setup(t)
		if _, err := a.DB.Exec(`UPDATE accounts SET proxy_id='px-a' WHERE id=?`, id); err != nil {
			t.Fatal(err)
		}
		res := admin.do("POST", "/api/v1/claude-oauth/sessions", `{"account_id":"`+id+`"}`)
		defer res.Body.Close()
		if res.StatusCode != http.StatusConflict {
			t.Fatalf("status=%d body=%s", res.StatusCode, readBody(res))
		}
	})

	t.Run("unbound account adopts chosen proxy via bindAccountProxy", func(t *testing.T) {
		a, admin, id := setup(t)
		if _, err := a.DB.Exec(`UPDATE accounts SET proxy_id='' WHERE id=?`, id); err != nil {
			t.Fatal(err)
		}
		res := admin.do("POST", "/api/v1/claude-oauth/sessions", `{"account_id":"`+id+`","proxy_id":"px-a"}`)
		defer res.Body.Close()
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("status=%d body=%s", res.StatusCode, readBody(res))
		}
		if err := a.bindAccountProxy(id, "px-a"); err != nil {
			t.Fatal(err)
		}
		if got := queryString(t, a, `SELECT proxy_id FROM accounts WHERE id=?`, id); got != "px-a" {
			t.Fatalf("binding not adopted: %q", got)
		}
	})

	t.Run("unbound direct start stays allowed", func(t *testing.T) {
		a, admin, id := setup(t)
		if _, err := a.DB.Exec(`UPDATE accounts SET proxy_id='' WHERE id=?`, id); err != nil {
			t.Fatal(err)
		}
		sealAccountAuth(t, a, id, map[string]any{})
		res := admin.do("POST", "/api/v1/claude-oauth/sessions", `{"account_id":"`+id+`"}`)
		defer res.Body.Close()
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("status=%d body=%s", res.StatusCode, readBody(res))
		}
	})
}
