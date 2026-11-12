package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"cc-manager/internal/errs"
	"cc-manager/internal/supplier"
)

// The console profile fields a user may edit, in the order the platform's
// own form shows them. Required ones can be changed but never emptied.
var (
	profileRequiredFields = []string{"plan", "register_time", "plan_start_time", "plan_end_time", "register_location"}
	profileOptionalFields = []string{"balance", "payment_method", "register_device", "recharge_time", "warranty_period", "remark"}
	profileDateFields     = map[string]bool{"register_time": true, "plan_start_time": true, "plan_end_time": true, "recharge_time": true}
	// profileLocalMetadata are the optional fields mirrored into
	// accounts.metadata_json (the supplier-defined keys the onboarding form
	// already keeps there).
	profileLocalMetadata = []string{"balance", "payment_method", "register_device", "recharge_time", "warranty_period", "remark"}
)

// snapshotProfile is the editable part of a remote snapshot keyed the way
// the request body and the platform block name it.
func snapshotProfile(x remoteRow) map[string]string {
	return map[string]string{
		"plan": x.Plan, "register_time": x.RegisterTime, "plan_start_time": x.PlanStart, "plan_end_time": x.PlanEnd, "register_location": x.RegisterLocation,
		"balance": x.Balance, "payment_method": x.PaymentMethod, "register_device": x.RegisterDevice, "recharge_time": x.RechargeTime, "warranty_period": x.WarrantyPeriod, "remark": x.Remark,
	}
}

// buildProfileUpdate overlays changes on the snapshot: every field the
// platform expects is sent, untouched ones with their current value (empty
// stays empty).
func buildProfileUpdate(snapshot remoteRow, changes map[string]string) supplier.WebProfileUpdate {
	v := snapshotProfile(snapshot)
	for k, val := range changes {
		v[k] = val
	}
	return supplier.WebProfileUpdate{
		Provider: snapshot.Provider, Model: snapshot.Model, Email: snapshot.Email, Remark: v["remark"],
		Plan: v["plan"], RegisterTime: v["register_time"], PlanStart: v["plan_start_time"], PlanEnd: v["plan_end_time"], RegisterLocation: v["register_location"],
		Balance: v["balance"], PaymentMethod: v["payment_method"], RegisterDevice: v["register_device"], RechargeTime: v["recharge_time"], WarrantyPeriod: v["warranty_period"],
	}
}

// validateProfileChanges reports every problem with the submitted changes.
func validateProfileChanges(changes map[string]string) *errs.Error {
	var problems errs.Fields
	allowed := map[string]bool{}
	for _, k := range append(append([]string{}, profileRequiredFields...), profileOptionalFields...) {
		allowed[k] = true
	}
	required := map[string]bool{}
	for _, k := range profileRequiredFields {
		required[k] = true
	}
	keys := make([]string, 0, len(changes))
	for k := range changes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		val := strings.TrimSpace(changes[k])
		switch {
		case !allowed[k]:
			problems.Add(k, errs.FieldUnsupportedValue, "不是可修改的平台资料字段")
		case required[k] && val == "":
			problems.Required(k)
		case profileDateFields[k] && val != "" && !validDate(val):
			problems.Invalid(k, "日期格式应为 YYYY-MM-DD")
		case k == "balance" && val != "":
			if n, err := strconv.ParseFloat(val, 64); err != nil || n < 0 || n > 100 {
				problems.Add(k, errs.FieldOutOfRange, "余额应在 0 到 100 之间")
			}
		}
	}
	if err := problems.Err(); err != nil {
		return err
	}
	return nil
}

// linkedSnapshot returns the remote record the account is linked to.
func (a *App) linkedSnapshot(ctx context.Context, accountID string) (remoteRow, error) {
	row := a.DB.QueryRowContext(ctx, `SELECT `+remoteColumns+` FROM platform_links l JOIN platform_remote_accounts r ON r.connection_id=l.connection_id AND r.upstream_id=l.upstream_id WHERE l.account_id=? AND l.ingestion_state='accepted' ORDER BY l.updated_at DESC LIMIT 1`, accountID)
	return scanRemoteRow(row)
}

// profileConflict answers like every other optimistic-lock failure here.
func profileConflict(w http.ResponseWriter, msg string) {
	fail(w, http.StatusConflict, errs.CodeVersionConflict, msg)
}

