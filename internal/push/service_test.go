package push

import (
	"net/http"
	"testing"
	"time"

	"go.uber.org/zap"
)

// A VAPID token carries an absolute expiry, so a wrong clock on this host
// yields a well-formed token the gateway still rejects — Apple reports that as
// BadJwtToken, which reads like a signing fault and sends you hunting the keys.
// Reporting the measured skew is what distinguishes the two.
func TestClockSkew(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   bool
	}{
		{"missing", "", false},
		{"unparseable", "not a date", false},
		{"in sync", time.Now().UTC().Format(http.TimeFormat), false},
		{"host 6h ahead", time.Now().UTC().Add(-6 * time.Hour).Format(http.TimeFormat), true},
		{"host 6h behind", time.Now().UTC().Add(6 * time.Hour).Format(http.TimeFormat), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			skew, ok := clockSkew(c.header)
			if ok != c.want {
				t.Fatalf("clockSkew(%q) reported %v, want %v", c.header, ok, c.want)
			}
			if !ok {
				return
			}
			// Direction has to be right or the advice points the wrong way.
			if skew > 0 && aheadOrBehind(skew) != "ahead of" {
				t.Errorf("positive skew %v described as %q", skew, aheadOrBehind(skew))
			}
			if skew < 0 && aheadOrBehind(skew) != "behind" {
				t.Errorf("negative skew %v described as %q", skew, aheadOrBehind(skew))
			}
		})
	}
}

func testLogger() *zap.Logger { return zap.NewNop() }
