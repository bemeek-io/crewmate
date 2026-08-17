package categorize

import (
	"testing"
	"time"
)

func TestMerchantKey(t *testing.T) {
	cases := map[string]string{
		"  Costco  ":        "costco",
		"TARGET #1234":      "target",
		"Walmart Store 042": "walmart",
		"Trader  Joe's":     "trader joe's",
		"SHELL STR 991":     "shell",
		"Netflix.com":       "netflix.com",
		"":                  "",
		"7-Eleven #22 ":     "7-eleven",
	}
	for in, want := range cases {
		if got := MerchantKey(in); got != want {
			t.Errorf("MerchantKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClassifyCadence(t *testing.T) {
	cases := []struct {
		days float64
		want string
	}{
		{7, "weekly"}, {6, "weekly"}, {14, "biweekly"}, {30, "monthly"},
		{31, "monthly"}, {27, "monthly"}, {91, "quarterly"}, {365, "yearly"},
		{3, "unknown"}, {50, "unknown"}, {200, "unknown"},
	}
	for _, c := range cases {
		if got, _ := classifyCadence(c.days); got != c.want {
			t.Errorf("classifyCadence(%v) = %q, want %q", c.days, got, c.want)
		}
	}
}

func TestMedianInterval(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	times := []time.Time{base, base.AddDate(0, 0, 30), base.AddDate(0, 0, 61), base.AddDate(0, 0, 91)}
	med, ok := medianInterval(times)
	if !ok {
		t.Fatal("expected ok")
	}
	if med < 29 || med > 31 {
		t.Fatalf("median = %v, want ~30", med)
	}
	if _, ok := medianInterval(times[:1]); ok {
		t.Fatal("single occurrence should not produce an interval")
	}
}
