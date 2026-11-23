package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"cc-manager/internal/errs"
)

// oauthDispatchSettings are runtime controls for the OAuth pipeline. They are
// deliberately narrow: operators tune request bursts and spacing, while 429
// handling and provider-imposed Retry-After remain untouched.
type oauthDispatchSettings struct {
	GlobalConcurrency       int    `json:"global_concurrency"`
	ExitConcurrency         int    `json:"exit_concurrency"`
	ExchangeIntervalSeconds int    `json:"exchange_interval_seconds"`
	ProbeCacheSeconds       int    `json:"probe_cache_seconds"`
	ManualPreheatSecondsMin int    `json:"manual_preheat_seconds_min"`
	ManualPreheatSecondsMax int    `json:"manual_preheat_seconds_max"`
	BatchPreheatSecondsMin  int    `json:"batch_preheat_seconds_min"`
	BatchPreheatSecondsMax  int    `json:"batch_preheat_seconds_max"`
	UAPoolJSON              string `json:"ua_pool_json"`
	Version                 int    `json:"version"`
}

func (a *App) loadOAuthDispatchSettings() (oauthDispatchSettings, error) {
	var s oauthDispatchSettings
	err := a.DB.QueryRow(`SELECT global_concurrency,exit_concurrency,exchange_interval_seconds,probe_cache_seconds,manual_preheat_seconds_min,manual_preheat_seconds_max,batch_preheat_seconds_min,batch_preheat_seconds_max,ua_pool_json,version FROM oauth_dispatch_settings WHERE id=1`).Scan(
		&s.GlobalConcurrency, &s.ExitConcurrency, &s.ExchangeIntervalSeconds, &s.ProbeCacheSeconds,
		&s.ManualPreheatSecondsMin, &s.ManualPreheatSecondsMax, &s.BatchPreheatSecondsMin, &s.BatchPreheatSecondsMax, &s.UAPoolJSON, &s.Version,
	)
	if err != nil {
		// A database created before the file was started still has the table
		// from migrations; no fallback is needed. An empty row is impossible
		// because the id=1 default is inserted by migration.
		return s, err
	}
	return s, nil
}

func (a *App) oauthDispatchSettingsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		methodNotAllowed(w, http.MethodGet, http.MethodPut)
		return
	}
	s, err := a.loadOAuthDispatchSettings()
	if err != nil {
		writeError(w, r, storageError(err, "上号调度设置读取失败"))
		return
	}
	if r.Method == http.MethodGet {
		write(w, 200, s)
		return
	}
	var in oauthDispatchSettings
	if !decodeJSON(w, r, &in, 8<<10) {
		return
	}
	problems := errs.Fields{}
	checkRange := func(field string, v, min, max int) {
		if v < min || v > max {
			problems.Add(field, errs.FieldOutOfRange, "须为 "+fmt.Sprintf("%d–%d", min, max))
		}
	}
	checkRange("global_concurrency", in.GlobalConcurrency, 1, 10)
	checkRange("exit_concurrency", in.ExitConcurrency, 1, 5)
	checkRange("exchange_interval_seconds", in.ExchangeIntervalSeconds, 0, 60)
	checkRange("probe_cache_seconds", in.ProbeCacheSeconds, 0, 30)
	checkRange("manual_preheat_seconds_min", in.ManualPreheatSecondsMin, 1, 3)
	checkRange("manual_preheat_seconds_max", in.ManualPreheatSecondsMax, 1, 3)
	checkRange("batch_preheat_seconds_min", in.BatchPreheatSecondsMin, 5, 15)
	checkRange("batch_preheat_seconds_max", in.BatchPreheatSecondsMax, 5, 15)
	if in.ManualPreheatSecondsMax < in.ManualPreheatSecondsMin {
		problems.Add("manual_preheat_seconds_max", errs.FieldMismatch, "上限不能小于下限")
	}
	if in.BatchPreheatSecondsMax < in.BatchPreheatSecondsMin {
		problems.Add("batch_preheat_seconds_max", errs.FieldMismatch, "上限不能小于下限")
	}
	if in.UAPoolJSON == "" {
		in.UAPoolJSON = "[]"
	}
	if pool, e := parseOAuthUAPool(in.UAPoolJSON); e != nil || len(pool) > 8 {
		problems.Invalid("ua_pool_json", "须为不超过 8 条、每条不超过 120 字符的 UA JSON 数组")
	}
	if e := problems.Err(); e != nil {
		writeError(w, r, e)
		return
	}
	if in.Version != s.Version {
		fail(w, 409, "VERSION_CONFLICT", "设置已变化，请重新加载后保存")
		return
	}
	result, err := a.DB.ExecContext(r.Context(), `UPDATE oauth_dispatch_settings SET global_concurrency=?,exit_concurrency=?,exchange_interval_seconds=?,probe_cache_seconds=?,manual_preheat_seconds_min=?,manual_preheat_seconds_max=?,batch_preheat_seconds_min=?,batch_preheat_seconds_max=?,ua_pool_json=?,version=version+1 WHERE id=1 AND version=?`,
		in.GlobalConcurrency, in.ExitConcurrency, in.ExchangeIntervalSeconds, in.ProbeCacheSeconds,
		in.ManualPreheatSecondsMin, in.ManualPreheatSecondsMax, in.BatchPreheatSecondsMin, in.BatchPreheatSecondsMax, in.UAPoolJSON, in.Version)
	if err != nil {
		writeError(w, r, storageError(err, "上号调度设置保存失败"))
		return
	}
	if n, _ := result.RowsAffected(); n != 1 {
		fail(w, 409, "VERSION_CONFLICT", "设置已变化，请重新加载后保存")
		return
	}
	a.auditFor(r, "oauth.settings.update", "oauth_settings", "1", dispatchSettingsAudit(in))
	a.applyOAuthDispatchSettings(in)
	resetOAuthUAPoolCache()
	s, err = a.loadOAuthDispatchSettings()
	if err != nil {
		writeError(w, r, storageError(err, "上号调度设置读取失败"))
		return
	}
	write(w, 200, s)
}

func dispatchSettingsAudit(s oauthDispatchSettings) string {
	b, _ := json.Marshal(s)
	return strings.TrimSpace(string(b))
}

// applyOAuthDispatchSettings atomically updates the gate size without dropping
// queued waiters. The new gate only takes effect for later acquisitions.
func (a *App) applyOAuthDispatchSettings(s oauthDispatchSettings) {
	a.oauthGate.Store(newOAuthGate(s.GlobalConcurrency))
	a.applyOAuthExitGate(s.ExitConcurrency)
}
