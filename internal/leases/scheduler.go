// Package leases distributes crew connections across replicas. Each replica
// runs one Scheduler that claims expired/unheld connection leases, runs a
// watcher.Runner per held connection, renews heartbeats, and stops runners
// immediately when a lease is lost.
package leases

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bemeek-io/crewmate/internal/categorize"
	"github.com/bemeek-io/crewmate/internal/crypto"
	"github.com/bemeek-io/crewmate/internal/push"
	"github.com/bemeek-io/crewmate/internal/store"
	"github.com/bemeek-io/crewmate/internal/watcher"
)

type heldConn struct {
	lease  store.Lease
	runner *watcher.Runner
	cancel context.CancelFunc
	done   chan struct{}
}

type Scheduler struct {
	Store          *store.Store
	Box            *crypto.Box
	Log            *zap.Logger
	Pipeline       *categorize.Pipeline
	Push           push.Sender
	LeaseTTL       time.Duration
	WatchInterval  time.Duration
	BackfillMonths int
	MaxPerReplica  int

	HolderID string

	mu      sync.Mutex
	held    map[uuid.UUID]*heldConn
	nudgeCh chan struct{}
}

// NewHolderID builds a replica identity: hostname plus random suffix, so
// restarted containers never collide with their own stale leases.
func NewHolderID() string {
	host, _ := os.Hostname()
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return host + "-" + hex.EncodeToString(b)
}

// Nudge triggers an immediate claim pass (e.g. right after a login created a
// new connection) instead of waiting for the next tick.
func (s *Scheduler) Nudge() {
	select {
	case s.nudgeCh <- struct{}{}:
	default:
	}
}

// Run blocks until ctx is canceled, then gracefully stops all runners and
// releases their leases.
func (s *Scheduler) Run(ctx context.Context) {
	if s.HolderID == "" {
		s.HolderID = NewHolderID()
	}
	s.held = make(map[uuid.UUID]*heldConn)
	s.nudgeCh = make(chan struct{}, 1)
	log := s.Log.With(zap.String("holder", s.HolderID))
	log.Info("lease scheduler starting")

	go s.listen(ctx, log, "crew_refresh", func(hc *heldConn) { hc.runner.RequestRefresh() })
	go s.listen(ctx, log, "crew_write", func(hc *heldConn) { hc.runner.RequestWriteDrain() })

	tick := s.LeaseTTL / 4
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	s.pass(ctx, log)
	for {
		select {
		case <-ctx.Done():
			s.shutdown(log)
			return
		case <-ticker.C:
			s.pass(ctx, log)
		case <-s.nudgeCh:
			s.pass(ctx, log)
		}
	}
}

// pass = reap finished runners, renew held leases, claim new connections.
func (s *Scheduler) pass(ctx context.Context, log *zap.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Reap runners that exited on their own (needs_relogin, disabled, lease lost).
	for id, hc := range s.held {
		select {
		case <-hc.done:
			// MarkNeedsRelogin/disable paths already handled lease state where
			// relevant; releasing again is fenced and harmless.
			_ = s.Store.ReleaseLease(context.WithoutCancel(ctx), hc.lease)
			delete(s.held, id)
			log.Info("runner exited", zap.String("conn", id.String()))
		default:
		}
	}

	// Renew. A failed renewal means the lease expired or was taken: stop the
	// runner immediately so its client can't race the new holder's token.
	for id, hc := range s.held {
		ok, err := s.Store.RenewLease(ctx, hc.lease, s.LeaseTTL)
		if err != nil {
			log.Warn("lease renew error (keeping runner until next pass)", zap.Error(err))
			continue
		}
		if !ok {
			log.Warn("lease lost, stopping runner", zap.String("conn", id.String()))
			hc.cancel()
			delete(s.held, id)
		}
	}

	// Claim.
	limit := 0
	if s.MaxPerReplica > 0 {
		limit = s.MaxPerReplica - len(s.held)
		if limit <= 0 {
			return
		}
	}
	claimed, err := s.Store.ClaimLeases(ctx, s.HolderID, s.LeaseTTL, limit)
	if err != nil {
		log.Warn("claim leases", zap.Error(err))
		return
	}
	for _, conn := range claimed {
		if _, exists := s.held[conn.ID]; exists {
			continue
		}
		s.startRunner(ctx, conn, log)
	}
}

func (s *Scheduler) startRunner(parent context.Context, conn *store.CrewConnection, log *zap.Logger) {
	lease := store.Lease{ConnID: conn.ID, Holder: s.HolderID, Epoch: conn.LeaseEpoch}
	rctx, cancel := context.WithCancel(parent)
	r := &watcher.Runner{
		Store:          s.Store,
		Box:            s.Box,
		Log:            s.Log,
		Pipeline:       s.Pipeline,
		Push:           s.Push,
		Conn:           conn,
		Lease:          lease,
		WatchInterval:  s.WatchInterval,
		BackfillMonths: s.BackfillMonths,
	}
	hc := &heldConn{lease: lease, runner: r, cancel: cancel, done: make(chan struct{})}
	s.held[conn.ID] = hc
	log.Info("claimed connection", zap.String("conn", conn.ID.String()), zap.Int64("epoch", conn.LeaseEpoch))
	go func() {
		defer close(hc.done)
		r.Run(rctx)
	}()
}

func (s *Scheduler) shutdown(log *zap.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	log.Info("lease scheduler shutting down", zap.Int("held", len(s.held)))
	for _, hc := range s.held {
		hc.cancel()
	}
	deadline := time.After(10 * time.Second)
	for id, hc := range s.held {
		select {
		case <-hc.done:
		case <-deadline:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = s.Store.ReleaseLease(ctx, hc.lease)
		cancel()
		delete(s.held, id)
	}
}

// listen forwards NOTIFY payloads (connection IDs) on a channel to the runner
// holding that connection, so cross-replica requests reach the one process
// that owns the Crew client.
func (s *Scheduler) listen(ctx context.Context, log *zap.Logger, channel string, deliver func(*heldConn)) {
	for ctx.Err() == nil {
		if err := s.listenOnce(ctx, channel, deliver); err != nil && ctx.Err() == nil {
			log.Warn("notify listener reconnecting", zap.String("channel", channel), zap.Error(err))
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}

func (s *Scheduler) listenOnce(ctx context.Context, channel string, deliver func(*heldConn)) error {
	conn, err := s.Store.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+pgIdent(channel)); err != nil {
		return err
	}
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		id, err := uuid.Parse(n.Payload)
		if err != nil {
			continue
		}
		s.mu.Lock()
		hc := s.held[id]
		s.mu.Unlock()
		if hc != nil {
			deliver(hc)
		}
	}
}

// pgIdent guards the LISTEN channel name, which cannot be parameterized.
// Channels are compile-time constants here; this keeps it that way.
func pgIdent(s string) string {
	for _, r := range s {
		if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			panic("leases: invalid notify channel name " + s)
		}
	}
	return s
}
