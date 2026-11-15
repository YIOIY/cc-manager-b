package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func (c *testClient) loginAs(username, password string) *http.Response {
	c.t.Helper()
	res := c.do(http.MethodPost, "/api/v1/auth/login", `{"username":"`+username+`","password":"`+password+`"}`)
	if res.StatusCode == http.StatusOK {
		var env struct {
			Data struct {
				CSRF string `json:"csrf_token"`
			} `json:"data"`
		}
		_ = json.NewDecoder(res.Body).Decode(&env)
		c.csrf = env.Data.CSRF
	}
	res.Body.Close()
	return res
}

type sessionEnvelope struct {
	Data struct {
		User struct {
			ID          string   `json:"id"`
			IsAdmin     bool     `json:"is_admin"`
			Permissions []string `json:"permissions"`
			Roles       []struct{ ID, Name string }
		} `json:"user"`
	} `json:"data"`
}

func rbacApp(t *testing.T) (*App, *testClient) {
	t.Helper()
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "bootstrap-admin-pass")
	a, err := newApp(t.TempDir()+"/rbac.db", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	admin := newTestClient(t, a)
	if res := admin.loginAs("admin", "bootstrap-admin-pass"); res.StatusCode != http.StatusOK {
		t.Fatalf("bootstrap admin login=%d", res.StatusCode)
	}
	return a, admin
}

func TestBuiltinRolesAndFineGrainedChecks(t *testing.T) {
	a, admin := rbacApp(t)
	var who sessionEnvelope
	_ = jsonDecode(admin.do(http.MethodGet, "/api/v1/auth/session", ""), &who)
	if !who.Data.User.IsAdmin || len(who.Data.User.Permissions) != len(allPermissionKeys()) || who.Data.User.Roles[0].ID != roleAdminID {
		t.Fatalf("admin session=%+v", who.Data.User)
	}
	for _, body := range []string{
		`{"username":"ops","password":"operator-password","role_ids":["` + roleOperatorID + `"]}`,
		`{"username":"ro","password":"viewer-password-1","role_ids":["` + roleViewerID + `"]}`,
	} {
		if res := admin.do(http.MethodPost, "/api/v1/users", body); res.StatusCode != http.StatusCreated {
			t.Fatalf("create user status=%d", res.StatusCode)
		}
	}
	if res := admin.do(http.MethodPost, "/api/v1/users", `{"username":"noroles","password":"operator-password","role_ids":[]}`); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("user without roles status=%d", res.StatusCode)
	}

	viewer := newTestClient(t, a)
	viewer.loginAs("ro", "viewer-password-1")
	for _, path := range []string{"/api/v1/dashboard", "/api/v1/accounts", "/api/v1/proxies", "/api/v1/jobs", "/api/v1/audit-events", "/api/v1/platform-connections"} {
		if res := viewer.do(http.MethodGet, path, ""); res.StatusCode != http.StatusOK {
			t.Fatalf("viewer GET %s=%d", path, res.StatusCode)
		}
	}
	for _, call := range []string{"POST /api/v1/accounts", "POST /api/v1/proxies", "GET /api/v1/users", "GET /api/v1/roles", "POST /api/v1/platform-usage/bills/sync"} {
		m, p, _ := strings.Cut(call, " ")
		res := viewer.do(m, p, `{}`)
		var body struct {
			Error struct{ Code string }
		}
		_ = jsonDecode(res, &body)
		if res.StatusCode != http.StatusForbidden || body.Error.Code != "PERMISSION_DENIED" {
			t.Fatalf("viewer %s=%d %s", call, res.StatusCode, body.Error.Code)
		}
	}

	operator := newTestClient(t, a)
	operator.loginAs("ops", "operator-password")
	if res := operator.do(http.MethodPost, "/api/v1/proxies", `{"scheme":"http","host":"127.0.0.1","port":1080}`); res.StatusCode != http.StatusCreated {
		t.Fatalf("operator create proxy=%d", res.StatusCode)
	}
	if res := operator.do(http.MethodPost, "/api/v1/platform-connections", `{"name":"x","base_url":"https://x.example","token":"t"}`); res.StatusCode != http.StatusForbidden {
		t.Fatalf("operator connection create=%d", res.StatusCode)
	}
	if res := operator.do(http.MethodGet, "/api/v1/users", ""); res.StatusCode != http.StatusForbidden {
		t.Fatalf("operator users=%d", res.StatusCode)
	}
	var actor string
	_ = a.DB.QueryRow(`SELECT actor FROM audit_events WHERE action='proxy.create' ORDER BY id DESC LIMIT 1`).Scan(&actor)
	if actor != "ops" {
		t.Fatalf("audit actor=%q", actor)
	}
}

