package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Insight subjects: what a savings suggestion, a dismissal or a verdict is
// about.
const (
	SubjectMerchant = "merchant"
	SubjectCategory = "category"
)

// MerchantMonthSpend is what one merchant was paid in one calendar month,
// split by the category those charges were filed under.
type MerchantMonthSpend struct {
	MerchantKey string
	// Month is the first of the month as wall-clock time in the zone asked
	// for; only its year and month mean anything.
	Month        time.Time
	CategoryID   *uuid.UUID
	CategoryName string
	Color        string
	SystemKey    *string
	Cents        int64 // positive magnitude
	Count        int
}

// SpendByMerchantMonth totals money out in [start, end) per merchant, calendar
// month (cut in tz) and category. It is the raw material for savings
// suggestions: merchant totals, category totals and the months between all
// come from these rows.
func (s *Store) SpendByMerchantMonth(ctx context.Context, familyID uuid.UUID, start, end time.Time, tz string) ([]MerchantMonthSpend, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT t.merchant_key,
		       date_trunc('month', t.occurred_at AT TIME ZONE $4) AS month,
		       c.id, COALESCE(c.name, ''), COALESCE(c.color, ''), c.system_key,
		       SUM(-t.amount_cents), COUNT(*)
		FROM transactions t
		LEFT JOIN categories c
		       ON c.family_id = t.family_id AND lower(c.name) = lower(t.note)
		WHERE t.family_id = $1 AND t.occurred_at >= $2 AND t.occurred_at < $3
		  AND t.amount_cents < 0
		GROUP BY t.merchant_key, month, c.id, c.name, c.color, c.system_key`,
		familyID, start, end, tz)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]MerchantMonthSpend, 0, 256)
	for rows.Next() {
		var r MerchantMonthSpend
		if err := rows.Scan(&r.MerchantKey, &r.Month, &r.CategoryID, &r.CategoryName, &r.Color,
			&r.SystemKey, &r.Cents, &r.Count); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MerchantProfile is how a merchant presents itself: its most recent display
// name and merchant category code.
type MerchantProfile struct {
	MerchantKey string
	Payee       string
	MCC         string
}

// MerchantProfiles describes every merchant paid in [start, end).
func (s *Store) MerchantProfiles(ctx context.Context, familyID uuid.UUID, start, end time.Time) (map[string]MerchantProfile, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT merchant_key,
		       (array_agg(payee ORDER BY occurred_at DESC))[1],
		       (array_agg(mcc ORDER BY occurred_at DESC))[1]
		FROM transactions
		WHERE family_id = $1 AND occurred_at >= $2 AND occurred_at < $3 AND amount_cents < 0
		GROUP BY merchant_key`, familyID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]MerchantProfile{}
	for rows.Next() {
		var p MerchantProfile
		if err := rows.Scan(&p.MerchantKey, &p.Payee, &p.MCC); err != nil {
			return nil, err
		}
		out[p.MerchantKey] = p
	}
	return out, rows.Err()
}

// InsightDismissal is the family saying a suggestion doesn't apply to them.
type InsightDismissal struct {
	SubjectType string
	SubjectKey  string
	Label       string
	CreatedAt   time.Time
}

func (s *Store) ListInsightDismissals(ctx context.Context, familyID uuid.UUID) ([]InsightDismissal, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT subject_type, subject_key, label, created_at
		FROM insight_dismissals WHERE family_id = $1
		ORDER BY created_at DESC`, familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InsightDismissal
	for rows.Next() {
		var d InsightDismissal
		if err := rows.Scan(&d.SubjectType, &d.SubjectKey, &d.Label, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DismissInsight records "not applicable". Dismissing twice is harmless.
func (s *Store) DismissInsight(ctx context.Context, familyID, userID uuid.UUID, subjectType, subjectKey, label string) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO insight_dismissals (family_id, subject_type, subject_key, label, dismissed_by)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (family_id, subject_type, subject_key) DO UPDATE SET label = EXCLUDED.label`,
		familyID, subjectType, subjectKey, label, userID)
	return err
}

