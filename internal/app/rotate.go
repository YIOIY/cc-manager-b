package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// RotationReport counts rows re-encrypted (or, in check mode, still pending) per table.
type RotationReport struct {
	ActiveKeyID string         `json:"active_key_id"`
	Pending     map[string]int `json:"pending"`
	Rewritten   map[string]int `json:"rewritten"`
	DryRun      bool           `json:"dry_run"`
}

type secretColumn struct {
	table, idColumn, column string
	aad                     func(id string) string
	skipEmptyJSON           bool
}

var rotatedColumns = []secretColumn{
	{"accounts", "id", "credentials_json", func(id string) string { return id }, true},
	{"account_secrets", "account_id", "password_secret", func(id string) string { return id }, false},
	{"proxies", "id", "password_secret", func(id string) string { return id }, false},
	{"platform_connections", "id", "token_secret", func(id string) string { return id }, false},
	{"proxy_probe_settings", "id", "password_secret", func(string) string { return proxySettingsAAD }, false},
}

// RotateKeys re-encrypts every stored secret with the active key. Rows sealed
// with an unknown key abort the whole transaction so a partial rotation can
// never leave some rows unreadable. With dryRun it only reports what would change.
func (a *App) RotateKeys(ctx context.Context, dryRun bool) (RotationReport, error) {
	report := RotationReport{Pending: map[string]int{}, Rewritten: map[string]int{}, DryRun: dryRun}
	if a.keys == nil {
		return report, errors.New("CC_MANAGER_ENCRYPTION_KEY is not configured")
	}
	report.ActiveKeyID = a.keys.ActiveID()
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return report, err
	}
	defer tx.Rollback()
	for _, col := range rotatedColumns {
		rows, err := tx.QueryContext(ctx, fmt.Sprintf(`SELECT %s,%s FROM %s WHERE %s<>''`, col.idColumn, col.column, col.table, col.column))
		if err != nil {
			return report, err
		}
		type pending struct{ id, value string }
		var items []pending
		for rows.Next() {
			var p pending
			if err = rows.Scan(&p.id, &p.value); err != nil {
				break
			}
			if col.skipEmptyJSON && (p.value == "{}" || p.value == "") {
				continue
			}
			if a.keys.NeedsRotation(p.value) {
				items = append(items, p)
			}
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return report, err
		}
		report.Pending[col.table] = len(items)
		if dryRun {
			continue
		}
		for _, p := range items {
			resealed, err := a.keys.Reseal(p.value, []byte(col.aad(p.id)))
			if err != nil {
				return report, fmt.Errorf("%s %s: %w", col.table, p.id, err)
			}
			if _, err = tx.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET %s=? WHERE %s=?`, col.table, col.column, col.idColumn), resealed, p.id); err != nil {
				return report, err
			}
			report.Rewritten[col.table]++
		}
	}
	if err = a.rotateImportPreviews(ctx, tx, &report, dryRun); err != nil {
		return report, err
	}
	if dryRun {
		return report, nil
	}
	if err = tx.Commit(); err != nil {
		return report, err
	}
	a.audit("security.key_rotation", "encryption_key", report.ActiveKeyID, fmt.Sprintf("%v", report.Rewritten))
	return report, nil
}

// Import previews embed per-row secrets inside rows_json; they are short-lived
// but are rotated too so a stale preview never pins an old key.
func (a *App) rotateImportPreviews(ctx context.Context, tx *sql.Tx, report *RotationReport, dryRun bool) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,rows_json FROM imports WHERE state='preview'`)
	if err != nil {
		return err
	}
	type preview struct{ id, body string }
	var previews []preview
	for rows.Next() {
		var p preview
		if err = rows.Scan(&p.id, &p.body); err != nil {
			break
		}
		previews = append(previews, p)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, p := range previews {
		var items []map[string]any
		if json.Unmarshal([]byte(p.body), &items) != nil {
			continue
		}
		changed := false
		for _, item := range items {
			for field, suffix := range map[string]string{"password_secret": ":password", "credentials_secret": ":credentials"} {
				value, ok := item[field].(string)
				if !ok || value == "" || !a.keys.NeedsRotation(value) {
					continue
				}
				changed = true
				report.Pending["imports"]++
				if dryRun {
					continue
				}
				resealed, err := a.keys.Reseal(value, []byte(p.id+suffix))
				if err != nil {
					return fmt.Errorf("imports %s: %w", p.id, err)
				}
				item[field] = resealed
			}
		}
		if !changed || dryRun {
			continue
		}
		encoded, _ := json.Marshal(items)
		if _, err = tx.ExecContext(ctx, `UPDATE imports SET rows_json=? WHERE id=?`, string(encoded), p.id); err != nil {
			return err
		}
		report.Rewritten["imports"]++
	}
	return nil
}
