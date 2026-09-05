package watcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	crew "github.com/bemeek-io/go-crew"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bemeek-io/crewmate/internal/store"
)

// The vanish sweep is mostly SQL and mostly about what it declines to delete,
// neither of which the pure unit tests reach. These run it for real: a live
// Postgres on one side, a fake Crew on the other.
//
// Skipped unless CREWMATE_TEST_DATABASE_URL is set, so `go test ./...` stays
// green on a machine (and in the deploy gate) with no database.
func testStore(t *testing.T) *store.Store {
	t.Helper()
	url := os.Getenv("CREWMATE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set CREWMATE_TEST_DATABASE_URL to run sweep integration tests")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, url)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(st.Close)
	return st
}

// seed builds a family with one connection and returns both IDs. Each test gets
// its own, so they can run against the same database without colliding.
func seed(t *testing.T, st *store.Store) (familyID, connID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()
	var userID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO users (crew_user_id) VALUES ($1) RETURNING id`, "crew-"+suffix).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO families (name) VALUES ($1) RETURNING id`, "fam-"+suffix).Scan(&familyID); err != nil {
		t.Fatalf("insert family: %v", err)
	}
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO family_members (family_id, user_id, role) VALUES ($1,$2,'admin')`,
		familyID, userID); err != nil {
		t.Fatalf("insert member: %v", err)
	}
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO crew_connections (user_id, token_ciphertext) VALUES ($1,$2) RETURNING id`,
		userID, []byte("x")).Scan(&connID); err != nil {
		t.Fatalf("insert connection: %v", err)
	}
	return familyID, connID
}

type seedTxn struct {
	crewID    string
	occurred  time.Time
	cleared   *time.Time
	storedAgo time.Duration // how long ago it was ingested
}

func insertTxn(t *testing.T, st *store.Store, familyID, connID uuid.UUID, s seedTxn) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := st.Pool.QueryRow(context.Background(), `
		INSERT INTO transactions
			(family_id, connection_id, crew_txn_id, amount_cents, payee, merchant_key,
			 occurred_at, cleared_at, processed_at)
		VALUES ($1,$2,$3,-6,'HOME DEPOT','home depot',$4,$5, now() - $6::interval)
		RETURNING id`,
		familyID, connID, s.crewID, s.occurred, s.cleared, s.storedAgo).Scan(&id)
	if err != nil {
		t.Fatalf("insert txn %s: %v", s.crewID, err)
	}
	return id
}

func exists(t *testing.T, st *store.Store, id uuid.UUID) bool {
	t.Helper()
	var n int
	if err := st.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM transactions WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatalf("count txn: %v", err)
	}
	return n > 0
}

