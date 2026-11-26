package app

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestSystemSettingsGetPutAndOnboardGates(t *testing.T) {
	a, admin := rbacApp(t)

	res := admin.do(http.MethodGet, "/api/v1/system-settings", "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("get status=%d", res.StatusCode)
	}
	var settings struct {
		Data systemSettings `json:"data"`
	}
	_ = jsonDecode(res, &settings)
	if settings.Data.DailyOnboardMax != 0 || settings.Data.OnboardWindowStart != "" || settings.Data.OnboardWindowEnd != "" {
		t.Fatalf("defaults=%+v", settings.Data)
	}

	body := `{"daily_onboard_max":2,"onboard_window_start":"","onboard_window_end":"","version":` + strconv.Itoa(settings.Data.Version) + `}`
	res = admin.do(http.MethodPut, "/api/v1/system-settings", body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("put status=%d", res.StatusCode)
	}
	_ = jsonDecode(res, &settings)
	if settings.Data.DailyOnboardMax != 2 || settings.Data.Version != 2 {
		t.Fatalf("after put=%+v", settings.Data)
	}

	res = admin.do(http.MethodPost, "/api/v1/users", `{"username":"ops-limit","password":"operator-password","role_ids":["`+roleOperatorID+`"]}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create ops=%d", res.StatusCode)
	}
	var created struct {
		Data struct {
			ID                string `json:"id"`
			DailyOnboardLimit int    `json:"daily_onboard_limit"`
			Version           int    `json:"version"`
		} `json:"data"`
	}
	_ = jsonDecode(res, &created)
	if created.Data.DailyOnboardLimit != 0 {
		t.Fatalf("default daily_onboard_limit=%d", created.Data.DailyOnboardLimit)
	}
	ops := newTestClient(t, a)
	ops.loginAs("ops-limit", "operator-password")

	proxy := createOwnedProxy(t, ops, "limit-proxy")
	for i := 0; i < 2; i++ {
		acc := createOwnedAccount(t, ops, "limit"+strconv.Itoa(i)+"@example.test", proxy)
		if _, err := a.DB.Exec(`INSERT INTO jobs(id,kind,state,account_id,created_at,updated_at) VALUES(?,?,?,?,?,?)`,
			"job-limit-"+strconv.Itoa(i), "account.submit", "succeeded", acc, now(), now()); err != nil {
			t.Fatal(err)
		}
	}
	res = ops.do(http.MethodPost, "/api/v1/accounts", `{"account":"over-global@example.test","plan":"claude/cc-pro"}`)
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(res.Body).Decode(&env)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden || env.Error.Code != "ONBOARD_DAILY_LIMIT" {
		t.Fatalf("global limit => status=%d code=%q", res.StatusCode, env.Error.Code)
	}

	// Clear global; set per-user limit of 1.
	body = `{"daily_onboard_max":0,"onboard_window_start":"","onboard_window_end":"","version":` + strconv.Itoa(settings.Data.Version) + `}`
	if res = admin.do(http.MethodPut, "/api/v1/system-settings", body); res.StatusCode != http.StatusOK {
		t.Fatalf("clear global=%d", res.StatusCode)
	}
	_ = jsonDecode(res, &settings)

	patch := `{"daily_onboard_limit":1,"expected_version":` + strconv.Itoa(created.Data.Version) + `}`
	res = admin.do(http.MethodPatch, "/api/v1/users/"+created.Data.ID, patch)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("patch user limit=%d", res.StatusCode)
	}
	_ = jsonDecode(res, &created)
	if created.Data.DailyOnboardLimit != 1 {
		t.Fatalf("user limit=%d", created.Data.DailyOnboardLimit)
	}
	// Ops already has 2 jobs today; user limit 1 must block.
	res = ops.do(http.MethodPost, "/api/v1/accounts", `{"account":"over-user@example.test","plan":"claude/cc-pro"}`)
	_ = json.NewDecoder(res.Body).Decode(&env)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden || env.Error.Code != "ONBOARD_DAILY_LIMIT" {
		t.Fatalf("user limit => status=%d code=%q", res.StatusCode, env.Error.Code)
	}

	// Outside window: set a window that excludes "now".
	nowLocal := time.Now().In(onboardDayLocation)
	outsideStart := nowLocal.Add(2 * time.Hour).Format("15:04")
	outsideEnd := nowLocal.Add(3 * time.Hour).Format("15:04")
	if outsideStart == outsideEnd {
		outsideEnd = nowLocal.Add(4 * time.Hour).Format("15:04")
	}
	// Raise user limit so only the window gate fires.
	patch = `{"daily_onboard_limit":0,"expected_version":` + strconv.Itoa(created.Data.Version) + `}`
	if res = admin.do(http.MethodPatch, "/api/v1/users/"+created.Data.ID, patch); res.StatusCode != http.StatusOK {
		t.Fatalf("clear user limit=%d", res.StatusCode)
	}
	_ = jsonDecode(res, &created)

	body = `{"daily_onboard_max":0,"onboard_window_start":"` + outsideStart + `","onboard_window_end":"` + outsideEnd + `","version":` + strconv.Itoa(settings.Data.Version) + `}`
	if res = admin.do(http.MethodPut, "/api/v1/system-settings", body); res.StatusCode != http.StatusOK {
		t.Fatalf("set window=%d", res.StatusCode)
	}
	res = ops.do(http.MethodPost, "/api/v1/accounts", `{"account":"outside-window@example.test","plan":"claude/cc-pro"}`)
	_ = json.NewDecoder(res.Body).Decode(&env)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden || env.Error.Code != "ONBOARD_OUTSIDE_WINDOW" {
		t.Fatalf("window => status=%d code=%q", res.StatusCode, env.Error.Code)
	}
}

func TestOnboardWindowAllows(t *testing.T) {
	loc := onboardDayLocation
	mid := time.Date(2026, 11, 26, 12, 0, 0, 0, loc)
	cases := []struct {
		start, end string
		ok         bool
	}{
		{"", "", true},
		{"09:00", "18:00", true},
		{"13:00", "18:00", false},
		{"22:00", "06:00", false},
		{"22:00", "13:00", true},
		{"12:00", "12:00", true},
	}
	for _, c := range cases {
		if got := onboardWindowAllows(c.start, c.end, mid); got != c.ok {
			t.Fatalf("%s-%s @12:00 => %v want %v", c.start, c.end, got, c.ok)
		}
	}
}
