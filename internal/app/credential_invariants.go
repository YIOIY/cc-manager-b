package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"cc-manager/internal/errs"
)

// applyCredentialInvariants runs only at a write boundary while the account is
// locked. Validation itself never invents a device identity. An explicit ID
// wins, a saved ID for this identity comes next, and a new ID is a last resort.
func applyCredentialInvariants(auth, saved map[string]any) *errs.Error {
	if err := credentialFreshness(auth, saved); err != nil {
		return err
	}
	ids, valid := credentialDeviceIDs(auth["claude_device_ids"])
	if !valid {
		return errs.New(errs.CodeValidationFailed).WithFields(errs.FieldError{Field: "auth_json.claude_device_ids", Code: errs.FieldInvalidFormat, Message: "设备标识必须是非空文本"})
	}
	if len(ids) == 0 && sameCredentialIdentity(auth, saved) {
		if previous, ok := credentialDeviceIDs(saved["claude_device_ids"]); ok && len(previous) > 0 {
			ids = previous
		}
	}
	if len(ids) == 0 {
		ids = []any{randomDeviceID()}
	}
	auth["claude_device_ids"] = ids
	return nil
}

func sameCredentialIdentity(auth, saved map[string]any) bool {
	if len(saved) == 0 {
		return false
	}
	email, oldEmail := str(auth, "email"), str(saved, "email")
	if email == "" || oldEmail == "" || !strings.EqualFold(email, oldEmail) {
		return false
	}
	uuid, oldUUID := str(auth, "account_uuid"), str(saved, "account_uuid")
	return uuid == "" || oldUUID == "" || uuid == oldUUID
}

// credentialDeviceIDs accepts absent legacy values, but never silently removes
// invalid entries from an explicit list.
func credentialDeviceIDs(raw any) ([]any, bool) {
	switch v := raw.(type) {
	case nil:
		return nil, true
	case []any:
		out := make([]any, 0, len(v))
		for _, x := range v {
			s, ok := x.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return nil, false
			}
			out = append(out, strings.TrimSpace(s))
		}
		return out, true
	case []string:
		out := make([]any, 0, len(v))
		for _, s := range v {
			if strings.TrimSpace(s) == "" {
				return nil, false
			}
			out = append(out, strings.TrimSpace(s))
		}
		return out, true
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, true
		}
		return nil, false
	default:
		return nil, false
	}
}

func credentialFreshness(auth, saved map[string]any) *errs.Error {
	if len(saved) == 0 {
		return nil
	}
	newer, comparable := false, 0
	for _, field := range []string{"last_refresh", "expired"} {
		incoming, e1 := time.Parse(time.RFC3339, str(auth, field))
		previous, e2 := time.Parse(time.RFC3339, str(saved, field))
		if e1 != nil || e2 != nil {
			continue
		}
		comparable++
		if incoming.Before(previous) {
			return errs.Adhoc("CREDENTIAL_STALE", http.StatusConflict, "凭证快照早于已保存凭证，请重新授权或使用最新凭证").WithDetail("field", field)
		}
		if incoming.After(previous) {
			newer = true
		}
	}
	// Equal timestamps do not establish which of two different tokens came last.
	if comparable == 2 && !newer && str(auth, "access_token") != "" && str(auth, "refresh_token") != "" && str(saved, "access_token") != "" && str(saved, "refresh_token") != "" && (str(auth, "access_token") != str(saved, "access_token") || str(auth, "refresh_token") != str(saved, "refresh_token")) {
		return errs.Adhoc("CREDENTIAL_STALE", http.StatusConflict, "凭证时间相同但令牌不同，无法确认新旧，请重新授权")
	}
	return nil
}

// persistCredentialPreparation writes a one-time migration before the first
// supplier request, so even rejected deliveries reuse the same device ID.
func (a *App) persistCredentialPreparation(ctx context.Context, accountID string, auth map[string]any, version int) error {
	data, err := json.Marshal(auth)
	if err != nil {
		return err
	}
	sealed, err := a.keys.Seal(data, []byte(accountID))
	if err != nil {
		return err
	}
	result, err := a.DB.ExecContext(ctx, `UPDATE accounts SET credentials_json=?,version=version+1,updated_at=? WHERE id=? AND version=?`, sealed, now(), accountID, version)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("credential preparation version conflict")
	}
	return nil
}

func sameProxyURL(a, b string) bool {
	left, e1 := normalizeProxyURL(a)
	right, e2 := normalizeProxyURL(b)
	return e1 == nil && e2 == nil && left == right
}

// loadDeviceAnchor recovers the identity reserved by an earlier failed push.
// It only fills a missing ID for the same account; supplied and sealed IDs win.
func (a *App) loadDeviceAnchor(ctx context.Context, accountID string, auth, saved map[string]any) error {
	if ids, valid := credentialDeviceIDs(auth["claude_device_ids"]); !valid || len(ids) > 0 {
		return nil
	}
	if sameCredentialIdentity(auth, saved) {
		if ids, valid := credentialDeviceIDs(saved["claude_device_ids"]); valid && len(ids) > 0 {
			return nil
		}
	}
	var raw string
	if err := a.DB.QueryRowContext(ctx, `SELECT COALESCE(metadata_json,'{}') FROM accounts WHERE id=?`, accountID).Scan(&raw); err != nil {
		return err
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return err
	}
	anchor, _ := metadata["_credential_devices"].(map[string]any)
	if !sameCredentialIdentity(auth, anchor) {
		return nil
	}
	if ids, valid := credentialDeviceIDs(anchor["ids"]); valid && len(ids) > 0 {
		auth["claude_device_ids"] = ids
	}
	return nil
}

// persistDeviceAnchor reserves only non-token identity before a supplier push.
// This internal metadata reservation does not change the public account version:
// a rejected push can retry the same expected_version, while the final credential
// replacement still increments it. The account lock and version CAS protect it.
func (a *App) persistDeviceAnchor(ctx context.Context, accountID string, auth map[string]any, version int) error {
	anchor := map[string]any{"email": str(auth, "email"), "account_uuid": str(auth, "account_uuid"), "ids": auth["claude_device_ids"]}
	encoded, err := json.Marshal(anchor)
	if err != nil {
		return err
	}
	result, err := a.DB.ExecContext(ctx, `UPDATE accounts SET metadata_json=json_set(COALESCE(metadata_json,'{}'),'$._credential_devices',json(?)) WHERE id=? AND version=?`, string(encoded), accountID, version)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("device identity reservation version conflict")
	}
	return nil
}
