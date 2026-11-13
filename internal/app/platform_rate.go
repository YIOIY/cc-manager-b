package app

import (
	"strconv"

	"cc-manager/internal/errs"
)

// The supplier platform prices overseas vendors in US dollars and reports
// every amount in RMB at a fixed rate it publishes on its own pages (7.2
// since September 2026, 7.3 before). Its API carries no rate, so the rate
// is kept per connection and the account card divides the RMB cost by it,
// exactly as the platform's console does.

const (
	defaultUSDCNYRate = 7.2
	minUSDCNYRate     = 1.0
	maxUSDCNYRate     = 20.0
)

// usdRateInput reads the optional "usd_cny_rate" of a connection request.
// present is false when the request does not mention it.
func usdRateInput(in map[string]any, problems *errs.Fields) (rate float64, present bool) {
	raw, ok := in["usd_cny_rate"]
	if !ok {
		return 0, false
	}
	rate, isNumber := raw.(float64)
	if !isNumber || rate < minUSDCNYRate || rate > maxUSDCNYRate {
		problems.Add("usd_cny_rate", errs.FieldOutOfRange, "美元汇率应为 1 到 20 之间的数字，例如 7.2")
		return 0, true
	}
	return rate, true
}

func formatRate(rate float64) string { return strconv.FormatFloat(rate, 'f', -1, 64) }
