package store

import (
	"context"
	"strconv"
	"strings"
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
	// DebitCardID is Crew's id for the card that paid, empty for bank
	// transactions. It decides who a notification is for.
	DebitCardID  string
	CategoryID   *uuid.UUID // derived from Note
	CategoryName *string    // derived from Note
	NoteIgnored  bool       // note deliberately not treated as a category
	RecurringID  *uuid.UUID
	NotifiedAt   *time.Time
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
	DebitCardID    string
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
			occurred_at, cleared_at, note, debit_card_id, raw
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
		ON CONFLICT (family_id, crew_txn_id) DO NOTHING
		RETURNING id`,
		t.FamilyID, t.ConnectionID, t.CrewTxnID, t.AmountCents, t.Payee, t.MerchantKey, t.Title,
		t.Description, t.Status, t.TxnType, t.MCC, t.ImageURL, t.SubaccountID, t.SubaccountName,
		t.OccurredAt, t.ClearedAt, t.Note, t.DebitCardID, t.Raw).Scan(&id)
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
//
// Keyed on the household rather than the connection — both members watch the
// same Crew accounts, and only one of their connections owns the row.
func (s *Store) UpdateTransactionFromCrew(ctx context.Context, familyID uuid.UUID, crewTxnID string, amountCents int64, status string, clearedAt *time.Time, note string, raw []byte) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE transactions
		SET amount_cents = $3, status = $4, cleared_at = $5, note = $6, raw = $7
		WHERE family_id = $1 AND crew_txn_id = $2`,
		familyID, crewTxnID, amountCents, status, clearedAt, note, raw)
	return err
}

