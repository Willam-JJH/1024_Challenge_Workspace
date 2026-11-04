package api

import (
	"testing"
	"time"
)

func TestFormatTimeMatchesPydantic(t *testing.T) {
	cases := []struct {
		input time.Time
		want  string
	}{
		{time.Date(2026, 11, 3, 12, 19, 5, 160024000, time.UTC),
			"2026-11-03T12:19:05.160024Z"},
		{time.Date(2026, 11, 3, 12, 19, 5, 0, time.UTC),
			"2026-11-03T12:19:05Z"},
		{time.Date(2026, 11, 3, 20, 19, 5, 0, time.FixedZone("CST", 8*3600)),
			"2026-11-03T12:19:05Z"},
	}
	for _, tc := range cases {
		if got := formatTime(tc.input); got != tc.want {
			t.Errorf("formatTime(%v) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