// platformProfileUpdate answers POST /api/v1/accounts/{id}/platform-profile:
// the one write the console channel allows. The caller proves it edited the
// current snapshot (base), the platform gets the whole record, and the
// snapshot plus the local account are brought in line afterwards.
func (a *App) platformProfileUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Changes map[string]string `json:"changes"`
		Base    map[string]string `json:"base"`
	}
	if !decodeJSON(w, r, &in, defaultBodyLimit) {
		return
	}
	changes := map[string]string{}
	for k, v := range in.Changes {
		changes[k] = strings.TrimSpace(v)
	}
	if err := validateProfileChanges(changes); err != nil {
		writeError(w, r, err)
		return
	}
	if len(changes) == 0 {
		fail(w, http.StatusUnprocessableEntity, errs.CodeValidationFailed, "没有可更新字段")
		return
	}
	ctx := r.Context()
	snapshot, err := a.linkedSnapshot(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		profileConflict(w, "账号没有可修改的平台记录")
		return
	}
	if err != nil {
		writeError(w, r, storageError(err, "平台账号读取失败"))
		return
	}
	if snapshot.MissingSince != "" || snapshot.SupersededBy != "" {
		profileConflict(w, "平台记录已变化，请刷新后重试")
		return
	}
	current := snapshotProfile(snapshot)
	changed := make([]string, 0, len(changes))
	for k, v := range changes {
		base, ok := in.Base[k]
		if !ok || strings.TrimSpace(base) != current[k] {
			profileConflict(w, "平台记录已变化，请刷新后重试")
			return
		}
		if v != current[k] {
			changed = append(changed, k)
		}
	}
	for k, base := range in.Base {
		if cur, ok := current[k]; ok && strings.TrimSpace(base) != cur {
			profileConflict(w, "平台记录已变化，请刷新后重试")
			return
		}
	}
	if len(changed) == 0 {
		fail(w, http.StatusUnprocessableEntity, errs.CodeValidationFailed, "资料没有变化")
		return
	}
	sort.Strings(changed)

	client, _, err := a.webClientFor(ctx, snapshot.ConnectionID)
	if err != nil {
		var local *errs.Error
		if errors.As(err, &local) {
			writeError(w, r, local)
		} else {
			writeError(w, r, storageError(err, "平台连接读取失败"))
		}
		return
	}
	upstreamID, _ := strconv.ParseInt(snapshot.UpstreamID, 10, 64)
	update := buildProfileUpdate(snapshot, changes)

	lock := a.webSyncLock(snapshot.ConnectionID)
	if err = lockWithContext(ctx, lock); err != nil {
		writeError(w, r, errs.New(errs.CodePlatformUnavailable).WithCause(err))
		return
	}
	defer lock.Unlock()
	callCtx, cancel := context.WithTimeout(ctx, supplierRequestTimeout)
	err = client.UpdateAccountProfile(callCtx, upstreamID, update)
	cancel()
	if err != nil {
		a.noteWebLogin(ctx, snapshot.ConnectionID, err)
		writeError(w, r, webUpstreamError(err))
		return
	}

	verified, hint := a.refreshSnapshotAfterUpdate(ctx, client, snapshot, update)
	if err = a.writeBackLocalProfile(ctx, id, update); err != nil {
		writeError(w, r, storageError(err, "本地账号更新失败"))
		return
	}
	a.auditFor(r, "account.platform_profile_update", "account", id, strings.Join(changed, ","))

	var block map[string]any
	if fresh, err := a.linkedSnapshot(ctx, id); err == nil {
		web, _ := a.webChannelState(ctx, fresh.ConnectionID)
		block = platformBlock(fresh, "web", web)
	}
	out := map[string]any{"verified": verified, "changed": changed, "platform": block}
	if hint != "" {
		out["hint"] = hint
	}
	write(w, http.StatusOK, out)
}

// refreshSnapshotAfterUpdate re-reads the account from the console so the
// snapshot shows what the platform really stored. When the listing fails or
// no longer carries the account, the submitted values are written to the
// snapshot instead and the caller is told the change is unverified.
func (a *App) refreshSnapshotAfterUpdate(ctx context.Context, client *supplier.WebClient, snapshot remoteRow, update supplier.WebProfileUpdate) (bool, string) {
	listCtx, cancel := context.WithTimeout(ctx, 2*supplierRequestTimeout)
	accounts, _, _, err := client.ListAccounts(listCtx)
	cancel()
	if err == nil {
		for _, r := range accounts {
			if strconv.FormatInt(r.ID, 10) != snapshot.UpstreamID {
				continue
			}
			if err = a.upsertRemoteAccount(ctx, snapshot.ConnectionID, r, snapshot.AccountID, false, snapshot.SupersededBy, now()); err == nil {
				return true, ""
			}
			break
		}
	}
	_, _ = a.DB.ExecContext(ctx, `UPDATE platform_remote_accounts SET plan=?,register_time=?,plan_start_time=?,plan_end_time=?,register_location=?,balance=?,payment_method=?,register_device=?,recharge_time=?,warranty_period=?,remark=? WHERE connection_id=? AND upstream_id=?`,
		update.Plan, update.RegisterTime, update.PlanStart, update.PlanEnd, update.RegisterLocation, update.Balance, update.PaymentMethod, update.RegisterDevice, update.RechargeTime, update.WarrantyPeriod, update.Remark, snapshot.ConnectionID, snapshot.UpstreamID)
	return false, "平台已接受修改，但刷新平台记录失败；下次网页同步后会自动核对"
}

// writeBackLocalProfile mirrors the platform record into the local account:
// the five core fields as columns (plan only when it is one this system
// knows) and the optional ones inside metadata_json.
func (a *App) writeBackLocalProfile(ctx context.Context, accountID string, update supplier.WebProfileUpdate) error {
	var plan, metadataJSON string
	if err := a.DB.QueryRowContext(ctx, `SELECT plan,metadata_json FROM accounts WHERE id=?`, accountID).Scan(&plan, &metadataJSON); err != nil {
		return err
	}
	if validPlan(update.Plan) {
		plan = update.Plan
	}
	meta := map[string]any{}
	_ = json.Unmarshal([]byte(metadataJSON), &meta)
	if meta == nil {
		meta = map[string]any{}
	}
	values := map[string]string{"balance": update.Balance, "payment_method": update.PaymentMethod, "register_device": update.RegisterDevice, "recharge_time": update.RechargeTime, "warranty_period": update.WarrantyPeriod, "remark": update.Remark}
	for _, k := range profileLocalMetadata {
		if values[k] == "" {
			delete(meta, k)
		} else {
			meta[k] = values[k]
		}
	}
	encoded, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	_, err = a.DB.ExecContext(ctx, `UPDATE accounts SET plan=?,register_time=?,plan_start_time=?,plan_end_time=?,register_location=?,metadata_json=?,version=version+1,updated_at=? WHERE id=?`,
		plan, update.RegisterTime, update.PlanStart, update.PlanEnd, update.RegisterLocation, string(encoded), now(), accountID)
	return err
}