func TestCustomRolesComposeAndApplyImmediately(t *testing.T) {
	a, admin := rbacApp(t)
	var catalogue []permissionModule
	_ = jsonDecode(admin.do(http.MethodGet, "/api/v1/permissions", ""), &struct{ Data *[]permissionModule }{&catalogue})
	if len(catalogue) != len(permissionCatalogue) {
		t.Fatalf("catalogue modules=%d", len(catalogue))
	}
	var role struct {
		Data struct {
			ID      string
			Version int
		}
	}
	// a custom role: can only test proxies and look at jobs
	if res := admin.do(http.MethodPost, "/api/v1/roles", `{"name":"网络巡检","description":"只做代理检测","permissions":["proxies.view","proxies.test","jobs.view","bogus.perm"]}`); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unknown permission accepted: %d", res.StatusCode)
	}
	_ = jsonDecode(admin.do(http.MethodPost, "/api/v1/roles", `{"name":"网络巡检","description":"只做代理检测","permissions":["proxies.view","proxies.test","jobs.view"]}`), &role)
	if role.Data.ID == "" {
		t.Fatal("custom role not created")
	}
	if res := admin.do(http.MethodPost, "/api/v1/roles", `{"name":"网络巡检","permissions":[]}`); res.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate role name=%d", res.StatusCode)
	}
	// a second custom role granting user administration but nothing else
	var userAdmin struct{ Data struct{ ID string } }
	_ = jsonDecode(admin.do(http.MethodPost, "/api/v1/roles", `{"name":"账号管理员","permissions":["users.view","users.create","users.update","roles.view","roles.manage"]}`), &userAdmin)
	if res := admin.do(http.MethodPost, "/api/v1/users", `{"username":"tech","password":"technician-pass-1","role_ids":["`+role.Data.ID+`","`+userAdmin.Data.ID+`"]}`); res.StatusCode != http.StatusCreated {
		t.Fatalf("create multi-role user=%d", res.StatusCode)
	}
	tech := newTestClient(t, a)
	tech.loginAs("tech", "technician-pass-1")
	var who sessionEnvelope
	_ = jsonDecode(tech.do(http.MethodGet, "/api/v1/auth/session", ""), &who)
	perms := strings.Join(who.Data.User.Permissions, ",")
	if who.Data.User.IsAdmin || !strings.Contains(perms, "proxies.test") || !strings.Contains(perms, "users.create") || strings.Contains(perms, "accounts.view") || len(who.Data.User.Roles) != 2 {
		t.Fatalf("union of roles wrong: %+v", who.Data.User)
	}
	if res := tech.do(http.MethodGet, "/api/v1/proxies", ""); res.StatusCode != http.StatusOK {
		t.Fatalf("tech proxies=%d", res.StatusCode)
	}
	if res := tech.do(http.MethodPost, "/api/v1/proxies", `{"scheme":"http","host":"127.0.0.1","port":1}`); res.StatusCode != http.StatusForbidden {
		t.Fatalf("tech proxies.create granted: %d", res.StatusCode)
	}
	if res := tech.do(http.MethodGet, "/api/v1/accounts", ""); res.StatusCode != http.StatusForbidden {
		t.Fatalf("tech accounts.view granted: %d", res.StatusCode)
	}
	// delegated administration cannot escalate: no admin role, no permissions beyond its own
	if res := tech.do(http.MethodPost, "/api/v1/users", `{"username":"evil","password":"escalation-pass-1","role_ids":["`+roleAdminID+`"]}`); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("admin role granted by non-admin: %d", res.StatusCode)
	}
	if res := tech.do(http.MethodPost, "/api/v1/roles", `{"name":"escalate","permissions":["accounts.credentials"]}`); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("permission escalation via role: %d", res.StatusCode)
	}
	if res := tech.do(http.MethodPatch, "/api/v1/users/"+who.Data.User.ID, `{"password":"reset-attempt-pass-1"}`); res.StatusCode != http.StatusForbidden {
		t.Fatalf("password reset without users.reset_password: %d", res.StatusCode)
	}
	// editing the role applies on the next request without re-login
	if res := admin.do(http.MethodPatch, "/api/v1/roles/"+role.Data.ID, `{"permissions":["proxies.view","jobs.view"],"expected_version":1}`); res.StatusCode != http.StatusOK {
		t.Fatalf("role update=%d", res.StatusCode)
	}
	if res := tech.do(http.MethodPost, "/api/v1/proxies/x/test", ""); res.StatusCode != http.StatusForbidden {
		t.Fatalf("revoked permission still honoured: %d", res.StatusCode)
	}
	// builtin roles are protected; bound roles cannot be deleted
	if res := admin.do(http.MethodDelete, "/api/v1/roles/"+roleViewerID, ""); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("builtin role deleted: %d", res.StatusCode)
	}
	if res := admin.do(http.MethodPatch, "/api/v1/roles/"+roleAdminID, `{"permissions":["jobs.view"]}`); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("admin role permissions changed: %d", res.StatusCode)
	}
	if res := admin.do(http.MethodDelete, "/api/v1/roles/"+role.Data.ID, ""); res.StatusCode != http.StatusConflict {
		t.Fatalf("bound role deleted: %d", res.StatusCode)
	}
}

