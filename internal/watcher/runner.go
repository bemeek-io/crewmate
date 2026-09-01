// Package watcher runs the per-connection Crew client on the lease-holding
// replica: transaction watching, boot reconciliation, balance snapshots, and
// fenced token-rotation persistence.
package watcher

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	crew "github.com/bemeek-io/go-crew"
	"go.uber.org/zap"

	"github.com/bemeek-io/crewmate/internal/categorize"
	"github.com/bemeek-io/crewmate/internal/crewcards"
	"github.com/bemeek-io/crewmate/internal/crypto"
	"github.com/bemeek-io/crewmate/internal/push"
	"github.com/bemeek-io/crewmate/internal/store"
	"github.com/google/uuid"
)

const (
	maxBackoff = 30 * time.Minute
	// reconcileOverlap guards against out-of-order arrival around the last
	// successful poll.
	reconcileOverlap = 24 * time.Hour
)

// errLeaseLost signals that another replica owns the connection now; the
// runner must stop silently without touching connection state.
var errLeaseLost = errors.New("lease lost")

// errDisabled signals a user-initiated disconnect.
var errDisabled = errors.New("connection disabled")

type Runner struct {
	Store          *store.Store
	Box            *crypto.Box
	Log            *zap.Logger
	Pipeline       *categorize.Pipeline
	Push           push.Sender
	Conn           *store.CrewConnection
	Lease          store.Lease
	WatchInterval  time.Duration
	BackfillMonths int

	refreshCh chan struct{}
	writeCh   chan struct{}
	persistMu sync.Mutex

	errMu   sync.Mutex
	lastErr error
}

// RequestRefresh asks for an early account-snapshot refresh (best effort).
func (r *Runner) RequestRefresh() {
	select {
	case r.refreshCh <- struct{}{}:
	default:
	}
}

// RequestWriteDrain asks the runner to flush queued Crew note writes now
// rather than at the next poll tick (best effort).
func (r *Runner) RequestWriteDrain() {
	select {
	case r.writeCh <- struct{}{}:
	default:
	}
}

// Run blocks until ctx is canceled, the lease is lost, or the connection hits
// a terminal state (401 -> needs_relogin, or user disconnect).
func (r *Runner) Run(ctx context.Context) {
	if r.refreshCh == nil {
		r.refreshCh = make(chan struct{}, 1)
	}
	if r.writeCh == nil {
		r.writeCh = make(chan struct{}, 1)
	}
	log := r.Log.With(zap.String("conn", r.Conn.ID.String()), zap.Int64("epoch", r.Lease.Epoch))

	tokenBytes, err := r.Box.Decrypt(r.Conn.TokenCiphertext, r.Conn.ID[:])
	if err != nil {
		log.Error("token decrypt failed — flagging needs_relogin", zap.Error(err))
		r.markNeedsRelogin(ctx, log)
		return
	}
	token := string(tokenBytes)

	backoff := time.Minute
	retriedAuth := false
	for {
		err := r.session(ctx, token, log)
		if ctx.Err() != nil || errors.Is(err, errLeaseLost) || errors.Is(err, errDisabled) {
			return
		}
		if errors.Is(err, crew.ErrUnauthorized) {
			// Failover race guard: the stored token may be newer than the one
			// we started with (e.g. user re-login) — re-read once and retry.
			if !retriedAuth {
				retriedAuth = true
				if fresh, ok := r.rereadToken(ctx, log); ok && fresh != token {
					token = fresh
					continue
				}
			}
			log.Warn("crew session unauthorized — flagging needs_relogin")
			r.markNeedsRelogin(ctx, log)
			return
		}
		log.Warn("watcher session ended, retrying", zap.Error(err), zap.Duration("backoff", backoff))
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > maxBackoff {
			backoff = maxBackoff
		}
		if fresh, ok := r.rereadToken(ctx, log); ok {
			token = fresh
		} else {
			return // lease lost
		}
	}
}

func (r *Runner) rereadToken(ctx context.Context, log *zap.Logger) (string, bool) {
	ct, held, err := r.Store.ReadTokenFenced(ctx, r.Lease)
	if err != nil {
		log.Warn("re-read token", zap.Error(err))
		return "", false
	}
	if !held {
		return "", false
	}
	pt, err := r.Box.Decrypt(ct, r.Conn.ID[:])
	if err != nil {
		log.Error("re-read token decrypt failed", zap.Error(err))
		return "", false
	}
	return string(pt), true
}