// RestoreInsight undoes a dismissal.
func (s *Store) RestoreInsight(ctx context.Context, familyID uuid.UUID, subjectType, subjectKey string) error {
	tag, err := s.Pool.Exec(ctx, `
		DELETE FROM insight_dismissals
		WHERE family_id = $1 AND subject_type = $2 AND subject_key = $3`,
		familyID, subjectType, subjectKey)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// InsightVerdict is the model's judgement of one flagged spend.
type InsightVerdict struct {
	SubjectType string
	SubjectKey  string
	Fingerprint string
	Verdict     string
	Note        string
	CreatedAt   time.Time
}

// InsightVerdicts returns every cached verdict for the family, keyed by
// subject type and key joined with a colon.
func (s *Store) InsightVerdicts(ctx context.Context, familyID uuid.UUID) (map[string]InsightVerdict, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT subject_type, subject_key, fingerprint, verdict, note, created_at
		FROM insight_verdicts WHERE family_id = $1`, familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]InsightVerdict{}
	for rows.Next() {
		var v InsightVerdict
		if err := rows.Scan(&v.SubjectType, &v.SubjectKey, &v.Fingerprint, &v.Verdict, &v.Note, &v.CreatedAt); err != nil {
			return nil, err
		}
		out[v.SubjectType+":"+v.SubjectKey] = v
	}
	return out, rows.Err()
}

// SaveInsightVerdicts caches fresh verdicts, replacing older ones.
func (s *Store) SaveInsightVerdicts(ctx context.Context, familyID uuid.UUID, vs []InsightVerdict) error {
	for _, v := range vs {
		if _, err := s.Pool.Exec(ctx, `
			INSERT INTO insight_verdicts (family_id, subject_type, subject_key, fingerprint, verdict, note)
			VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (family_id, subject_type, subject_key) DO UPDATE SET
				fingerprint = EXCLUDED.fingerprint, verdict = EXCLUDED.verdict,
				note = EXCLUDED.note, created_at = now()`,
			familyID, v.SubjectType, v.SubjectKey, v.Fingerprint, v.Verdict, v.Note); err != nil {
			return err
		}
	}
	return nil
}

// ClaimInsightNudge reserves the right to nudge about a suggestion now. It
// returns false when a nudge about it went out within cooldown — on any
// replica, since the check and the claim are one statement.
func (s *Store) ClaimInsightNudge(ctx context.Context, familyID uuid.UUID, subjectType, subjectKey string, cooldown time.Duration) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO insight_nudges (family_id, subject_type, subject_key, last_sent_at)
		VALUES ($1,$2,$3, now())
		ON CONFLICT (family_id, subject_type, subject_key) DO UPDATE
		   SET last_sent_at = now()
		 WHERE insight_nudges.last_sent_at < now() - make_interval(secs => $4)
		RETURNING true`,
		familyID, subjectType, subjectKey, cooldown.Seconds()).Scan(&ok)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	return ok, err
}

// FamilyTimezone is the zone the family's browser last reported, or "" if it
// never has.
func (s *Store) FamilyTimezone(ctx context.Context, familyID uuid.UUID) (string, error) {
	var tz *string
	err := s.Pool.QueryRow(ctx, `SELECT timezone FROM families WHERE id = $1`, familyID).Scan(&tz)
	if err != nil || tz == nil {
		return "", err
	}
	return *tz, nil
}

// SetFamilyTimezone records the zone the family's browser reports.
func (s *Store) SetFamilyTimezone(ctx context.Context, familyID uuid.UUID, tz string) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE families SET timezone = $2 WHERE id = $1 AND timezone IS DISTINCT FROM $2`, familyID, tz)
	return err
}

// NudgeRecipient is a family member and whether they want savings nudges.
type NudgeRecipient struct {
	UserID uuid.UUID
	Wants  bool
}

// FamilyNudgeRecipients lists every member of the family.
func (s *Store) FamilyNudgeRecipients(ctx context.Context, familyID uuid.UUID) ([]NudgeRecipient, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT u.id, u.savings_nudges
		FROM family_members m JOIN users u ON u.id = m.user_id
		WHERE m.family_id = $1`, familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NudgeRecipient
	for rows.Next() {
		var r NudgeRecipient
		if err := rows.Scan(&r.UserID, &r.Wants); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// WantsSavingsNudges reports one person's setting.
func (s *Store) WantsSavingsNudges(ctx context.Context, userID uuid.UUID) (bool, error) {
	var on bool
	err := s.Pool.QueryRow(ctx, `SELECT savings_nudges FROM users WHERE id = $1`, userID).Scan(&on)
	return on, err
}

// SetSavingsNudges changes one person's setting.
func (s *Store) SetSavingsNudges(ctx context.Context, userID uuid.UUID, on bool) error {
	_, err := s.Pool.Exec(ctx, `UPDATE users SET savings_nudges = $2 WHERE id = $1`, userID, on)
	return err
}
