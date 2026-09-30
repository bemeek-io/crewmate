package insights

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/bemeek-io/crewmate/internal/store"
)

// Report is a family's suggestions and everything they were made from.
type Report struct {
	Months []string
	// Start is the first full month; Current the month in progress; End the
	// month after it.
	Start, Current, End time.Time
	Now                 time.Time
	Suggestions         []Suggestion
	Dismissed           []store.InsightDismissal
	Cached              map[string]store.InsightVerdict
}

// Load works out a family's suggestions as of now, with months cut in now's
// zone. The page and the push pipeline both come through here, so a nudge
// always quotes the figures the page will show.
func Load(ctx context.Context, st *store.Store, familyID uuid.UUID, now time.Time) (*Report, error) {
	loc := now.Location()
	r := &Report{Now: now}
	r.Current = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	r.Start = r.Current.AddDate(0, -WindowMonths, 0)
	r.End = r.Current.AddDate(0, 1, 0)
	for m := r.Start; m.Before(r.End); m = m.AddDate(0, 1, 0) {
		r.Months = append(r.Months, m.Format("2006-01"))
	}

	rows, err := st.SpendByMerchantMonth(ctx, familyID, r.Start, r.End, loc.String())
	if err != nil {
		return nil, fmt.Errorf("spend: %w", err)
	}
	profiles, err := st.MerchantProfiles(ctx, familyID, r.Start, r.End)
	if err != nil {
		return nil, fmt.Errorf("profiles: %w", err)
	}
	series, err := st.ListRecurringSeries(ctx, familyID)
	if err != nil {
		return nil, fmt.Errorf("series: %w", err)
	}
	if r.Dismissed, err = st.ListInsightDismissals(ctx, familyID); err != nil {
		return nil, fmt.Errorf("dismissals: %w", err)
	}
	if r.Cached, err = st.InsightVerdicts(ctx, familyID); err != nil {
		return nil, fmt.Errorf("verdicts: %w", err)
	}
	r.Suggestions = Detect(Input{
		Now: now, Months: r.Months, Rows: rows, Profiles: profiles,
		Series: series, Dismissed: r.Dismissed,
	})
	return r, nil
}

// Necessities are what the family has marked not applicable, by name.
func (r *Report) Necessities() []string {
	out := make([]string, 0, len(r.Dismissed))
	for _, d := range r.Dismissed {
		out = append(out, d.Label)
	}
	return out
}