func (r *Runner) markNeedsRelogin(ctx context.Context, log *zap.Logger) {
	ok, err := r.Store.MarkNeedsRelogin(context.WithoutCancel(ctx), r.Lease)
	if err != nil {
		log.Error("mark needs_relogin", zap.Error(err))
		return
	}
	if !ok {
		return // fencing failed — another replica owns it, stay silent
	}
	r.Push.SendToUser(context.WithoutCancel(ctx), r.Conn.UserID, push.Notification{
		Title: "Reconnect your Crew account",
		Body:  "Crewmate lost access to your Crew account. Sign in again to keep tracking transactions.",
		URL:   "/settings",
	})
}

// session builds a client, reconciles, watches, and blocks until something
// ends it. Its error return classifies why.
func (r *Runner) session(ctx context.Context, token string, log *zap.Logger) error {
	client := crew.NewClient(
		crew.WithToken(token),
		crew.WithTokenCallback(r.persistToken(log)),
		crew.WithLogger(zapCrewAdapter{log.Sugar()}),
		crew.WithWatchInterval(r.WatchInterval),
	)

	r.setLastErr(nil)
	client.OnTransaction(func(tx crew.CashTransaction) {
		r.ingest(ctx, tx, true, log)
	})
	client.OnTransactionUpdate(func(tx crew.CashTransaction) {
		raw, _ := json.Marshal(tx)
		// Note is included: a category set in the Crew app flows back to us.
		if err := r.Store.UpdateTransactionFromCrew(ctx, r.Conn.FamilyID, tx.ID, tx.AmountCents, tx.Status, tx.ClearedAt, tx.Note, raw); err != nil {
			log.Warn("update txn from crew", zap.Error(err))
		}
	})
	client.OnWatchError(func(err error) {
		r.setLastErr(err)
		log.Warn("watch error", zap.Error(err))
	})

	// Reconcile BEFORE StartWatching: the SDK's baseline poll silently absorbs
	// everything currently visible, so anything that arrived while no replica
	// held this connection must be ingested by us first.
	if err := r.reconcile(ctx, client, log); err != nil {
		return err
	}

	// Rebuild recurring classifications from stored history. Detection
	// otherwise only runs on new ingest, which leaves imported or migrated
	// history unclassified.
	if r.Conn.FamilyID != uuid.Nil {
		if n, err := categorize.ReclassifyFamily(ctx, r.Store, r.Conn.FamilyID); err != nil {
			log.Warn("reclassify recurring", zap.Error(err))
		} else if n > 0 {
			log.Info("classified recurring merchants", zap.Int("count", n))
		}
	}

	if err := client.StartWatching(ctx); err != nil {
		return err
	}
	defer client.StopWatching()

	// Initial snapshot so /api/accounts has data immediately after claim.
	r.refreshSnapshot(ctx, client, log)

	ticker := time.NewTicker(r.WatchInterval)
	defer ticker.Stop()
	// Notes edited in the Crew app don't trigger the SDK's update handler, and
	// neither does a pending transaction being cancelled, so reconcile both on
	// their own cadence.
	noteSync := time.NewTicker(noteSyncInterval)
	defer noteSync.Stop()
	r.syncRecent(ctx, client, true, log)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-client.WatchDone():
			// Watcher stopped on its own — permanently on 401.
			if err := r.getLastErr(); err != nil {
				return err
			}
			return errors.New("watcher stopped")
		case <-r.refreshCh:
			r.refreshSnapshot(ctx, client, log)
		case <-r.writeCh:
			r.drainWriteJobs(ctx, client, log)
		case <-noteSync.C:
			r.syncRecent(ctx, client, false, log)
		case <-ticker.C:
			st, held, err := r.Store.ConnectionStatusFenced(ctx, r.Lease)
			if err == nil {
				if !held {
					return errLeaseLost
				}
				if st == store.ConnDisabled {
					return errDisabled
				}
			}
			r.refreshSnapshot(ctx, client, log)
			r.drainWriteJobs(ctx, client, log)
			if ok, err := r.Store.TouchPolled(ctx, r.Lease); err == nil && !ok {
				return errLeaseLost
			}
		}
	}
}

