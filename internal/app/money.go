package app

import (
	"fmt"
	"math"
)

// fenToUsdCents converts RMB fen to US cents at the platform's fixed rate
// (RMB per dollar). Storage stays in fen; API responses use cents / usd text.
func fenToUsdCents(fen int64, rate float64) int64 {
	if rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
		return 0
	}
	return int64(math.Round(float64(fen) / rate))
}

// yuanText formats an integer amount with two decimal places (fen or cents).
func yuanText(amount int64) string {
	if amount < 0 {
		return "-" + yuanText(-amount)
	}
	return fmt.Sprintf("%d.%02d", amount/100, amount%100)
}

func usdText(cents int64) string { return yuanText(cents) }

// costUSDFields is the API money shape: display string plus integer cents.
func costUSDFields(fen int64, rate float64) (cents int64, text string) {
	cents = fenToUsdCents(fen, rate)
	return cents, usdText(cents)
}
