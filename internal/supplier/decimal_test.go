package supplier

import "testing"

func TestDecimalFenBoundaries(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int64
		ok   bool
	}{
		{"0", 0, true},
		{"12.34", 1234, true},
		{"0.01", 1, true},
		{"1e2", 10000, true},
		{"92233720368547758.07", 9223372036854775807, true},
		{"92233720368547758.08", 0, false}, // overflows int64 fen
		{"1.234", 0, false},                // more than two decimals
		{"abc", 0, false},
		{"", 0, false},
	} {
		got, err := decimalFen(tc.in)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("decimalFen(%q) = %d, %v; want %d ok=%v", tc.in, got, err, tc.want, tc.ok)
		}
	}
}
