package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CardOwner returns the family member whose physical card this is.
//
// Ownership is read from the account snapshots, which each connection writes
// for its own user and which carry only physical cards. That is exactly the
// set that should scope a notification: a swipe on someone's own card is their
// business, whereas Crew's per-merchant virtual cards are household
// subscriptions and belong to everyone.
//
// Returns false when the card isn't a known member card — an unrecognized
// card, or a snapshot not yet written — which callers treat as "tell
// everyone". Staying loud on the unknown case is the safe direction: a missed
// notification about real money is worse than a redundant one.
func (s *Store) CardOwner(ctx context.Context, familyID uuid.UUID, crewCardID string) (uuid.UUID, bool, error) {
	if crewCardID == "" {
		return uuid.Nil, false, nil
	}
	var userID uuid.UUID
	err := s.Pool.QueryRow(ctx, `
		SELECT c.user_id
		FROM account_snapshots s
		JOIN crew_connections c ON c.id = s.connection_id
		JOIN family_members m ON m.user_id = c.user_id
		CROSS JOIN LATERAL jsonb_array_elements(COALESCE(s.payload->'cards', '[]'::jsonb)) AS card
		WHERE m.family_id = $1 AND card->>'id' = $2
		LIMIT 1`, familyID, crewCardID).Scan(&userID)
	if err == pgx.ErrNoRows {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return userID, true, nil
}