func TestUserLifecycleRevokesSessionsAndProtectsLastAdmin(t *testing.T) {
	a, admin := rbacApp(t)
	var me sessionEnvelope
	_ = jsonDecode(admin.do(http.MethodGet, "/api/v1/auth/session", ""), &me)
	for _, body := range []string{`{"role_ids":["` + roleViewerID + `"]}`, `{"enabled":false}`} {
		if res := admin.do(http.MethodPatch, "/api/v1/users/"+me.Data.User.ID, body); res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("self change %s=%d", body, res.StatusCode)
		}
	}
	if res := admin.do(http.MethodDelete, "/api/v1/users/"+me.Data.User.ID, ""); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("self delete=%d", res.StatusCode)
	}
	var created struct {
		Data struct {
			ID      string
			Version int
		}
	}
	_ = jsonDecode(admin.do(http.MethodPost, "/api/v1/users", `{"username":"ops","password":"operator-password","role_ids":["`+roleOperatorID+`"]}`), &created)
	ops := newTestClient(t, a)
	if res := ops.loginAs("ops", "operator-password"); res.StatusCode != http.StatusOK {
		t.Fatal("operator login failed")
	}
	if res := admin.do(http.MethodPatch, "/api/v1/users/"+created.Data.ID, `{"enabled":false,"expected_version":1}`); res.StatusCode != http.StatusOK {
		t.Fatalf("disable=%d", res.StatusCode)
	}
	if res := ops.do(http.MethodGet, "/api/v1/dashboard", ""); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("disabled user still served: %d", res.StatusCode)
	}
	ops.refreshSession()
	if res := ops.loginAs("ops", "operator-password"); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("disabled user could log in: %d", res.StatusCode)
	}
	if res := admin.do(http.MethodPatch, "/api/v1/users/"+created.Data.ID, `{"enabled":true,"expected_version":1}`); res.StatusCode != http.StatusConflict {
		t.Fatalf("stale version=%d", res.StatusCode)
	}
	if res := admin.do(http.MethodPost, "/api/v1/auth/password", `{"current_password":"wrong","new_password":"another-long-password"}`); res.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong current password=%d", res.StatusCode)
	}
	if res := admin.do(http.MethodPost, "/api/v1/auth/password", `{"current_password":"bootstrap-admin-pass","new_password":"another-long-password"}`); res.StatusCode != http.StatusOK {
		t.Fatalf("password change=%d", res.StatusCode)
	}
	if res := admin.do(http.MethodGet, "/api/v1/dashboard", ""); res.StatusCode != http.StatusOK {
		t.Fatal("current session dropped after password change")
	}
	again := newTestClient(t, a)
	if res := again.loginAs("admin", "bootstrap-admin-pass"); res.StatusCode != http.StatusUnauthorized {
		t.Fatal("old password still accepted")
	}
	if res := again.loginAs("admin", "another-long-password"); res.StatusCode != http.StatusOK {
		t.Fatal("new password rejected")
	}
	if err := a.ResetPassword("admin", "reset-by-cli-pass"); err != nil {
		t.Fatal(err)
	}
	if res := again.do(http.MethodGet, "/api/v1/dashboard", ""); res.StatusCode != http.StatusUnauthorized {
		t.Fatal("reset did not revoke sessions")
	}
	again.refreshSession()
	if res := again.loginAs("admin", "reset-by-cli-pass"); res.StatusCode != http.StatusOK {
		t.Fatal("reset password rejected")
	}
}

