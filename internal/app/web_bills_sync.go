package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"cc-manager/internal/errs"
	"cc-manager/internal/supplier"
)

// Per-model bills over the supplier web console: every 2 hours the last 7
// days of every listed delivery are read in batches of 50 and mirrored into
// platform_bill_models. Today's rows stay non-final until the day is over.

const (
	webBillsInterval  = 2 * time.Hour
	webBillWindowDays = 7
	webBillBatch      = 50
)

// webBillWindow returns the inclusive [today-6, today] window in the
// connection's timezone.
func webBillWindow(at time.Time, loc *time.Location) (start, end string) {
	today := at.In(loc)
	return today.AddDate(0, 0, -(webBillWindowDays - 1)).Format("2006-01-02"), today.Format("2006-01-02")
}

func (a *App) syncAllWebBills(ctx context.Context) error {
	ids, err := a.webSyncCandidates(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, id := range ids {
		if _, _, skip, e := a.syncBackoff(ctx, id, "web-bills", time.Now()); e != nil {
			failures = append(failures, e)
			continue
		} else if skip {
			continue
		}
		if e := a.syncWebBills(ctx, id); e != nil {
			failures = append(failures, e)
		}
	}
	return errors.Join(failures...)
}

// syncWebBills runs one bill sync for a connection: one request per batch of
// 50 listed deliveries. A batch that fails is counted and skipped; its rows
// are left untouched.
func (a *App) syncWebBills(ctx context.Context, connectionID string) error {
	lock := a.webSyncLock(connectionID)
	if !lock.TryLock() {
		return nil
	}
	defer lock.Unlock()
	_, finish := a.startWebRun(ctx, connectionID, "web-bills")
	client, conn, err := a.webClientFor(ctx, connectionID)
	if err != nil {
		finish("failed", upstreamCode(err), err.Error(), "", 0, 0)
		return err
	}
	loc, err := loadBillLocation(conn.Timezone)
	if err != nil {
		finish("failed", errs.CodeConnectionInvalid, "connection timezone invalid", "", 0, 0)
		return err
	}
	start, end := webBillWindow(time.Now(), loc)
	today := end
	rows, err := a.DB.QueryContext(ctx, `SELECT upstream_id,account_id FROM platform_remote_accounts WHERE connection_id=? AND missing_since='' ORDER BY upstream_id`, connectionID)
	if err != nil {
		finish("failed", errs.CodeInternalError, "remote accounts unavailable", "", 0, 0)
		return err
	}
	var ids []int64
	accountOf := map[int64]string{}
	for rows.Next() {
		var upstream, account string
		if err = rows.Scan(&upstream, &account); err != nil {
			break
		}
		id, perr := strconv.ParseInt(upstream, 10, 64)
		if perr != nil {
			continue
		}
		ids = append(ids, id)
		accountOf[id] = account
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		finish("failed", errs.CodeInternalError, "remote accounts unavailable", "", 0, 0)
		return err
	}
	request, _ := json.Marshal(map[string]any{"start": start, "end": end, "deliveries": len(ids)})
	processed, failed := 0, 0
	var firstErr error
	fetched := now()
	for i := 0; i < len(ids); i += webBillBatch {
		j := i + webBillBatch
		if j > len(ids) {
			j = len(ids)
		}
		batch := ids[i:j]
		billCtx, cancel := context.WithTimeout(ctx, supplierRequestTimeout)
		result, e := client.QueryModelBills(billCtx, batch, start, end)
		cancel()
		if e != nil {
			a.noteWebLogin(ctx, connectionID, e)
			if firstErr == nil {
				firstErr = e
			}
			failed += len(batch)
			continue
		}
		n, e := a.storeModelBills(ctx, connectionID, batch, accountOf, result, start, end, today, fetched)
		if e != nil {
			slog.Error("model bill store failed", "connection", connectionID, "error", e)
			failed += len(batch)
			continue
		}
		processed += n
	}
	state, code, message := "succeeded", "", ""
	if failed > 0 {
		state = "partial"
		if firstErr != nil {
			code, message = upstreamCode(firstErr), firstErr.Error()
		} else {
			code = errs.CodeInternalError
		}
		if processed == 0 && len(ids) > 0 {
			state = "failed"
		}
	}
	finish(state, code, message, string(request), processed, failed)
	return firstErr
}

// storeModelBills replaces the window's rows for one batch of deliveries
// with what the console returned, in one transaction: rows the console
// still reports are upserted (generation+1), rows it no longer reports are
// deleted. Returns the number of rows written.
func (a *App) storeModelBills(ctx context.Context, connectionID string, batch []int64, accountOf map[int64]string, result []supplier.WebBillRow, start, end, today, fetched string) (int, error) {
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	sourceRange, _ := json.Marshal(map[string]string{"start": start, "end": end})
	keep := map[string]bool{}
	for _, r := range result {
		if r.AccountID <= 0 || r.Date < start || r.Date > end {
			continue
		}
		upstream := fmt.Sprint(r.AccountID)
		final := 0
		if r.Date < today {
			final = 1
		}
		if _, err = tx.Exec(`INSERT INTO platform_bill_models(id,connection_id,upstream_id,account_id,bill_date,model_name,bill_model_name,cost_fen,source_range_json,fetched_at,generation,final) VALUES(?,?,?,?,?,?,?,?,?,?,1,?)
ON CONFLICT(connection_id,upstream_id,bill_date,model_name) DO UPDATE SET account_id=CASE WHEN excluded.account_id<>'' THEN excluded.account_id ELSE platform_bill_models.account_id END,bill_model_name=excluded.bill_model_name,cost_fen=excluded.cost_fen,source_range_json=excluded.source_range_json,fetched_at=excluded.fetched_at,generation=platform_bill_models.generation+1,final=excluded.final`,
			newID(), connectionID, upstream, accountOf[r.AccountID], r.Date, r.Model, r.BillModel, r.CostFen, string(sourceRange), fetched, final); err != nil {
			return 0, err
		}
		keep[upstream+"/"+r.Date+"/"+r.Model] = true
	}
	// Delete window rows of this batch that the console no longer reports.
	args := []any{connectionID, start, end}
	for _, id := range batch {
		args = append(args, fmt.Sprint(id))
	}
	existing, err := tx.Query(`SELECT id,upstream_id,bill_date,model_name FROM platform_bill_models WHERE connection_id=? AND bill_date>=? AND bill_date<=? AND upstream_id IN (`+marks(len(batch))+`)`, args...)
	if err != nil {
		return 0, err
	}
	var stale []string
	for existing.Next() {
		var id, upstream, day, model string
		if err = existing.Scan(&id, &upstream, &day, &model); err != nil {
			break
		}
		if !keep[upstream+"/"+day+"/"+model] {
			stale = append(stale, id)
		}
	}
	if err == nil {
		err = existing.Err()
	}
	existing.Close()
	if err != nil {
		return 0, err
	}
	for _, id := range stale {
		if _, err = tx.Exec(`DELETE FROM platform_bill_models WHERE id=?`, id); err != nil {
			return 0, err
		}
	}
	return len(keep), tx.Commit()
}
