package rollup

import (
	"testing"
	"time"
)

func TestParseSpanBounds(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  time.Duration
	}{
		{"1m", time.Minute}, {"90m", 90 * time.Minute}, {"24h", 24 * time.Hour},
		{"800d", MaxSpan}, {"19200h", MaxSpan}, {"1152000m", MaxSpan},
		{"000001m", time.Minute},
		{"", 0}, {"1", 0}, {"0d", 0}, {"1w", 0}, {"1H", 0}, {"+1h", 0},
		{"-1h", 0}, {"1.5h", 0}, {" 1h", 0}, {"1h ", 0}, {"１h", 0},
		{"801d", 0}, {"19201h", 0}, {"1152001m", 0}, {"213504d", 0},
		{"213505d", 0}, {"9999999h", 0}, {"99999999m", 0},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := ParseSpan(tc.input)
			if tc.want == 0 && err == nil || tc.want != 0 && (err != nil || got != tc.want) {
				t.Fatalf("ParseSpan(%q) = %s, %v; want %s", tc.input, got, err, tc.want)
			}
		})
	}
}
