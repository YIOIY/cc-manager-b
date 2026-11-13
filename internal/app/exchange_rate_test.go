package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// rateStub serves one fixed body and counts requests.
func rateStub(t *testing.T, status int, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Method != http.MethodGet || r.ContentLength > 0 || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("rate request must be a bare GET, got %s with headers %v", r.Method, r.Header)
		}
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func storedRate(t *testing.T, a *App) (rate float64, source, date string, ok bool) {
	t.Helper()
	err := a.DB.QueryRow(`SELECT rate,source,rate_date FROM exchange_rates WHERE pair='USD/CNY'`).Scan(&rate, &source, &date)
	return rate, source, date, err == nil
}

func TestExchangeRateRefreshStoresPrimary(t *testing.T) {
	a, _ := rbacApp(t)
	primary, primaryHits := rateStub(t, 200, `{"amount":1.0,"base":"USD","date":"2026-11-12","rates":{"CNY":6.6683}}`)
	fallback, fallbackHits := rateStub(t, 200, `{"result":"success","base_code":"USD","rates":{"CNY":6.7}}`)
	a.rateSources = []rateSource{{Name: "ecb", URL: primary.URL}, {Name: "open-er-api", URL: fallback.URL}}
	if err := a.refreshExchangeRate(context.Background()); err != nil {
		t.Fatal(err)
	}
	rate, source, date, ok := storedRate(t, a)
	if !ok || rate != 6.6683 || source != "ecb" || date != "2026-11-12" {
		t.Fatalf("stored rate=%v source=%q date=%q ok=%v", rate, source, date, ok)
	}
	if primaryHits.Load() != 1 || fallbackHits.Load() != 0 {
		t.Fatalf("hits primary=%d fallback=%d", primaryHits.Load(), fallbackHits.Load())
	}
}

func TestExchangeRateFallsBackAndKeepsLastGood(t *testing.T) {
	a, _ := rbacApp(t)
	down, _ := rateStub(t, 500, `oops`)
	fallback, _ := rateStub(t, 200, `{"result":"success","base_code":"USD","rates":{"CNY":6.685071}}`)
	a.rateSources = []rateSource{{Name: "ecb", URL: down.URL}, {Name: "open-er-api", URL: fallback.URL}}
	if err := a.refreshExchangeRate(context.Background()); err != nil {
		t.Fatal(err)
	}
	rate, source, date, _ := storedRate(t, a)
	// A source without a date is dated with the day it was fetched.
	if rate != 6.685071 || source != "open-er-api" || date != time.Now().UTC().Format("2006-01-02") {
		t.Fatalf("fallback rate=%v source=%q date=%q", rate, source, date)
	}

	// Nothing usable anywhere: report it and keep the last good rate.
	for name, body := range map[string]string{
		"too small":   `{"rates":{"CNY":0.5}}`,
		"too large":   `{"rates":{"CNY":100}}`,
		"missing":     `{"rates":{"JPY":150}}`,
		"not json":    `<html>`,
		"wrong shape": `{"rates":{"CNY":"6.7"}}`,
	} {
		bad, _ := rateStub(t, 200, body)
		a.rateSources = []rateSource{{Name: "ecb", URL: down.URL}, {Name: "bad", URL: bad.URL}}
		if err := a.refreshExchangeRate(context.Background()); err == nil {
			t.Fatalf("%s: accepted", name)
		}
		if got, src, _, _ := storedRate(t, a); got != 6.685071 || src != "open-er-api" {
			t.Fatalf("%s: stored rate changed to %v (%s)", name, got, src)
		}
	}
}

func TestExchangeRateEndpoint(t *testing.T) {
	a, admin := rbacApp(t)
	type reply struct {
		Data struct {
			Pair      string   `json:"pair"`
			Rate      *float64 `json:"rate"`
			Source    string   `json:"source"`
			RateDate  string   `json:"rate_date"`
			FetchedAt string   `json:"fetched_at"`
		} `json:"data"`
	}
	var out reply
	res := admin.do(http.MethodGet, "/api/v1/exchange-rate", "")
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil || res.StatusCode != 200 || out.Data.Pair != "USD/CNY" || out.Data.Rate != nil {
		t.Fatalf("empty: status=%d data=%+v err=%v", res.StatusCode, out.Data, err)
	}
	src, _ := rateStub(t, 200, `{"date":"2026-11-12","rates":{"CNY":6.6683}}`)
	a.rateSources = []rateSource{{Name: "ecb", URL: src.URL}}
	if err := a.refreshExchangeRate(context.Background()); err != nil {
		t.Fatal(err)
	}
	out = reply{}
	res = admin.do(http.MethodGet, "/api/v1/exchange-rate", "")
	_ = json.NewDecoder(res.Body).Decode(&out)
	if res.StatusCode != 200 || out.Data.Rate == nil || *out.Data.Rate != 6.6683 || out.Data.Source != "ecb" || out.Data.RateDate != "2026-11-12" || out.Data.FetchedAt == "" {
		t.Fatalf("filled: status=%d data=%+v", res.StatusCode, out.Data)
	}
	anonymous := newTestClient(t, a)
	if res := anonymous.do(http.MethodGet, "/api/v1/exchange-rate", ""); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d", res.StatusCode)
	}
}
