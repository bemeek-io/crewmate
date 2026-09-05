package watcher

import (
	"context"
	"testing"
	"time"

	crew "github.com/bemeek-io/go-crew"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bemeek-io/crewmate/internal/categorize"
	"github.com/bemeek-io/crewmate/internal/store"
)

// Reconcile is the only path that inserts a transaction crewmate does not
// already have. The SDK's baseline poll absorbs the visible page silently and
// the note sync only updates rows that exist, so whatever reconcile declines to
// ingest stays missing for good. These run it against a live Postgres and a
// fake Crew, and are skipped without CREWMATE_TEST_DATABASE_URL like the rest.

func reconcileRunner(st *store.Store, familyID, connID uuid.UUID, lastPolled *time.Time, backfillMonths int) *Runner {
	return &Runner{
		Store:          st,
		Log:            zap.NewNop(),
		Pipeline:       categorize.NewPipeline(st, nil, nil, zap.NewNop()),
		Conn:           &store.CrewConnection{ID: connID, FamilyID: familyID, LastPolledAt: lastPolled},
		BackfillMonths: backfillMonths,
	}
}

func storedCrewIDs(t *testing.T, st *store.Store, familyID uuid.UUID) map[string]bool {
	t.Helper()
	rows, err := st.Pool.Query(context.Background(),
		`SELECT crew_txn_id FROM transactions WHERE family_id = $1`, familyID)
	if err != nil {
		t.Fatalf("list stored: %v", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan stored: %v", err)
		}
		out[id] = true
	}
	return out
}

// The case behind the bug report: a transaction went missing from a stretch
// that the connection had already polled past, so every later reconcile skipped
// straight over it. last_polled_at says when we last spoke to Crew, not what we
// managed to store, and using it to bound ingest makes any older hole permanent
// — reconnecting cannot fix what reconcile refuses to look at.
func TestReconcileRestoresTransactionLostBeforeTheLastPoll(t *testing.T) {
	st := testStore(t)
	familyID, connID := seed(t, st)
	now := time.Now()

	// Crew still lists all six; crewmate is missing the one from four days ago,
	// which is well before the 24h overlap behind a poll an hour ago.
	crewList := []struct {
		id       string
		occurred time.Time
	}{
		{"c0", now.Add(-2 * time.Hour)},
		{"c1", now.Add(-26 * time.Hour)},
		{"lost", now.Add(-4 * 24 * time.Hour)},
		{"c3", now.Add(-5 * 24 * time.Hour)},
		{"c4", now.Add(-6 * 24 * time.Hour)},
		{"c5", now.Add(-7 * 24 * time.Hour)},
	}
	var txns []crew.CashTransaction
	for _, c := range crewList {
		txns = append(txns, crewTxn(c.id, c.occurred))
		if c.id == "lost" {
			continue // the hole
		}
		cleared := c.occurred
		insertTxn(t, st, familyID, connID, seedTxn{
			crewID: c.id, occurred: c.occurred, cleared: &cleared, storedAgo: time.Hour,
		})
	}

	polled := now.Add(-time.Hour)
	r := reconcileRunner(st, familyID, connID, &polled, 12)
	if err := r.reconcile(context.Background(), fakeCrew(t, txns), zap.NewNop()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if got := storedCrewIDs(t, st, familyID); !got["lost"] {
		t.Error("reconcile left the missing transaction missing; nothing else can insert it")
	}
}

// The backfill bound is deliberate and must survive the fix above: a first run
// takes a fixed slice of history rather than everything Crew will hand over.
func TestReconcileFirstRunStopsAtTheBackfillHorizon(t *testing.T) {
	st := testStore(t)
	familyID, connID := seed(t, st)
	now := time.Now()

	txns := []crew.CashTransaction{
		crewTxn("recent", now.Add(-2*time.Hour)),
		crewTxn("ancient", now.AddDate(0, -3, 0)),
	}

	r := reconcileRunner(st, familyID, connID, nil, 1) // first run, 1 month back
	if err := r.reconcile(context.Background(), fakeCrew(t, txns), zap.NewNop()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	stored := storedCrewIDs(t, st, familyID)
	if !stored["recent"] {
		t.Error("first run skipped a transaction inside the backfill window")
	}
	if stored["ancient"] {
		t.Error("first run back-filled past BackfillMonths")
	}
}