func TestLegacyFixedRolesMigrateToBindings(t *testing.T) {
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "bootstrap-admin-pass")
	path := t.TempDir() + "/legacy-roles.db"
	a, err := newApp(path, false)
	if err != nil {
		t.Fatal(err)
	}
	// simulate a database written by migration 4 only: the fixed role column
	// exists again and migrations 5 and 7 have not run
	if _, err = a.DB.Exec(`ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT ''; DELETE FROM user_roles; DELETE FROM roles; DELETE FROM schema_migrations WHERE version IN (5,7); UPDATE users SET role='admin'`); err != nil {
		t.Fatal(err)
	}
	a.Close()
	a, err = newApp(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if a.enabledAdminCount() != 1 {
		t.Fatal("legacy admin role was not converted to a binding")
	}
	var hasRole int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('users') WHERE name='role'`).Scan(&hasRole)
	if hasRole != 0 {
		t.Fatal("legacy role column survived migration 7")
	}
}

func TestLookupsDoNotRequireProxyOrConnectionModules(t *testing.T) {
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "correct-horse-battery")
	t.Setenv("CC_MANAGER_ENCRYPTION_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	a, err := newApp(t.TempDir()+"/lookups.db", false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	admin := newTestClient(t, a)
	admin.login("correct-horse-battery")
	var role struct{ Data struct{ ID string } }
	res := admin.do(http.MethodPost, "/api/v1/roles", `{"name":"accounts-only","permissions":["accounts.view","accounts.onboard"]}`)
	_ = jsonDecode(res, &role)
	if res.StatusCode != http.StatusCreated || role.Data.ID == "" {
		t.Fatalf("create role status=%d", res.StatusCode)
	}
	if res := admin.do(http.MethodPost, "/api/v1/users", `{"username":"acct","password":"accounts-password-1","role_ids":["`+role.Data.ID+`"]}`); res.StatusCode != http.StatusCreated {
		t.Fatalf("create user status=%d", res.StatusCode)
	}
	if res := admin.do(http.MethodPost, "/api/v1/platform-connections", `{"name":"supplier","base_url":"https://x.example","timezone":"Asia/Shanghai","token":"t"}`); res.StatusCode != http.StatusCreated {
		t.Fatalf("create connection status=%d", res.StatusCode)
	}

	user := newTestClient(t, a)
	user.loginAs("acct", "accounts-password-1")
	for _, path := range []string{"/api/v1/proxies", "/api/v1/platform-connections"} {
		if res := user.do(http.MethodGet, path, ""); res.StatusCode != http.StatusForbidden {
			t.Fatalf("GET %s=%d, want 403", path, res.StatusCode)
		}
	}
	// Private proxy pools: admin-created proxies are invisible to other users.
	var proxies struct{ Data []map[string]any }
	_ = jsonDecode(user.do(http.MethodGet, "/api/v1/lookups/proxies", ""), &proxies)
	if len(proxies.Data) != 0 {
		t.Fatalf("proxy lookup should be empty for non-owner, got %v", proxies.Data)
	}
	var me struct {
		Data struct {
			User struct{ ID string }
		}
	}
	_ = jsonDecode(user.do(http.MethodGet, "/api/v1/auth/session", ""), &me)
	if _, err := a.DB.Exec(`INSERT INTO proxies(id,name,scheme,host,port,enabled,created_at,updated_at,created_by_user_id) VALUES('own-proxy','exit-1','http','127.0.0.1',1080,1,?,?,?)`, now(), now(), me.Data.User.ID); err != nil {
		t.Fatal(err)
	}
	_ = jsonDecode(user.do(http.MethodGet, "/api/v1/lookups/proxies", ""), &proxies)
	if len(proxies.Data) != 1 || proxies.Data[0]["name"] != "exit-1" || proxies.Data[0]["latest_check"] != nil {
		t.Fatalf("proxy lookup=%v", proxies.Data)
	}
	var conns struct{ Data []map[string]any }
	_ = jsonDecode(user.do(http.MethodGet, "/api/v1/lookups/platform-connections", ""), &conns)
	if len(conns.Data) != 1 || conns.Data[0]["name"] != "supplier" {
		t.Fatalf("connection lookup=%v", conns.Data)
	}
	for k := range conns.Data[0] {
		if k == "token" || k == "token_configured" || k == "version" {
			t.Fatalf("lookup leaks %s", k)
		}
	}
}

func TestProxyUpdateAndDelete(t *testing.T) {
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "correct-horse-battery")
	t.Setenv("CC_MANAGER_ENCRYPTION_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	a, err := newApp(t.TempDir()+"/proxy-edit.db", false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	c := newTestClient(t, a)
	c.login("correct-horse-battery")
	var created struct{ Data struct{ ID string } }
	_ = jsonDecode(c.do(http.MethodPost, "/api/v1/proxies", `{"name":"old","scheme":"http","host":"127.0.0.1","port":1080,"password":"secret"}`), &created)
	id := created.Data.ID
	if res := c.do(http.MethodPatch, "/api/v1/proxies/"+id, `{"name":"new","port":1081,"expected_version":9}`); res.StatusCode != http.StatusConflict {
		t.Fatalf("stale version status=%d", res.StatusCode)
	}
	if res := c.do(http.MethodPatch, "/api/v1/proxies/"+id, `{"port":70000}`); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bad port status=%d", res.StatusCode)
	}
	var updated struct{ Data struct{ Version int } }
	res := c.do(http.MethodPatch, "/api/v1/proxies/"+id, `{"name":"new","port":1081,"enabled":false,"clear_password":true,"expected_version":1}`)
	_ = jsonDecode(res, &updated)
	if res.StatusCode != http.StatusOK || updated.Data.Version != 2 {
		t.Fatalf("update status=%d version=%d", res.StatusCode, updated.Data.Version)
	}
	var list struct{ Data []map[string]any }
	_ = jsonDecode(c.do(http.MethodGet, "/api/v1/proxies", ""), &list)
	row := list.Data[0]
	if row["name"] != "new" || row["port"].(float64) != 1081 || row["enabled"] != false || row["password_configured"] != false {
		t.Fatalf("row after update=%v", row)
	}
	// Bound to a live account: refuse. Archive the account: delete succeeds and unbinds.
	if _, err := a.DB.Exec(`INSERT INTO accounts(id,account,proxy_id,created_at,updated_at) VALUES('acc-1','a@example.com',?,?,?)`, id, now(), now()); err != nil {
		t.Fatal(err)
	}
	var body struct{ Error struct{ Code string } }
	res = c.do(http.MethodDelete, "/api/v1/proxies/"+id, "")
	_ = jsonDecode(res, &body)
	if res.StatusCode != http.StatusConflict || body.Error.Code != "PROXY_IN_USE" {
		t.Fatalf("delete bound status=%d code=%s", res.StatusCode, body.Error.Code)
	}
	if _, err := a.DB.Exec(`UPDATE accounts SET archived=1 WHERE id='acc-1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`INSERT INTO proxy_checks(id,proxy_id,ok,checked_at) VALUES('chk',?,1,?)`, id, now()); err != nil {
		t.Fatal(err)
	}
	if res := c.do(http.MethodDelete, "/api/v1/proxies/"+id, ""); res.StatusCode != http.StatusOK {
		t.Fatalf("delete status=%d", res.StatusCode)
	}
	var n int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM proxies WHERE id=?`, id).Scan(&n)
	if n != 0 {
		t.Fatal("proxy row still present")
	}
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM proxy_checks WHERE proxy_id=?`, id).Scan(&n)
	if n != 0 {
		t.Fatal("checks not removed")
	}
	var bound string
	_ = a.DB.QueryRow(`SELECT proxy_id FROM accounts WHERE id='acc-1'`).Scan(&bound)
	if bound != "" {
		t.Fatalf("archived account still bound to %q", bound)
	}
	if res := c.do(http.MethodDelete, "/api/v1/proxies/"+id, ""); res.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete status=%d", res.StatusCode)
	}
}

