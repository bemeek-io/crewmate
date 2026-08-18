package store

import (
	"context"

	"github.com/google/uuid"
)

// EffectiveKind is what the family actually considers this series to be: their
// own judgement where they've given one, otherwise the classifier's.
func (r RecurringSeries) EffectiveKind() string {
	if r.MarkedKind != "" {
		return r.MarkedKind
	}
	return r.Kind
}

// SetSeriesKind records a member overruling the classifier. An empty kind
// clears the override, returning the series to whatever detection says.
//
// Deliberately separate from labelling: "this is a subscription" and "file it
// under Subscription" are different decisions. Tying them together meant
// anyone who filed subscriptions under a category of their own — Tech, say —
// had to drop the label, which silently removed the charge from the
// subscription total.
//
// It also gives people a way past the classifier being stricter than they are:
// it wants a near-identical amount, so usage-billed services come out merely
// 'recurring' despite plainly being subscriptions.
func (s *Store) SetSeriesKind(ctx context.Context, familyID, id uuid.UUID, kind string) error {
	var marked *string
	if kind != "" {
		marked = &kind
	}
	tag, err := s.Pool.Exec(ctx, `
		UPDATE recurring_series SET marked_kind = $3
		WHERE id = $2 AND family_id = $1`, familyID, id, marked)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
