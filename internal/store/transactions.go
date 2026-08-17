package store

import (
	"context"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Transaction mirrors a Crew cash transaction. Note holds Crew's user
// annotation, which crewmate uses as the category: CategoryID/CategoryName are
// resolved by joining Note against the family's category list, never stored.
type Transaction struct {
	ID             uuid.UUID
	FamilyID       uuid.UUID
	ConnectionID   uuid.UUID
	CrewTxnID      string
	AmountCents    int64
	Payee          string
	MerchantKey    string
	Title          string
	Description    string
	Status         string
	TxnType        string
	MCC            string
	ImageURL       string
	SubaccountID   *string
	SubaccountName string
	OccurredAt     time.Time
	ClearedAt      *time.Time
	Note           string
	CategoryID     *uuid.UUID // derived from Note
	CategoryName   *string    // derived from Note
	RecurringID    *uuid.UUID
	NotifiedAt     *time.Time
}

// Categorized reports whether this transaction's note names a known category.
func (t *Transaction) Categorized() bool { return t.CategoryID != nil }

type IngestTxn struct {
	FamilyID       uuid.UUID
	ConnectionID   uuid.UUID
	CrewTxnID      string
	AmountCents    int64
	Payee          string
	MerchantKey    string
	Title          string
	Description    string
	Status         string
	TxnType        string
	MCC            string
	ImageURL       string
	SubaccountID   *string
	SubaccountName string
	OccurredAt     time.Time
	ClearedAt      *time.Time
	Note           string
	Raw            []byte
}

// InsertTransaction is the idempotent ingest: returns (id, true) when this call
// created the row, (uuid.Nil, false) when the transaction was already known.
func (s *Store) InsertTransaction(ctx context.Context, t IngestTxn) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO transactions (
			family_id, connection_id, crew_txn_id, amount_cents, payee, merchant_key, title,
			description, status, txn_type, mcc, image_url, subaccount_id, subaccount_name,
			occurred_at, cleared_at, note, raw
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		ON CONFLICT (connection_id, crew_txn_id) DO NOTHING
		RETURNING id`,
		t.FamilyID, t.ConnectionID, t.CrewTxnID, t.AmountCents, t.Payee, t.MerchantKey, t.Title,
		t.Description, t.Status, t.TxnType, t.MCC, t.ImageURL, t.SubaccountID, t.SubaccountName,
		t.OccurredAt, t.ClearedAt, t.Note, t.Raw).Scan(&id)
	if err == pgx.ErrNoRows {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return id, true, nil
}

// UpdateTransactionFromCrew refreshes mutable fields when Crew reports a
// change. The note is included: a category set in the Crew app flows back here.
func (s *Store) UpdateTransactionFromCrew(ctx context.Context, connID uuid.UUID, crewTxnID string, amountCents int64, status string, clearedAt *time.Time, note string, raw []byte) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE transactions
		SET amount_cents = $3, status = $4, cleared_at = $5, note = $6, raw = $7
		WHERE connection_id = $1 AND crew_txn_id = $2`,
		connID, crewTxnID, amountCents, status, clearedAt, note, raw)
	return err
}