func TestOperatorCanDeleteDraftAccountsViewerCannot(t *testing.T) {
	a, admin := rbacApp(t)
	if res := admin.do(http.MethodPost, "/api/v1/users", `{"username":"ops2","password":"operator-password","role_ids":["`+roleOperatorID+`"]}`); res.StatusCode != http.StatusCreated {
		t.Fatalf("create operator=%d", res.StatusCode)
	}
	if res := admin.do(http.MethodPost, "/api/v1/users", `{"username":"ro","password":"operator-password","role_ids":["`+roleViewerID+`"]}`); res.StatusCode != http.StatusCreated {
		t.Fatalf("create viewer=%d", res.StatusCode)
	}
	operator := newTestClient(t, a)
	operator.loginAs("ops2", "operator-password")
	res := operator.do(http.MethodPost, "/api/v1/accounts", `{"account":"draft@example.test","plan":"claude/cc-pro"}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create draft=%d", res.StatusCode)
	}
	var created struct {
		Data struct{ ID string }
	}
	_ = jsonDecode(res, &created)
	viewer := newTestClient(t, a)
	viewer.loginAs("ro", "operator-password")
	if res := viewer.do(http.MethodDelete, "/api/v1/accounts/"+created.Data.ID, ""); res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer delete=%d", res.StatusCode)
	}
	if res := operator.do(http.MethodDelete, "/api/v1/accounts/"+created.Data.ID, ""); res.StatusCode != http.StatusOK {
		t.Fatalf("operator delete=%d", res.StatusCode)
	}
	_ = a
}