// SetLocalNote updates only the cached note, after a successful write to Crew.
func (s *Store) SetLocalNote(ctx context.Context, familyID uuid.UUID, crewTxnID, note string) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE transactions SET note = $3 WHERE family_id = $1 AND crew_txn_id = $2`,
		familyID, crewTxnID, note)
	return err
}

// The category join derives CategoryID/CategoryName from the note text; the
// ignored_notes join marks notes the family has chosen not to treat as a
// category, so the UI stops offering to promote them.
const txnFrom = `
	FROM transactions t
	LEFT JOIN categories c
	       ON c.family_id = t.family_id AND lower(c.name) = lower(t.note)
	LEFT JOIN ignored_notes i
	       ON i.family_id = t.family_id AND i.note_key = lower(t.note)`

const txnCols = `
	t.id, t.family_id, t.connection_id, t.crew_txn_id, t.amount_cents, t.payee, t.merchant_key,
	t.title, t.description, t.status, t.txn_type, t.mcc, t.image_url, t.subaccount_id,
	t.subaccount_name, t.occurred_at, t.cleared_at, t.note, t.debit_card_id, c.id, c.name,
	(i.family_id IS NOT NULL) AS note_ignored, t.recurring_id, t.notified_at`

func scanTxn(row pgx.Row) (*Transaction, error) {
	var t Transaction
	if err := row.Scan(&t.ID, &t.FamilyID, &t.ConnectionID, &t.CrewTxnID, &t.AmountCents, &t.Payee,
		&t.MerchantKey, &t.Title, &t.Description, &t.Status, &t.TxnType, &t.MCC, &t.ImageURL,
		&t.SubaccountID, &t.SubaccountName, &t.OccurredAt, &t.ClearedAt, &t.Note, &t.DebitCardID,
		&t.CategoryID, &t.CategoryName, &t.NoteIgnored, &t.RecurringID, &t.NotifiedAt); err != nil {
		return nil, err
	}
	return &t, nil
}

// GetTransactionByCrewID looks a transaction up by its Crew identity, for the
// holder-side sync paths that work in Crew's namespace. Scoped to the
// household, since either member's connection may own the row.
func (s *Store) GetTransactionByCrewID(ctx context.Context, familyID uuid.UUID, crewTxnID string) (*Transaction, error) {
	t, err := scanTxn(s.Pool.QueryRow(ctx,
		`SELECT `+txnCols+txnFrom+` WHERE t.family_id = $1 AND t.crew_txn_id = $2`, familyID, crewTxnID))
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	return t, err
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

// PendingForConnection lists a connection's still-pending transactions that
// occurred at or after since and have been stored for at least minAge.
//
// It backs the vanished-transaction sweep, which is why it is scoped to one
// connection rather than the household: only that connection's lease holder
// holds the Crew list that can vouch for these rows. minAge keeps a transaction
// ingested just after that list was fetched from looking like one that has
// disappeared from it.
func (s *Store) PendingForConnection(ctx context.Context, connID uuid.UUID, since time.Time, minAge time.Duration) ([]*Transaction, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+txnCols+txnFrom+`
		WHERE t.connection_id = $1 AND t.cleared_at IS NULL
		  AND t.occurred_at >= $2 AND t.processed_at < now() - $3::interval
		ORDER BY t.occurred_at DESC`, connID, since, minAge)
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

// OldestPendingForConnection reports when a connection's oldest still-pending
// transaction occurred, and whether it has one old enough to judge at all.
//
// The sweep asks first: with nothing to sweep there is no reason to walk Crew's
// list looking for gaps.
func (s *Store) OldestPendingForConnection(ctx context.Context, connID uuid.UUID, minAge time.Duration) (time.Time, bool, error) {
	var oldest *time.Time
	err := s.Pool.QueryRow(ctx, `
		SELECT min(occurred_at) FROM transactions
		WHERE connection_id = $1 AND cleared_at IS NULL AND processed_at < now() - $2::interval`,
		connID, minAge).Scan(&oldest)
	if err != nil {
		return time.Time{}, false, err
	}
	if oldest == nil {
		return time.Time{}, false, nil
	}
	return *oldest, true, nil
}

// DeleteVanishedTransaction removes a transaction Crew no longer reports, along
// with any note write still queued against it.
//
// The two go together: a write against an ID Crew has forgotten can only fail,
// and it would keep retrying until it hit the attempt cap.
func (s *Store) DeleteVanishedTransaction(ctx context.Context, id, connID uuid.UUID, crewTxnID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if _, err := tx.Exec(ctx, `DELETE FROM transactions WHERE id = $1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM crew_write_jobs
		WHERE connection_id = $1 AND kind = $2 AND target_id = $3`,
		connID, WriteNote, crewTxnID); err != nil {
		return err
	}
	return tx.Commit(ctx)
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
	BeforeTime *time.Time // keyset cursor: (occurred_at, id) strictly before
	BeforeID   *uuid.UUID
	Limit      int
	// CategoryIDs selects any of the given categories; combined with
	// Uncategorized it also includes Misc.
	CategoryIDs   []uuid.UUID
	Uncategorized bool
	// Query matches merchant, note, or memo text, case-insensitively.
	Query string
	// Since/Until bound occurred_at as [Since, Until).
	Since *time.Time
	Until *time.Time
	// Direction restricts to money in ("income") or out ("expense").
	Direction string
	// MerchantKey restricts to one merchant, for drilling into a vendor line.
	MerchantKey string
}

func (s *Store) ListTransactions(ctx context.Context, familyID uuid.UUID, f TxnFilter) ([]*Transaction, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 50
	}
	q := `SELECT ` + txnCols + txnFrom + ` WHERE t.family_id = $1`
	args := []any{familyID}
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	if f.BeforeTime != nil && f.BeforeID != nil {
		q += ` AND (t.occurred_at, t.id) < (` + arg(*f.BeforeTime) + `, ` + arg(*f.BeforeID) + `)`
	}
	// Category selection: named categories, Misc, or both.
	switch {
	case len(f.CategoryIDs) > 0 && f.Uncategorized:
		q += ` AND (c.id = ANY(` + arg(f.CategoryIDs) + `) OR c.id IS NULL)`
	case len(f.CategoryIDs) > 0:
		q += ` AND c.id = ANY(` + arg(f.CategoryIDs) + `)`
	case f.Uncategorized:
		// No note, or a note that doesn't name one of the family's categories.
		q += ` AND c.id IS NULL`
	}
	if f.Since != nil {
		q += ` AND t.occurred_at >= ` + arg(*f.Since)
	}
	if f.Until != nil {
		q += ` AND t.occurred_at < ` + arg(*f.Until)
	}
	switch f.Direction {
	case DirectionIncome:
		q += ` AND t.amount_cents > 0`
	case DirectionExpense:
		q += ` AND t.amount_cents < 0`
	}
	if f.MerchantKey != "" {
		q += ` AND t.merchant_key = ` + arg(f.MerchantKey)
	}
	if term := strings.TrimSpace(f.Query); term != "" {
		p := arg("%" + term + "%")
		q += ` AND (t.payee ILIKE ` + p + ` OR t.note ILIKE ` + p +
			` OR t.title ILIKE ` + p + ` OR t.description ILIKE ` + p + `)`
	}
	q += ` ORDER BY t.occurred_at DESC, t.id DESC LIMIT ` + arg(f.Limit)

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

// MerchantExample is one past decision about a merchant: what the family
// called it, and for how much.
type MerchantExample struct {
	Category    string
	AmountCents int64
}

// MerchantCategoryHistory returns recent categorized transactions for a merchant,
// newest first.
//
// Amount is included because for some merchants it *is* the signal — a $100
// Costco charge is fuel, a $12 one is lunch, anything else is groceries. A
// merchant→category mapping cannot express that; these examples let the model
// infer it.
func (s *Store) MerchantCategoryHistory(ctx context.Context, familyID uuid.UUID, merchantKey string, limit int) ([]MerchantExample, error) {
	if merchantKey == "" {
		return nil, nil
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT c.name, t.amount_cents
		FROM transactions t
		JOIN categories c
		  ON c.family_id = t.family_id AND lower(c.name) = lower(t.note)
		WHERE t.family_id = $1 AND t.merchant_key = $2
		ORDER BY t.occurred_at DESC
		LIMIT $3`, familyID, merchantKey, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MerchantExample
	for rows.Next() {
		var e MerchantExample
		if err := rows.Scan(&e.Category, &e.AmountCents); err != nil {
			return nil, err
		}
		out = append(out, e)
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

// TransactionsInSeries lists the charges behind a recurring series. A series
// covers a whole merchant, so this matches on merchant rather than the
// recurring_id link — occurrences ingested before the series existed count too.
func (s *Store) TransactionsInSeries(ctx context.Context, familyID uuid.UUID, merchantKey string, limit int) ([]*Transaction, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+txnCols+txnFrom+`
		WHERE t.family_id = $1 AND t.merchant_key = $2 AND t.amount_cents < 0
		ORDER BY t.occurred_at DESC LIMIT $3`,
		familyID, merchantKey, limit)
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

// Replaceable reports whether a bulk action may rewrite this transaction's
// category.
//
// A note naming one of the family's categories is a category and can be
// replaced — that is the point of recategorizing. A note naming nothing is
// something a person typed into Crew, and no bulk action destroys that.
func (t *Transaction) Replaceable() bool {
	return t.Note == "" || t.CategoryID != nil
}

// ListSince returns every transaction in the window regardless of note, for
// actions that deliberately recategorize rather than fill blanks. Pass a zero
// time for all history.
func (s *Store) ListSince(ctx context.Context, familyID uuid.UUID, since time.Time, limit int) ([]*Transaction, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+txnCols+txnFrom+`
		WHERE t.family_id = $1 AND t.occurred_at >= $2
		ORDER BY t.occurred_at DESC LIMIT $3`, familyID, since, limit)
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

// AllForMerchant returns every transaction for a merchant regardless of note,
// for recategorizing a merchant wholesale.
func (s *Store) AllForMerchant(ctx context.Context, familyID uuid.UUID, merchantKey string, limit int) ([]*Transaction, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+txnCols+txnFrom+`
		WHERE t.family_id = $1 AND t.merchant_key = $2
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

// ListUncategorizedSince returns transactions in the window carrying no note at
// all, newest first.
//
// An empty note is the only safe target for a bulk re-assessment: a note that
// names a category is already the answer — from a rule, a series label, or a
// person — and a note that names nothing is something a human typed. Neither
// should be rewritten in bulk, especially since the note lives in Crew where
// they can see it.
func (s *Store) ListUncategorizedSince(ctx context.Context, familyID uuid.UUID, since time.Time, limit int) ([]*Transaction, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+txnCols+txnFrom+`
		WHERE t.family_id = $1 AND t.note = '' AND t.occurred_at >= $2
		ORDER BY t.occurred_at DESC LIMIT $3`, familyID, since, limit)
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
