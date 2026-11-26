package app

import (
	"net/http"
	"testing"
)

func TestLookupProxiesOmitsBound(t *testing.T) {
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "correct-horse-battery")
	t.Setenv("CC_MANAGER_ENCRYPTION_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	a, err := newApp(t.TempDir()+"/lookup-proxies.db", false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	admin := newTestClient(t, a)
	admin.login("correct-horse-battery")

	free := createOwnedProxy(t, admin, "free")
	bound := createOwnedProxy(t, admin, "bound")
	acc := createOwnedAccount(t, admin, "bound@example.test", bound)

	assertListIDs(t, admin.do(http.MethodGet, "/api/v1/lookups/proxies", ""), []string{free})
	assertListIDs(t, admin.do(http.MethodGet, "/api/v1/lookups/proxies?account_id="+acc, ""), []string{free, bound})

	if _, err := a.DB.Exec(`UPDATE accounts SET archived=1 WHERE id=?`, acc); err != nil {
		t.Fatal(err)
	}
	assertListIDs(t, admin.do(http.MethodGet, "/api/v1/lookups/proxies", ""), []string{free, bound})
}