// fakeCrew serves the SDK's cashTransactions query from a fixed list, newest
// first, honouring `first`/`after` so paging behaves as it would against the
// real API.
//
// An optional counter records how many Crew calls a run made, so a test can
// hold a code path to a call budget rather than trusting it to stay cheap.
func fakeCrew(t *testing.T, txns []crew.CashTransaction, calls ...*atomic.Int64) *crew.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(calls) > 0 {
			calls[0].Add(1)
		}
		var req struct {
			Variables struct {
				First int    `json:"first"`
				After string `json:"after"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode graphql request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		start := 0
		if req.Variables.After != "" {
			for i, tx := range txns {
				if tx.ID == req.Variables.After {
					start = i + 1
					break
				}
			}
		}
		end := len(txns)
		if n := req.Variables.First; n > 0 && start+n < end {
			end = start + n
		}
		page := txns[start:end]

		type edge struct {
			Node crew.CashTransaction `json:"node"`
		}
		edges := make([]edge, len(page))
		for i, tx := range page {
			edges[i] = edge{Node: tx}
		}
		endCursor := ""
		if len(page) > 0 {
			endCursor = page[len(page)-1].ID
		}
		resp := map[string]any{"data": map[string]any{
			"currentUser": map[string]any{
				"cashTransactions": map[string]any{
					"edges": edges,
					"pageInfo": map[string]any{
						"endCursor":   endCursor,
						"hasNextPage": end < len(txns),
					},
				},
			},
		}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return crew.NewClient(crew.WithAPIURL(srv.URL), crew.WithToken("test-token"))
}

func runner(st *store.Store, familyID, connID uuid.UUID) *Runner {
	return &Runner{
		Store: st,
		Log:   zap.NewNop(),
		Conn:  &store.CrewConnection{ID: connID, FamilyID: familyID},
	}
}

// sweep fetches the first page the way syncRecent does, then sweeps.
func sweep(t *testing.T, r *Runner, client *crew.Client) {
	t.Helper()
	ctx := context.Background()
	page, err := client.CashTransactions(ctx, crew.CashTransactionsOptions{First: recentPageSize})
	if err != nil {
		t.Fatalf("fetch first page: %v", err)
	}
	r.sweepVanished(ctx, page, zap.NewNop())
}

func crewTxn(id string, occurred time.Time) crew.CashTransaction {
	return crew.CashTransaction{ID: id, OccurredAt: occurred, AmountCents: -6, Title: "Home Depot"}
}

// unrelatedPage is a page that lists something, but not the transaction under
// test. An empty page would make the sweep bail before deciding anything, so a
// test using one proves nothing.
func unrelatedPage(now time.Time) []crew.CashTransaction {
	return []crew.CashTransaction{crewTxn("unrelated", now.Add(-time.Hour))}
}

// The case that started this: a cancelled pending charge Crew no longer lists.
func TestSweepDeletesCancelledPending(t *testing.T) {
	st := testStore(t)
	familyID, connID := seed(t, st)
	now := time.Now()

	gone := insertTxn(t, st, familyID, connID, seedTxn{
		crewID: "cancelled", occurred: now.Add(-48 * time.Hour), storedAgo: 47 * time.Hour,
	})
	kept := insertTxn(t, st, familyID, connID, seedTxn{
		crewID: "live", occurred: now.Add(-24 * time.Hour), storedAgo: 23 * time.Hour,
	})

	client := fakeCrew(t, []crew.CashTransaction{crewTxn("live", now.Add(-24*time.Hour))})
	sweep(t, runner(st, familyID, connID), client)

	if exists(t, st, gone) {
		t.Error("cancelled pending transaction survived the sweep")
	}
	if !exists(t, st, kept) {
		t.Error("sweep deleted a transaction Crew still reports")
	}
}

// A queued category write against a vanished transaction must go with it,
// otherwise it retries against an ID Crew has forgotten until it is abandoned.
func TestSweepDropsQueuedNoteWrite(t *testing.T) {
	st := testStore(t)
	familyID, connID := seed(t, st)
	now := time.Now()

	insertTxn(t, st, familyID, connID, seedTxn{
		crewID: "cancelled", occurred: now.Add(-48 * time.Hour), storedAgo: 47 * time.Hour,
	})
	if err := st.EnqueueNoteWrite(context.Background(), connID, "cancelled", "Home"); err != nil {
		t.Fatalf("enqueue note write: %v", err)
	}

	client := fakeCrew(t, unrelatedPage(now))
	sweep(t, runner(st, familyID, connID), client)

	jobs, err := st.TakeWriteJobs(context.Background(), connID, 10)
	if err != nil {
		t.Fatalf("take write jobs: %v", err)
	}
	if len(jobs) != 0 {
		t.Errorf("write job for a vanished transaction survived: %+v", jobs)
	}
}

// Everything the sweep must refuse to delete.
func TestSweepLeavesAlone(t *testing.T) {
	now := time.Now()
	cleared := now.Add(-40 * time.Hour)
	cases := []struct {
		name string
		txn  seedTxn
	}{
		{
			// Within the grace period: it may simply be newer than the page.
			"recently ingested",
			seedTxn{crewID: "fresh", occurred: now.Add(-10 * time.Minute), storedAgo: time.Minute},
		},
		{
			// A settled charge missing from a page means the page is wrong.
			"already cleared",
			seedTxn{crewID: "settled", occurred: now.Add(-48 * time.Hour), cleared: &cleared, storedAgo: 47 * time.Hour},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := testStore(t)
			familyID, connID := seed(t, st)
			id := insertTxn(t, st, familyID, connID, c.txn)

			client := fakeCrew(t, unrelatedPage(now))
			sweep(t, runner(st, familyID, connID), client)

			if !exists(t, st, id) {
				t.Errorf("sweep deleted a %s transaction", c.name)
			}
		})
	}
}

// The sweep is confined to the page syncRecent already fetched, and spends no
// Crew calls of its own. It once chased a buried charge across twenty more
// pages on every lease acquisition; leaving that row alone is the deliberate
// price of not putting an unbounded walk on a timer. A cancelled charge is
// caught on the first page in the case that actually occurs, since a charge is
// cancelled within a day or so of being authorized.
func TestSweepStaysOnTheFirstPage(t *testing.T) {
	st := testStore(t)
	familyID, connID := seed(t, st)
	now := time.Now()

	buried := insertTxn(t, st, familyID, connID, seedTxn{
		crewID: "cancelled", occurred: now.Add(-400 * time.Hour), storedAgo: 399 * time.Hour,
	})

	// 250 live transactions, all newer — more than two pages' worth on top of it.
	var live []crew.CashTransaction
	for i := 0; i < 250; i++ {
		live = append(live, crewTxn("live-"+uuid.NewString(), now.Add(-time.Duration(i)*time.Hour)))
	}

	var calls atomic.Int64
	sweep(t, runner(st, familyID, connID), fakeCrew(t, live, &calls))

	if !exists(t, st, buried) {
		t.Error("the sweep deleted a transaction it never actually looked at")
	}
	// One call, and it is the page sweep() fetched — the sweep itself adds none.
	if got := calls.Load(); got != 1 {
		t.Errorf("crew calls = %d, want 1: the sweep must not page on its own", got)
	}
}
