package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PendingLogin is an in-flight OTP login. Persisted so any replica can serve
// any step of the multi-request flow. The intermediate Crew bearer token is
// AES-GCM encrypted (AAD = pending login ID).
type PendingLogin struct {
	ID              uuid.UUID
	Stage           string // sms | email
	PhoneID         string
	EmailID         string
	TokenCiphertext []byte
	Attempts        int
	ExpiresAt       time.Time
}

func (s *Store) CreatePendingLogin(ctx context.Context, id uuid.UUID, phoneID string, ttl time.Duration) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO pending_logins (id, stage, phone_id, expires_at)
		VALUES ($1, 'sms', $2, now() + $3)`, id, phoneID, ttl)
	return err
}

func (s *Store) GetPendingLogin(ctx context.Context, id uuid.UUID) (*PendingLogin, error) {
	var p PendingLogin
	err := s.Pool.QueryRow(ctx, `
		SELECT id, stage, phone_id, email_id, token_ciphertext, attempts, expires_at
		FROM pending_logins WHERE id = $1 AND expires_at > now()`, id,
	).Scan(&p.ID, &p.Stage, &p.PhoneID, &p.EmailID, &p.TokenCiphertext, &p.Attempts, &p.ExpiresAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// BumpPendingAttempts increments the attempt counter and reports the new value.
func (s *Store) BumpPendingAttempts(ctx context.Context, id uuid.UUID) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `
		UPDATE pending_logins SET attempts = attempts + 1 WHERE id = $1 RETURNING attempts`, id).Scan(&n)
	return n, err
}

func (s *Store) AdvancePendingToEmail(ctx context.Context, id uuid.UUID, emailID string, tokenCiphertext []byte) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE pending_logins
		SET stage = 'email', email_id = $2, token_ciphertext = $3, attempts = 0
		WHERE id = $1`, id, emailID, tokenCiphertext)
	return err
}

func (s *Store) UpdatePendingToken(ctx context.Context, id uuid.UUID, tokenCiphertext []byte) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE pending_logins SET token_ciphertext = $2 WHERE id = $1`, id, tokenCiphertext)
	return err
}

func (s *Store) DeletePendingLogin(ctx context.Context, id uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM pending_logins WHERE id = $1`, id)
	return err
}

func (s *Store) DeleteExpiredPendingLogins(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM pending_logins WHERE expires_at < now()`)
	return err
}
