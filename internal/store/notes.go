package store

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

// UnmatchedNote is a note appearing on transactions that names no category and
// has not been ignored — a candidate to promote into the category list.
type UnmatchedNote struct {
	Note     string
	Count    int
	LastSeen time.Time
}

// UnmatchedNotes lists candidate notes, most frequent first.
func (s *Store) UnmatchedNotes(ctx context.Context, familyID uuid.UUID, limit int) ([]UnmatchedNote, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT t.note, count(*) AS cnt, max(t.occurred_at) AS last_seen
		FROM transactions t
		LEFT JOIN categories c
		       ON c.family_id = t.family_id AND lower(c.name) = lower(t.note)
		LEFT JOIN ignored_notes i
		       ON i.family_id = t.family_id AND i.note_key = lower(t.note)
		WHERE t.family_id = $1 AND t.note <> '' AND c.id IS NULL AND i.family_id IS NULL
		GROUP BY t.note
		ORDER BY count(*) DESC, max(t.occurred_at) DESC
		LIMIT $2`, familyID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UnmatchedNote
	for rows.Next() {
		var n UnmatchedNote
		if err := rows.Scan(&n.Note, &n.Count, &n.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) IgnoreNote(ctx context.Context, familyID uuid.UUID, note string) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO ignored_notes (family_id, note_key, note) VALUES ($1, lower($2), $2)
		ON CONFLICT (family_id, note_key) DO NOTHING`, familyID, strings.TrimSpace(note))
	return err
}

func (s *Store) UnignoreNote(ctx context.Context, familyID uuid.UUID, note string) error {
	_, err := s.Pool.Exec(ctx, `
		DELETE FROM ignored_notes WHERE family_id = $1 AND note_key = lower($2)`,
		familyID, strings.TrimSpace(note))
	return err
}

func (s *Store) ListIgnoredNotes(ctx context.Context, familyID uuid.UUID) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT note FROM ignored_notes WHERE family_id = $1 ORDER BY lower(note)`, familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// SyncNoteFromCrew updates a cached note when Crew's differs, and reports
// whether anything changed. Used by the periodic note sync, because the SDK
// watcher only fires on status/amount changes and never on a note edit.
func (s *Store) SyncNoteFromCrew(ctx context.Context, connID uuid.UUID, crewTxnID, note string) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE transactions SET note = $3
		WHERE connection_id = $1 AND crew_txn_id = $2 AND note <> $3`,
		connID, crewTxnID, note)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
