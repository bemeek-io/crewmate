package transactionsapi

import (
	"testing"
	"time"
)

// The since/until filters are fed by JS Date.toISOString(), which always emits
// milliseconds. Go's RFC3339 layout has no fractional part, so this pins down
// that such timestamps still parse — otherwise every timeframe filter would
// come back 400 with no obvious cause.
func TestRFC3339AcceptsBrowserTimestamps(t *testing.T) {
	for _, s := range []string{
		"2026-08-17T17:45:00.000Z", // Date.toISOString()
		"2026-08-17T17:45:00Z",
		"2026-08-17T11:45:00-06:00",
	} {
		if _, err := time.Parse(time.RFC3339, s); err != nil {
			t.Errorf("time.Parse(RFC3339, %q) = %v, want no error", s, err)
		}
	}
}

func TestRangesAreSelectableAndBounded(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	for _, key := range []string{"1w", "1m", "3m", "6m", "1y"} {
		spec, ok := ranges[key]
		if !ok {
			t.Fatalf("range %q missing", key)
		}
		start := spec.start(now)
		if !start.Before(now) {
			t.Errorf("range %q starts at %v, not before %v", key, start, now)
		}
		// A year is the longest window the UI offers.
		if start.Before(now.AddDate(-1, 0, 0)) {
			t.Errorf("range %q reaches past one year: %v", key, start)
		}
	}
	if _, ok := ranges[defaultRange]; !ok {
		t.Errorf("defaultRange %q is not a real range", defaultRange)
	}
}
