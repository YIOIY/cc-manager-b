package app

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"testing"
	"time"
)

type profileResponse struct {
	Data struct {
		Verified bool            `json:"verified"`
		Hint     string          `json:"hint"`
		Changed  []string        `json:"changed"`
		Platform json.RawMessage `json:"platform"`
	} `json:"data"`
}

// profileBase is the snapshot the fixtures hold for every account: what a
// client sends back as "base" to prove it edited the current record.
func profileBase(overrides map[string]string) map[string]string {
	base := map[string]string{"plan": "claude/cc-max-20x", "register_time": "2026-11-03", "plan_start_time": "2026-11-11", "plan_end_time": "2026-12-10", "register_location": "US", "balance": "100", "payment_method": "google_pay", "register_device": "Mac", "recharge_time": "", "warranty_period": "", "remark": ""}
	for k, v := range overrides {
		base[k] = v
	}
	return base
}

func profileRequest(t *testing.T, changes, base map[string]string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"changes": changes, "base": base})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func postProfile(t *testing.T, c *testClient, accountID, body string) (*http.Response, profileResponse) {
	t.Helper()
	res := c.do(http.MethodPost, "/api/v1/accounts/"+accountID+"/platform-profile", body)
	var out profileResponse
	if res.StatusCode == http.StatusOK {
		_ = jsonDecode(res, &out)
	}
	return res, out
}

