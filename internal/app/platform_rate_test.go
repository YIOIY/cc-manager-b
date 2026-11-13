package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

type connectionRow struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	BaseURL string  `json:"base_url"`
	Zone    string  `json:"timezone"`
	Version int     `json:"version"`
	Rate    float64 `json:"usd_cny_rate"`
}

func listConnections(t *testing.T, c *testClient) []connectionRow {
	t.Helper()
	var out struct {
		Data []connectionRow `json:"data"`
	}
	res := c.do(http.MethodGet, "/api/v1/platform-connections", "")
	if err := jsonDecode(res, &out); err != nil || res.StatusCode != 200 {
		t.Fatalf("list connections: %d %v", res.StatusCode, err)
	}
	return out.Data
}

func platformRateOf(t *testing.T, c *testClient, email string) any {
	t.Helper()
	var list platformEnvelope
	res := c.do(http.MethodGet, "/api/v1/accounts?page=1&page_size=20&q="+email, "")
	if err := jsonDecode(res, &list); err != nil || len(list.Data.Items) != 1 {
		t.Fatalf("account %s: items=%d err=%v", email, len(list.Data.Items), err)
	}
	return decodePlatform(t, list.Data.Items[0].Platform)["usd_cny_rate"]
}

// The platform prices in USD and bills in RMB at a fixed rate (7.2 since
// September 2026); the account card divides by that rate to show dollars.
func TestPlatformRateDefaultsToThePlatformFixedRate(t *testing.T) {
	_, _, admin := routesFixture(t)
	conns := listConnections(t, admin)
	if len(conns) != 1 || conns[0].Rate != 7.2 {
		t.Fatalf("connections=%+v", conns)
	}
	if got := platformRateOf(t, admin, "one@example.test"); got != 7.2 {
		t.Fatalf("platform block usd_cny_rate=%v", got)
	}
}

func TestPlatformRateCanBeChangedPerConnection(t *testing.T) {
	a, _, admin := routesFixture(t)
	c := listConnections(t, admin)[0]
	put := func(rate any, version int) *http.Response {
		body, _ := json.Marshal(map[string]any{"name": c.Name, "base_url": c.BaseURL, "timezone": c.Zone, "enabled": true, "usd_cny_rate": rate, "expected_version": version})
		return admin.do(http.MethodPut, "/api/v1/platform-connections/"+c.ID, string(body))
	}
	for _, bad := range []any{0, -1, 0.5, 25, "7.3", nil, true} {
		env := assertEnvelope(t, put(bad, c.Version), http.StatusUnprocessableEntity, "VALIDATION_FAILED")
		if len(env.Error.Fields) != 1 || env.Error.Fields[0].Field != "usd_cny_rate" {
			t.Fatalf("rate %v: fields=%+v", bad, env.Error.Fields)
		}
	}
	if res := put(7.3, c.Version); res.StatusCode != 200 {
		t.Fatalf("set 7.3: %d", res.StatusCode)
	}
	if got := listConnections(t, admin)[0]; got.Rate != 7.3 || got.Version != c.Version+1 {
		t.Fatalf("after update: %+v", got)
	}
	if got := platformRateOf(t, admin, "one@example.test"); got != 7.3 {
		t.Fatalf("platform block usd_cny_rate=%v", got)
	}
	if n := queryInt(t, a, `SELECT COUNT(*) FROM audit_events WHERE action='connection.usd_rate_set' AND resource_id=? AND detail='7.2 → 7.3'`, c.ID); n != 1 {
		t.Fatalf("audit rows=%d", n)
	}
	// An update that does not mention the rate leaves it alone.
	body := fmt.Sprintf(`{"name":%q,"base_url":%q,"timezone":%q,"enabled":true,"expected_version":%d}`, c.Name, c.BaseURL, c.Zone, c.Version+1)
	if res := admin.do(http.MethodPut, "/api/v1/platform-connections/"+c.ID, body); res.StatusCode != 200 {
		t.Fatalf("plain update: %d", res.StatusCode)
	}
	if got := listConnections(t, admin)[0].Rate; got != 7.3 {
		t.Fatalf("rate after plain update=%v", got)
	}
}

func TestPlatformRateOnCreate(t *testing.T) {
	_, _, admin := routesFixture(t) // an app with an encryption key
	create := func(name string, extra string) *http.Response {
		return admin.do(http.MethodPost, "/api/v1/platform-connections", fmt.Sprintf(`{"name":%q,"base_url":"https://%s.example","token":"t","timezone":"Asia/Shanghai"%s}`, name, name, extra))
	}
	if res := create("plain", ""); res.StatusCode != http.StatusCreated {
		t.Fatalf("create plain: %d", res.StatusCode)
	}
	if res := create("custom", `,"usd_cny_rate":7.3`); res.StatusCode != http.StatusCreated {
		t.Fatalf("create custom: %d", res.StatusCode)
	}
	assertEnvelope(t, create("bad", `,"usd_cny_rate":0`), http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	rates := map[string]float64{}
	for _, c := range listConnections(t, admin) {
		rates[c.Name] = c.Rate
	}
	if _, created := rates["bad"]; created || rates["plain"] != 7.2 || rates["custom"] != 7.3 {
		t.Fatalf("rates=%v", rates)
	}
}

// Version 11 briefly kept a market reference rate; version 12 replaces it
// with the per-connection platform rate.
func TestPlatformRateMigration(t *testing.T) {
	legacy, err := sql.Open("sqlite", t.TempDir()+"/legacy.db")
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	if _, err = legacy.Exec(`CREATE TABLE platform_connections(id TEXT PRIMARY KEY,name TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec(`CREATE TABLE exchange_rates(pair TEXT PRIMARY KEY,rate REAL NOT NULL,source TEXT NOT NULL DEFAULT '',rate_date TEXT NOT NULL DEFAULT '',fetched_at TEXT NOT NULL); INSERT INTO exchange_rates VALUES('USD/CNY',6.6683,'ecb','2026-11-12','x'); INSERT INTO platform_connections(id,name) VALUES('conn','n')`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // idempotent
		tx, err := legacy.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err = migratePlatformRateV12(tx); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	var rate float64
	if err = legacy.QueryRow(`SELECT usd_cny_rate FROM platform_connections WHERE id='conn'`).Scan(&rate); err != nil || rate != 7.2 {
		t.Fatalf("existing connection rate=%v err=%v", rate, err)
	}
	var n int
	if err = legacy.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='exchange_rates'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("exchange_rates still present: %d %v", n, err)
	}
}

func TestMarketRateEndpointIsGone(t *testing.T) {
	_, admin := rbacApp(t)
	if res := admin.do(http.MethodGet, "/api/v1/exchange-rate", ""); res.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d", res.StatusCode)
	}
}