// SetLocalNote updates only the cached note, after a successful write to Crew.
func (s *Store) SetLocalNote(ctx context.Context, connID uuid.UUID, crewTxnID, note string) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE transactions SET note = $3 WHERE connection_id = $1 AND crew_txn_id = $2`,
		connID, crewTxnID, note)
	return err
}

// The category join derives CategoryID/CategoryName from the note text.
const txnFrom = `
	FROM transactions t
	LEFT JOIN categories c
	       ON c.family_id = t.family_id AND lower(c.name) = lower(t.note)`

const txnCols = `
	t.id, t.family_id, t.connection_id, t.crew_txn_id, t.amount_cents, t.payee, t.merchant_key,
	t.title, t.description, t.status, t.txn_type, t.mcc, t.image_url, t.subaccount_id,
	t.subaccount_name, t.occurred_at, t.cleared_at, t.note, c.id, c.name,
	t.recurring_id, t.notified_at`

func scanTxn(row pgx.Row) (*Transaction, error) {
	var t Transaction
	if err := row.Scan(&t.ID, &t.FamilyID, &t.ConnectionID, &t.CrewTxnID, &t.AmountCents, &t.Payee,
		&t.MerchantKey, &t.Title, &t.Description, &t.Status, &t.TxnType, &t.MCC, &t.ImageURL,
		&t.SubaccountID, &t.SubaccountName, &t.OccurredAt, &t.ClearedAt, &t.Note,
		&t.CategoryID, &t.CategoryName, &t.RecurringID, &t.NotifiedAt); err != nil {
		return nil, err
	}
	return &t, nil
}

// GetTransactionByID is for internal pipeline use only — HTTP handlers must
// use GetTransaction, which enforces family scoping.
func (s *Store) GetTransactionByID(ctx context.Context, id uuid.UUID) (*Transaction, error) {
	t, err := scanTxn(s.Pool.QueryRow(ctx, `SELECT `+txnCols+txnFrom+` WHERE t.id = $1`, id))
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	return t, err
}

func (s *Store) GetTransaction(ctx context.Context, familyID, id uuid.UUID) (*Transaction, error) {
	t, err := scanTxn(s.Pool.QueryRow(ctx,
		`SELECT `+txnCols+txnFrom+` WHERE t.id = $2 AND t.family_id = $1`, familyID, id))
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	return t, err
}

// SweepUnnotified returns recent transactions that were ingested but never
// notified — a safety net for items dropped between ingest and pipeline.
func (s *Store) SweepUnnotified(ctx context.Context, limit int) ([]uuid.UUID, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id FROM transactions
		WHERE notified_at IS NULL AND processed_at > now() - interval '7 days'
		ORDER BY processed_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

type TxnFilter struct {
	BeforeTime    *time.Time // keyset cursor: (occurred_at, id) strictly before
	BeforeID      *uuid.UUID
	Limit         int
	CategoryID    *uuid.UUID
	Uncategorized bool
}

func (s *Store) ListTransactions(ctx context.Context, familyID uuid.UUID, f TxnFilter) ([]*Transaction, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 50
	}
	q := `SELECT ` + txnCols + txnFrom + ` WHERE t.family_id = $1`
	args := []any{familyID}
	if f.BeforeTime != nil && f.BeforeID != nil {
		args = append(args, *f.BeforeTime, *f.BeforeID)
		q += ` AND (t.occurred_at, t.id) < ($2, $3)`
	}
	if f.CategoryID != nil {
		args = append(args, *f.CategoryID)
		q += ` AND c.id = $` + strconv.Itoa(len(args))
	}
	if f.Uncategorized {
		// No note, or a note that doesn't name one of the family's categories.
		q += ` AND c.id IS NULL`
	}
	args = append(args, f.Limit)
	q += ` ORDER BY t.occurred_at DESC, t.id DESC LIMIT $` + strconv.Itoa(len(args))

	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Transaction
	for rows.Next() {
		t, err := scanTxn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SuggestCategoryForMerchant returns the note most recently applied to this
// merchant that names a known category. This replaces a merchant-rule table:
// history *is* the cache, and it also learns from categories set in the Crew
// app directly.
func (s *Store) SuggestCategoryForMerchant(ctx context.Context, familyID uuid.UUID, merchantKey string) (string, bool, error) {
	if merchantKey == "" {
		return "", false, nil
	}
	var name string
	err := s.Pool.QueryRow(ctx, `
		SELECT c.name
		FROM transactions t
		JOIN categories c
		  ON c.family_id = t.family_id AND lower(c.name) = lower(t.note)
		WHERE t.family_id = $1 AND t.merchant_key = $2
		ORDER BY t.occurred_at DESC
		LIMIT 1`, familyID, merchantKey).Scan(&name)
	if err == pgx.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return name, true, nil
}

// UncategorizedForMerchant lists transactions of a merchant that carry no note
// at all — the safe targets for an "apply to this merchant" backfill. A
// transaction whose note is a real user annotation is never touched.
func (s *Store) UncategorizedForMerchant(ctx context.Context, familyID uuid.UUID, merchantKey string, limit int) ([]*Transaction, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+txnCols+txnFrom+`
		WHERE t.family_id = $1 AND t.merchant_key = $2 AND t.note = ''
		ORDER BY t.occurred_at DESC LIMIT $3`, familyID, merchantKey, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Transaction
	for rows.Next() {
		t, err := scanTxn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TransactionsWithNote finds every transaction whose note matches a given
// string — used to rewrite notes when a category is renamed.
func (s *Store) TransactionsWithNote(ctx context.Context, familyID uuid.UUID, note string, limit int) ([]*Transaction, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+txnCols+txnFrom+`
		WHERE t.family_id = $1 AND lower(t.note) = lower($2)
		ORDER BY t.occurred_at DESC LIMIT $3`, familyID, note, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Transaction
	for rows.Next() {
		t, err := scanTxn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ClaimNotification atomically claims the right to send the push for a txn.
// Returns true exactly once per transaction across all replicas.
func (s *Store) ClaimNotification(ctx context.Context, txnID uuid.UUID) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE transactions SET notified_at = now()
		WHERE id = $1 AND notified_at IS NULL`, txnID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) SetTransactionRecurring(ctx context.Context, txnID, recurringID uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `UPDATE transactions SET recurring_id = $2 WHERE id = $1`, txnID, recurringID)
	return err
}

// MerchantOccurrences returns occurred_at timestamps for a merchant (for cadence detection).
func (s *Store) MerchantOccurrences(ctx context.Context, familyID uuid.UUID, merchantKey string, amountCents int64, tolerance int64) ([]time.Time, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT occurred_at FROM transactions
		WHERE family_id = $1 AND merchant_key = $2
		  AND abs(amount_cents - $3) <= $4
		ORDER BY occurred_at`,
		familyID, merchantKey, amountCents, tolerance)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []time.Time
	for rows.Next() {
		var ts time.Time
		if err := rows.Scan(&ts); err != nil {
			return nil, err
		}
		out = append(out, ts)
	}
	return out, rows.Err()
}
