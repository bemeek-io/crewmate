package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type PushSubscription struct {
	ID       uuid.UUID
	UserID   uuid.UUID
	Endpoint string
	P256dh   string
	Auth     string
}

// UpsertPushSubscription is called on opt-in AND on every app launch — iOS
// silently drops subscriptions, so the client re-registers each time.
func (s *Store) UpsertPushSubscription(ctx context.Context, userID uuid.UUID, endpoint, p256dh, auth, userAgent string) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth, user_agent)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (endpoint)
		DO UPDATE SET user_id = EXCLUDED.user_id, p256dh = EXCLUDED.p256dh,
		              auth = EXCLUDED.auth, last_seen_at = now()`,
		userID, endpoint, p256dh, auth, userAgent)
	return err
}

// DeletePushSubscriptionByEndpoint is the unscoped prune used when a push
// gateway answers 404/410 — at that point the endpoint is dead for everyone and
// there is no caller to scope it to. HTTP handlers must use
// DeletePushSubscriptionForUser instead.
func (s *Store) DeletePushSubscriptionByEndpoint(ctx context.Context, endpoint string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM push_subscriptions WHERE endpoint = $1`, endpoint)
	return err
}

// DeletePushSubscriptionForUser unsubscribes one of the caller's own devices.
// Scoping by user_id means knowing someone else's endpoint URL is not enough to
// silence their notifications.
func (s *Store) DeletePushSubscriptionForUser(ctx context.Context, userID uuid.UUID, endpoint string) error {
	_, err := s.Pool.Exec(ctx,
		`DELETE FROM push_subscriptions WHERE endpoint = $2 AND user_id = $1`, userID, endpoint)
	return err
}

func (s *Store) ListPushSubscriptionsForUser(ctx context.Context, userID uuid.UUID) ([]PushSubscription, error) {
	return s.listSubs(ctx, `
		SELECT id, user_id, endpoint, p256dh, auth FROM push_subscriptions WHERE user_id = $1`, userID)
}

// ListPushSubscriptionsForFamily returns every member's subscriptions.
func (s *Store) ListPushSubscriptionsForFamily(ctx context.Context, familyID uuid.UUID) ([]PushSubscription, error) {
	return s.listSubs(ctx, `
		SELECT p.id, p.user_id, p.endpoint, p.p256dh, p.auth
		FROM push_subscriptions p
		JOIN family_members m ON m.user_id = p.user_id
		WHERE m.family_id = $1`, familyID)
}

func (s *Store) listSubs(ctx context.Context, q string, arg any) ([]PushSubscription, error) {
	rows, err := s.Pool.Query(ctx, q, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PushSubscription
	for rows.Next() {
		var p PushSubscription
		if err := rows.Scan(&p.ID, &p.UserID, &p.Endpoint, &p.P256dh, &p.Auth); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// --- Rate limiting (fixed window, replica-safe) ------------------------------

// RateAllow increments the counter for key in the current window and reports
// whether the request is within limit.
func (s *Store) RateAllow(ctx context.Context, key string, window time.Duration, limit int) (bool, error) {
	windowStart := time.Now().UTC().Truncate(window)
	var count int
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO rate_limits (key, window_start, count) VALUES ($1, $2, 1)
		ON CONFLICT (key, window_start) DO UPDATE SET count = rate_limits.count + 1
		RETURNING count`, key, windowStart).Scan(&count)
	if err != nil {
		return false, err
	}
	return count <= limit, nil
}

func (s *Store) DeleteExpiredRateWindows(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM rate_limits WHERE window_start < now() - interval '2 days'`)
	return err
}
