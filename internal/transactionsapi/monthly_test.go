package transactionsapi

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bemeek-io/crewmate/internal/store"
)

func TestMonthSpanCrossesYearAndKeepsZone(t *testing.T) {
	den, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 2, 14, 22, 0, 0, 0, den)
	keys, start, end := monthSpan(now, 6)

	want := []string{"2025-09", "2025-10", "2025-11", "2025-12", "2026-01", "2026-02"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
	if !start.Equal(time.Date(2025, 9, 1, 0, 0, 0, 0, den)) {
		t.Errorf("start = %v, want Denver midnight on Sep 1", start)
	}
	if !end.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, den)) {
		t.Errorf("end = %v, want Denver midnight on Mar 1", end)
	}
}

func month(key string) time.Time {
	t, _ := time.Parse("2006-01", key)
	return t
}

func TestBuildMonthly(t *testing.T) {
	groceries := uuid.New()
	months := []string{"2026-04", "2026-05", "2026-06", "2026-07", "2026-08", "2026-09"}
	rows := []store.MonthlySpendRow{
		// April has nothing: the family's history starts in May.
		{Month: month("2026-05"), CategoryID: &groceries, CategoryName: "Groceries", Cents: 100_00},
		{Month: month("2026-05"), CategoryName: "", Cents: 50_00},
		{Month: month("2026-06"), CategoryID: &groceries, CategoryName: "Groceries", Cents: 200_00},
		{Month: month("2026-07"), CategoryID: &groceries, CategoryName: "Groceries", Cents: 300_00},
		{Month: month("2026-08"), CategoryID: &groceries, CategoryName: "Groceries", Cents: 400_00},
		{Month: month("2026-09"), CategoryID: &groceries, CategoryName: "Groceries", Cents: 100_00},
	}
	rep := buildMonthly(months, rows, 10, 30)

	if rep.HistoryStart != 1 {
		t.Errorf("HistoryStart = %d, want 1", rep.HistoryStart)
	}
	wantTotal := []int64{0, 150_00, 200_00, 300_00, 400_00, 100_00}
	if !reflect.DeepEqual(rep.Total.Cents, wantTotal) {
		t.Errorf("total = %v, want %v", rep.Total.Cents, wantTotal)
	}
	// May–Aug only: April predates history, September is in progress.
	if got, want := rep.Total.AvgCents, float64(150_00+200_00+300_00+400_00)/4; got != want {
		t.Errorf("total avg = %v, want %v", got, want)
	}
	// 10 of 30 days in, $100 spent → on pace for $300.
	if rep.Total.ProjectedCents != 300_00 {
		t.Errorf("projected = %d, want 30000", rep.Total.ProjectedCents)
	}

	if len(rep.Categories) != 2 || rep.Categories[0].CategoryName != "Groceries" {
		t.Fatalf("categories not biggest-first: %+v", rep.Categories)
	}
	g := rep.Categories[0]
	if g.TotalCents != 1100_00 {
		t.Errorf("groceries total = %d", g.TotalCents)
	}
	// Groceries climbs exactly $100/month from x=1..4, so the fit is exact.
	if g.Trend == nil {
		t.Fatal("groceries has no trend")
	}
	if math.Abs(g.Trend.SlopeCents-100_00) > 1e-6 || math.Abs(g.Trend.InterceptCents) > 1e-6 {
		t.Errorf("trend = %+v, want slope 10000 intercept 0", *g.Trend)
	}
	// Uncategorized is its own series, with months it had nothing as zero.
	if misc := rep.Categories[1]; misc.CategoryID != nil || !reflect.DeepEqual(misc.Cents, []int64{0, 50_00, 0, 0, 0, 0}) {
		t.Errorf("misc = %+v", misc)
	}
}

func TestBuildMonthlyWithoutHistory(t *testing.T) {
	months := []string{"2026-07", "2026-08", "2026-09"}
	rep := buildMonthly(months, nil, 1, 30)
	if rep.HistoryStart != 2 || rep.Total.AvgCents != 0 || rep.Total.Trend != nil {
		t.Errorf("empty report = %+v", rep)
	}
	if rep.Categories == nil {
		t.Error("categories must encode as [], not null")
	}
}
