package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// The account cards show the platform's RMB cost next to its USD
// equivalent. The rate is a public reference rate fetched in the
// background; requests to the sources are bare GETs that carry no account
// or session data.

const (
	exchangePair         = "USD/CNY"
	exchangeRateInterval = 6 * time.Hour
	exchangeRateFirst    = 15 * time.Second
	exchangeRateTimeout  = 15 * time.Second
	exchangeRateMaxBody  = 64 << 10
	// A USD/CNY rate outside this range is a broken response, not a rate.
	exchangeRateMin = 1.0
	exchangeRateMax = 20.0
)

type rateSource struct{ Name, URL string }

// defaultRateSources are tried in order; both answer {"rates":{"CNY":n}}.
// The first is the ECB reference rate, the second a fallback.
var defaultRateSources = []rateSource{
	{Name: "ecb", URL: "https://api.frankfurter.dev/v1/latest?base=USD&symbols=CNY"},
	{Name: "open-er-api", URL: "https://open.er-api.com/v6/latest/USD"},
}

var exchangeRateClient = &http.Client{Timeout: exchangeRateTimeout}

// fetchRate reads one source. date is the day the source says the rate is
// for, or "" when it does not say.
func fetchRate(ctx context.Context, src rateSource) (rate float64, date string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := exchangeRateClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("%s: HTTP %d", src.Name, resp.StatusCode)
	}
	var body struct {
		Date  string             `json:"date"`
		Rates map[string]float64 `json:"rates"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, exchangeRateMaxBody)).Decode(&body); err != nil {
		return 0, "", fmt.Errorf("%s: unreadable response", src.Name)
	}
	rate, ok := body.Rates["CNY"]
	if !ok || rate < exchangeRateMin || rate > exchangeRateMax {
		return 0, "", fmt.Errorf("%s: no usable CNY rate", src.Name)
	}
	if _, e := time.Parse("2006-01-02", body.Date); e != nil {
		body.Date = ""
	}
	return rate, body.Date, nil
}

// refreshExchangeRate stores the first usable rate. When no source answers
// the stored rate is left as it is and the last error is returned.
func (a *App) refreshExchangeRate(ctx context.Context) error {
	sources := a.rateSources
	if sources == nil {
		sources = defaultRateSources
	}
	var lastErr error = errors.New("no exchange rate source configured")
	for _, src := range sources {
		callCtx, cancel := context.WithTimeout(ctx, exchangeRateTimeout)
		rate, date, err := fetchRate(callCtx, src)
		cancel()
		if err != nil {
			lastErr = err
			continue
		}
		fetched := time.Now().UTC()
		if date == "" {
			date = fetched.Format("2006-01-02")
		}
		_, err = a.DB.ExecContext(ctx, `INSERT INTO exchange_rates(pair,rate,source,rate_date,fetched_at) VALUES(?,?,?,?,?)
ON CONFLICT(pair) DO UPDATE SET rate=excluded.rate,source=excluded.source,rate_date=excluded.rate_date,fetched_at=excluded.fetched_at`,
			exchangePair, rate, src.Name, date, fetched.Format(time.RFC3339))
		return err
	}
	return lastErr
}

// exchangeRateGet answers GET /api/v1/exchange-rate for any signed-in user.
// rate is null until the first successful fetch.
func (a *App) exchangeRateGet(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"pair": exchangePair, "rate": nil, "source": "", "rate_date": "", "fetched_at": ""}
	var rate float64
	var source, date, fetched string
	err := a.DB.QueryRowContext(r.Context(), `SELECT rate,source,rate_date,fetched_at FROM exchange_rates WHERE pair=?`, exchangePair).Scan(&rate, &source, &date, &fetched)
	switch {
	case err == nil:
		out["rate"], out["source"], out["rate_date"], out["fetched_at"] = rate, source, date, fetched
	case !errors.Is(err, sql.ErrNoRows):
		writeError(w, r, storageError(err, "汇率读取失败"))
		return
	}
	write(w, http.StatusOK, out)
}
