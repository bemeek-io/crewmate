package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Session struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

func (s *Store) CreateSession(ctx context.Context, tokenHash []byte, userID uuid.UUID, ttl time.Duration, userAgent string) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at, user_agent)
		VALUES ($1, $2, now() + $3, $4)`,
		tokenHash, userID, ttl, userAgent)
	return err
}

// GetSessionByTokenHash returns the live session for a token hash, or nil when
// absent/expired. It slides last_seen_at/expires_at at most once per hour.
func (s *Store) GetSessionByTokenHash(ctx context.Context, tokenHash []byte, ttl time.Duration) (*Session, error) {
	var sess Session
	err := s.Pool.QueryRow(ctx, `
		SELECT id, user_id, last_seen_at, expires_at
		FROM sessions
		WHERE token_hash = $1 AND expires_at > now()`, tokenHash,
	).Scan(&sess.ID, &sess.UserID, &sess.LastSeenAt, &sess.ExpiresAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if time.Since(sess.LastSeenAt) > time.Hour {
		_, _ = s.Pool.Exec(ctx, `
			UPDATE sessions SET last_seen_at = now(), expires_at = now() + $2
			WHERE id = $1`, sess.ID, ttl)
	}
	return &sess, nil
}

func (s *Store) DeleteSession(ctx context.Context, id uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
	return err
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
	return err
}
