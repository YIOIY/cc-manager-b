package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"cc-manager/internal/errs"
)

// onboardDayLocation is the calendar used for "daily" onboard caps. Operators
// and the UI copy treat a day as Asia/Shanghai, matching bill date practice.
var onboardDayLocation = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}()

var onboardWindowClock = regexp.MustCompile(`^([01]\d|2[0-3]):([0-5]\d)$`)

// systemSettings are site-wide onboard gates. daily_onboard_max 0 = unlimited;
// empty window start/end = all day.
type systemSettings struct {
	DailyOnboardMax    int    `json:"daily_onboard_max"`
	OnboardWindowStart string `json:"onboard_window_start"`
	OnboardWindowEnd   string `json:"onboard_window_end"`
	Version            int    `json:"version"`
}

func (a *App) loadSystemSettings() (systemSettings, error) {
	var s systemSettings
	err := a.DB.QueryRow(`SELECT daily_onboard_max,onboard_window_start,onboard_window_end,version FROM system_settings WHERE id=1`).Scan(
		&s.DailyOnboardMax, &s.OnboardWindowStart, &s.OnboardWindowEnd, &s.Version,
	)
	return s, err
}

func (a *App) systemSettingsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		methodNotAllowed(w, http.MethodGet, http.MethodPut)
		return
	}
	s, err := a.loadSystemSettings()
	if err != nil {
		writeError(w, r, storageError(err, "系统设置读取失败"))
		return
	}
	if r.Method == http.MethodGet {
		write(w, 200, s)
		return
	}
	var in systemSettings
	if !decodeJSON(w, r, &in, 4<<10) {
		return
	}
	problems := errs.Fields{}
	if in.DailyOnboardMax < 0 || in.DailyOnboardMax > 100000 {
		problems.Add("daily_onboard_max", errs.FieldOutOfRange, "须为 0–100000（0 表示不限制）")
	}
	in.OnboardWindowStart = strings.TrimSpace(in.OnboardWindowStart)
	in.OnboardWindowEnd = strings.TrimSpace(in.OnboardWindowEnd)
	startEmpty, endEmpty := in.OnboardWindowStart == "", in.OnboardWindowEnd == ""
	if startEmpty != endEmpty {
		problems.Add("onboard_window_start", errs.FieldMismatch, "开始与结束须同时填写或同时留空（留空=全天）")
		problems.Add("onboard_window_end", errs.FieldMismatch, "开始与结束须同时填写或同时留空（留空=全天）")
	}
	if !startEmpty && !onboardWindowClock.MatchString(in.OnboardWindowStart) {
		problems.Invalid("onboard_window_start", "须为 HH:MM（00:00–23:59）")
	}
	if !endEmpty && !onboardWindowClock.MatchString(in.OnboardWindowEnd) {
		problems.Invalid("onboard_window_end", "须为 HH:MM（00:00–23:59）")
	}
	if e := problems.Err(); e != nil {
		writeError(w, r, e)
		return
	}
	if in.Version != s.Version {
		fail(w, 409, "VERSION_CONFLICT", "设置已变化，请重新加载后保存")
		return
	}
	result, err := a.DB.ExecContext(r.Context(), `UPDATE system_settings SET daily_onboard_max=?,onboard_window_start=?,onboard_window_end=?,version=version+1 WHERE id=1 AND version=?`,
		in.DailyOnboardMax, in.OnboardWindowStart, in.OnboardWindowEnd, in.Version)
	if err != nil {
		writeError(w, r, storageError(err, "系统设置保存失败"))
		return
	}
	if n, _ := result.RowsAffected(); n != 1 {
		fail(w, 409, "VERSION_CONFLICT", "设置已变化，请重新加载后保存")
		return
	}
	a.auditFor(r, "system.settings.update", "system_settings", "1", systemSettingsAudit(in))
	s, err = a.loadSystemSettings()
	if err != nil {
		writeError(w, r, storageError(err, "系统设置读取失败"))
		return
	}
	write(w, 200, s)
}

func systemSettingsAudit(s systemSettings) string {
	b, _ := json.Marshal(s)
	return strings.TrimSpace(string(b))
}

// checkOnboardAllowed enforces the can_onboard flag, the global time window,
// and daily caps (global + per-user). 0 means unlimited.
func (a *App) checkOnboardAllowed(user sessionUserInfo) error {
	if !user.CanOnboard {
		return errs.New(errs.CodeOnboardDisabled)
	}
	s, err := a.loadSystemSettings()
	if err != nil {
		return storageError(err, "系统设置读取失败")
	}
	nowLocal := time.Now().In(onboardDayLocation)
	if !onboardWindowAllows(s.OnboardWindowStart, s.OnboardWindowEnd, nowLocal) {
		return errs.New(errs.CodeOnboardOutsideWindow)
	}
	dayStart, dayEnd := onboardDayBounds(nowLocal)
	startUTC := dayStart.UTC().Format(time.RFC3339)
	endUTC := dayEnd.UTC().Format(time.RFC3339)
	if s.DailyOnboardMax > 0 {
		n, err := a.countOnboardJobs(startUTC, endUTC, "")
		if err != nil {
			return storageError(err, "上号计数失败")
		}
		if n >= s.DailyOnboardMax {
			return errs.New(errs.CodeOnboardDailyLimit)
		}
	}
	if user.DailyOnboardLimit > 0 {
		n, err := a.countOnboardJobs(startUTC, endUTC, user.ID)
		if err != nil {
			return storageError(err, "上号计数失败")
		}
		if n >= user.DailyOnboardLimit {
			return errs.New(errs.CodeOnboardDailyLimit)
		}
	}
	return nil
}

func (a *App) countOnboardJobs(startUTC, endUTC, createdByUserID string) (int, error) {
	query := `SELECT COUNT(*) FROM jobs j WHERE j.kind='account.submit' AND j.created_at>=? AND j.created_at<?`
	args := []any{startUTC, endUTC}
	if createdByUserID != "" {
		query = `SELECT COUNT(*) FROM jobs j INNER JOIN accounts a ON a.id=j.account_id WHERE j.kind='account.submit' AND a.created_by_user_id=? AND j.created_at>=? AND j.created_at<?`
		args = []any{createdByUserID, startUTC, endUTC}
	}
	var n int
	err := a.DB.QueryRow(query, args...).Scan(&n)
	return n, err
}

func onboardDayBounds(nowLocal time.Time) (time.Time, time.Time) {
	start := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, nowLocal.Location())
	return start, start.Add(24 * time.Hour)
}

// onboardWindowAllows reports whether t falls in [start, end). Empty start and
// end mean all day. When start > end the window crosses midnight.
func onboardWindowAllows(start, end string, t time.Time) bool {
	start, end = strings.TrimSpace(start), strings.TrimSpace(end)
	if start == "" && end == "" {
		return true
	}
	if start == "" || end == "" || !onboardWindowClock.MatchString(start) || !onboardWindowClock.MatchString(end) {
		return true
	}
	if start == end {
		return true
	}
	mins := t.Hour()*60 + t.Minute()
	s := clockMinutes(start)
	e := clockMinutes(end)
	if s < e {
		return mins >= s && mins < e
	}
	// Overnight: e.g. 22:00–06:00.
	return mins >= s || mins < e
}

func clockMinutes(hhmm string) int {
	var h, m int
	fmt.Sscanf(hhmm, "%d:%d", &h, &m)
	return h*60 + m
}