// persistToken is the token-rotation callback: synchronous, fenced, critical.
// Losing a rotation kills the Crew session, so DB errors retry with backoff.
func (r *Runner) persistToken(log *zap.Logger) func(string) {
	return func(token string) {
		r.persistMu.Lock()
		defer r.persistMu.Unlock()
		ct, err := r.Box.Encrypt([]byte(token), r.Conn.ID[:])
		if err != nil {
			log.Error("token encrypt failed", zap.Error(err))
			return
		}
		for attempt := 0; attempt < 3; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			ok, err := r.Store.PersistRotatedToken(ctx, r.Lease, ct)
			cancel()
			if err == nil {
				if !ok {
					log.Warn("token rotation write fenced out — lease lost")
					r.setLastErr(errLeaseLost)
				}
				return
			}
			log.Error("persist rotated token failed", zap.Error(err), zap.Int("attempt", attempt+1))
			time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
		}
	}
}

// ingest idempotently stores a transaction and enqueues fresh ones for
// categorization. Returns whether this call created the row.
func (r *Runner) ingest(ctx context.Context, tx crew.CashTransaction, notify bool, log *zap.Logger) bool {
	raw, _ := json.Marshal(tx)
	payee := tx.Payee()
	var subID *string
	subName := ""
	if tx.Subaccount != nil {
		id := tx.Subaccount.ID
		subID = &id
		subName = tx.Subaccount.Name
	}
	id, fresh, err := r.Store.InsertTransaction(ctx, store.IngestTxn{
		FamilyID:       r.Conn.FamilyID,
		ConnectionID:   r.Conn.ID,
		CrewTxnID:      tx.ID,
		AmountCents:    tx.AmountCents,
		Payee:          payee,
		MerchantKey:    categorize.MerchantKey(payee),
		Title:          tx.Title,
		Description:    tx.Description,
		Status:         tx.Status,
		TxnType:        tx.Type,
		MCC:            tx.MCC,
		ImageURL:       tx.ImageURL,
		SubaccountID:   subID,
		SubaccountName: subName,
		OccurredAt:     tx.OccurredAt,
		ClearedAt:      tx.ClearedAt,
		Note:           tx.Note,
		DebitCardID:    debitCardID(tx),
		Raw:            raw,
	})
	if err != nil {
		log.Error("ingest txn", zap.Error(err), zap.String("crew_txn", tx.ID))
		return false
	}
	if fresh {
		r.Pipeline.Enqueue(categorize.Item{TxnID: id, Notify: notify})
	}
	return fresh
}

func (r *Runner) refreshSnapshot(ctx context.Context, client *crew.Client, log *zap.Logger) {
	cu, err := client.CurrentUser(ctx)
	if err != nil {
		r.setLastErr(err)
		if errors.Is(err, crew.ErrUnauthorized) {
			// Let the main loop classify it via WatchDone / next poll; also
			// record so session() returns the right error.
			log.Warn("snapshot refresh unauthorized")
		} else {
			log.Warn("snapshot refresh failed", zap.Error(err))
		}
		return
	}
	// Cards ride along in the snapshot so the dashboard can show which pocket
	// each card spends from without a live Crew call.
	cards, err := crewcards.Fetch(ctx, client, cu)
	if err != nil {
		log.Warn("card fetch failed; snapshot keeps previous cards", zap.Error(err))
		if prev, perr := r.Store.GetSnapshot(ctx, r.Conn.ID); perr == nil && prev != nil {
			var old struct {
				Cards []crewcards.Card `json:"cards"`
			}
			if json.Unmarshal(prev.Payload, &old) == nil {
				cards = old.Cards
			}
		}
	}
	payload, err := json.Marshal(map[string]any{"accounts": cu.Accounts, "cards": cards})
	if err != nil {
		return
	}
	if err := r.Store.UpsertAccountSnapshot(ctx, r.Conn.ID, payload); err != nil {
		log.Warn("save snapshot", zap.Error(err))
	}
}

func (r *Runner) setLastErr(err error) {
	r.errMu.Lock()
	r.lastErr = err
	r.errMu.Unlock()
}

func (r *Runner) getLastErr() error {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	return r.lastErr
}

// zapCrewAdapter adapts zap to go-crew's slog-shaped Logger interface.
type zapCrewAdapter struct{ s *zap.SugaredLogger }

func (a zapCrewAdapter) Debug(msg string, args ...any) { a.s.Debugw(msg, args...) }
func (a zapCrewAdapter) Info(msg string, args ...any)  { a.s.Infow(msg, args...) }
func (a zapCrewAdapter) Error(msg string, args ...any) { a.s.Errorw(msg, args...) }

// debitCardID is the card that paid, empty for bank transactions. It decides
// whether a notification is one member's business or the household's.
func debitCardID(tx crew.CashTransaction) string {
	if tx.DebitCard == nil {
		return ""
	}
	return tx.DebitCard.ID
}