func TestPlatformProfileUpdatesPlatformAndLocal(t *testing.T) {
	a, stub, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	res, out := postProfile(t, admin, id, profileRequest(t, map[string]string{"remark": "1 cc-manager", "plan_end_time": "2026-12-31"}, profileBase(nil)))
	if res.StatusCode != 200 || !out.Data.Verified || out.Data.Hint != "" {
		t.Fatalf("status=%d verified=%v hint=%q", res.StatusCode, out.Data.Verified, out.Data.Hint)
	}
	sort.Strings(out.Data.Changed)
	if len(out.Data.Changed) != 2 || out.Data.Changed[0] != "plan_end_time" || out.Data.Changed[1] != "remark" {
		t.Fatalf("changed=%v", out.Data.Changed)
	}
	if len(stub.updates) != 1 {
		t.Fatalf("stub updates=%d", len(stub.updates))
	}
	got := stub.updates[0]
	for k, v := range map[string]any{"account_id": "one@example.test", "provider": "claude", "model": "claude/cc-max-20x", "remark": "1 cc-manager", "cpa_plan": "claude/cc-max-20x", "cpa_register_time": "2026-11-03", "cpa_plan_start_time": "2026-11-11", "cpa_plan_end_time": "2026-12-31", "cpa_register_location": "US", "cpa_balance": "100", "cpa_payment_method": "google_pay", "cpa_register_device": "Mac", "cpa_recharge_time": "", "cpa_warranty_period": "", "account_type": float64(2)} {
		if got[k] != v {
			t.Errorf("payload %s=%#v want %#v", k, got[k], v)
		}
	}
	if enc, _ := got["enc_adc_config"].(string); stub.decrypt(enc) != "" {
		t.Errorf("enc_adc_config must encrypt an empty password")
	}
	if v := queryString(t, a, `SELECT plan_end_time FROM platform_remote_accounts WHERE upstream_id='428802'`); v != "2026-12-31" {
		t.Fatalf("snapshot plan_end_time=%q", v)
	}
	if v := queryString(t, a, `SELECT remark FROM platform_remote_accounts WHERE upstream_id='428802'`); v != "1 cc-manager" {
		t.Fatalf("snapshot remark=%q", v)
	}
	if v := queryString(t, a, `SELECT plan_end_time FROM accounts WHERE id=?`, id); v != "2026-12-31" {
		t.Fatalf("local plan_end_time=%q", v)
	}
	var meta map[string]any
	_ = json.Unmarshal([]byte(queryString(t, a, `SELECT metadata_json FROM accounts WHERE id=?`, id)), &meta)
	if meta["remark"] != "1 cc-manager" || meta["balance"] != "100" {
		t.Fatalf("local metadata=%v", meta)
	}
	if n := queryInt(t, a, `SELECT version FROM accounts WHERE id=?`, id); n != 2 {
		t.Fatalf("local version=%d", n)
	}
	block := decodePlatform(t, out.Data.Platform)
	if block == nil || block["remark"] != "1 cc-manager" {
		t.Fatalf("response platform=%v", block)
	}
	web, _ := block["web_channel"].(map[string]any)
	if web == nil || web["configured"] != true || web["suspended"] != false {
		t.Fatalf("web_channel=%v", web)
	}
	// Local and platform agree again, so nothing is flagged as a difference.
	items, counts, err := a.attentionItems(context.Background(), time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	if err != nil || counts["profile_diff"] != 0 {
		t.Fatalf("attention items=%v counts=%v err=%v", items, counts, err)
	}
	if n := queryInt(t, a, `SELECT COUNT(*) FROM audit_events WHERE action='account.platform_profile_update' AND resource_id=? AND detail='plan_end_time,remark'`, id); n != 1 {
		t.Fatalf("audit rows=%d", n)
	}
	// The account list carries the new fields too.
	var list platformEnvelope
	_ = jsonDecode(admin.do(http.MethodGet, "/api/v1/accounts?page=1&page_size=20&q=one@example.test", ""), &list)
	if len(list.Data.Items) != 1 {
		t.Fatalf("items=%d", len(list.Data.Items))
	}
	if p := decodePlatform(t, list.Data.Items[0].Platform); p["remark"] != "1 cc-manager" || p["web_channel"] == nil {
		t.Fatalf("list platform=%v", p)
	}
}

func TestPlatformProfileRejectsStaleBase(t *testing.T) {
	a, stub, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	res, _ := postProfile(t, admin, id, profileRequest(t, map[string]string{"remark": "x"}, profileBase(map[string]string{"remark": "old"})))
	assertEnvelope(t, res, http.StatusConflict, "VERSION_CONFLICT")
	// A base that omits the field being changed proves nothing either.
	res, _ = postProfile(t, admin, id, profileRequest(t, map[string]string{"remark": "x"}, map[string]string{}))
	assertEnvelope(t, res, http.StatusConflict, "VERSION_CONFLICT")
	if len(stub.updates) != 0 {
		t.Fatalf("stub received %d updates", len(stub.updates))
	}
}

func TestPlatformProfileValidation(t *testing.T) {
	a, stub, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	res, _ := postProfile(t, admin, id, profileRequest(t, map[string]string{"foo": "x", "plan": "", "balance": "150", "plan_end_time": "2026-13-01"}, profileBase(nil)))
	env := assertEnvelope(t, res, http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	fields := map[string]bool{}
	for _, f := range env.Error.Fields {
		fields[f.Field] = true
	}
	for _, want := range []string{"foo", "plan", "balance", "plan_end_time"} {
		if !fields[want] {
			t.Errorf("missing field error for %s: %+v", want, env.Error.Fields)
		}
	}
	res, _ = postProfile(t, admin, id, profileRequest(t, map[string]string{}, profileBase(nil)))
	assertEnvelope(t, res, http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	res, _ = postProfile(t, admin, id, profileRequest(t, map[string]string{"remark": ""}, profileBase(nil)))
	assertEnvelope(t, res, http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	if len(stub.updates) != 0 {
		t.Fatalf("stub received %d updates", len(stub.updates))
	}
}

func TestPlatformProfileUnverifiedWhenRefreshFails(t *testing.T) {
	a, stub, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	stub.mu.Lock()
	stub.listStatus = 500
	stub.mu.Unlock()
	res, out := postProfile(t, admin, id, profileRequest(t, map[string]string{"plan_end_time": "2026-12-31"}, profileBase(nil)))
	if res.StatusCode != 200 || out.Data.Verified || out.Data.Hint == "" {
		t.Fatalf("status=%d verified=%v hint=%q", res.StatusCode, out.Data.Verified, out.Data.Hint)
	}
	if len(stub.updates) != 1 {
		t.Fatalf("stub updates=%d", len(stub.updates))
	}
	if v := queryString(t, a, `SELECT plan_end_time FROM platform_remote_accounts WHERE upstream_id='428802'`); v != "2026-12-31" {
		t.Fatalf("snapshot plan_end_time=%q", v)
	}
	if v := queryString(t, a, `SELECT plan_end_time FROM accounts WHERE id=?`, id); v != "2026-12-31" {
		t.Fatalf("local plan_end_time=%q", v)
	}
	if n := queryInt(t, a, `SELECT COUNT(*) FROM audit_events WHERE action='account.platform_profile_update' AND resource_id=?`, id); n != 1 {
		t.Fatalf("audit rows=%d", n)
	}
}

func TestPlatformProfileKeepsEmptyUntouchedFields(t *testing.T) {
	a, stub, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	res, out := postProfile(t, admin, id, profileRequest(t, map[string]string{"remark": "only remark"}, map[string]string{"remark": ""}))
	if res.StatusCode != 200 || !out.Data.Verified || len(out.Data.Changed) != 1 {
		t.Fatalf("status=%d verified=%v changed=%v", res.StatusCode, out.Data.Verified, out.Data.Changed)
	}
	got := stub.updates[0]
	if got["cpa_recharge_time"] != "" || got["cpa_warranty_period"] != "" || got["cpa_auth_json"] != "" || got["remark"] != "only remark" {
		t.Fatalf("payload=%v", got)
	}
	if _, ok := got["cpa_tags"]; ok {
		t.Fatal("payload carries cpa_tags")
	}
}

func TestPlatformProfileBlockedStates(t *testing.T) {
	a, stub, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	body := profileRequest(t, map[string]string{"remark": "x"}, profileBase(nil))

	// A user without accounts.platform_edit is refused before anything else.
	if res := admin.do(http.MethodPost, "/api/v1/users", `{"username":"ops","password":"operator-password","role_ids":["`+roleOperatorID+`"]}`); res.StatusCode != http.StatusCreated {
		t.Fatalf("create operator=%d", res.StatusCode)
	}
	operator := newTestClient(t, a)
	operator.loginAs("ops", "operator-password")
	assertEnvelope(t, operator.do(http.MethodPost, "/api/v1/accounts/"+id+"/platform-profile", body), http.StatusForbidden, "PERMISSION_DENIED")

	// An account the platform no longer lists cannot be edited.
	if _, err := a.DB.Exec(`UPDATE platform_remote_accounts SET missing_since='2026-11-12T00:00:00Z' WHERE upstream_id='428802'`); err != nil {
		t.Fatal(err)
	}
	res, _ := postProfile(t, admin, id, body)
	assertEnvelope(t, res, http.StatusConflict, "VERSION_CONFLICT")
	if _, err := a.DB.Exec(`UPDATE platform_remote_accounts SET missing_since='' WHERE upstream_id='428802'`); err != nil {
		t.Fatal(err)
	}

	// An account without a platform record at all.
	insertLocalAccount(t, a, "acc-local", "local@example.test")
	res, _ = postProfile(t, admin, "acc-local", body)
	assertEnvelope(t, res, http.StatusConflict, "VERSION_CONFLICT")

	// Without console credentials the channel is not configured.
	if _, err := a.DB.Exec(`UPDATE platform_connections SET web_username='',web_password_secret='' WHERE id='conn'`); err != nil {
		t.Fatal(err)
	}
	a.dropWebClient("conn")
	res, _ = postProfile(t, admin, id, body)
	assertEnvelope(t, res, http.StatusUnprocessableEntity, "WEB_NOT_CONFIGURED")
	if len(stub.updates) != 0 {
		t.Fatalf("stub received %d updates", len(stub.updates))
	}
}

func TestPermissionCatalogueIncludesPlatformEdit(t *testing.T) {
	found := false
	for _, mod := range permissionCatalogue {
		for _, def := range mod.Permissions {
			if def.Key == PermAccountsPlatformEdit {
				found = mod.Key == "accounts"
			}
		}
	}
	if !found {
		t.Fatal("accounts.platform_edit is not in the 账号管理 module")
	}
	for _, role := range builtinRoles() {
		if role.id == roleAdminID {
			continue // the administrator holds every catalogue entry
		}
		for _, p := range role.permissions {
			if p == string(PermAccountsPlatformEdit) {
				t.Fatalf("built-in role %s grants accounts.platform_edit", role.name)
			}
		}
	}
}
