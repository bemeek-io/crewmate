// Command crewmate runs the API server, lease scheduler, transaction watcher,
// and categorization pipeline. Replicas are symmetric — run as many as you
// like against one Postgres.
package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/bemeek-io/crewmate/internal/auth"
	"github.com/bemeek-io/crewmate/internal/categoriesapi"
	"github.com/bemeek-io/crewmate/internal/categorize"
	"github.com/bemeek-io/crewmate/internal/config"
	"github.com/bemeek-io/crewmate/internal/crewlink"
	"github.com/bemeek-io/crewmate/internal/crypto"
	"github.com/bemeek-io/crewmate/internal/family"
	"github.com/bemeek-io/crewmate/internal/leases"
	"github.com/bemeek-io/crewmate/internal/push"
	"github.com/bemeek-io/crewmate/internal/server"
	"github.com/bemeek-io/crewmate/internal/store"
	"github.com/bemeek-io/crewmate/internal/transactionsapi"
	"github.com/bemeek-io/crewmate/web"
)

func main() {
	genVAPID := flag.Bool("generate-vapid", false, "generate a VAPID key pair and exit")
	flag.Parse()
	if *genVAPID {
		priv, pub, err := webpush.GenerateVAPIDKeys()
		if err != nil {
			fmt.Fprintln(os.Stderr, "generate vapid keys:", err)
			os.Exit(1)
		}
		fmt.Printf("VAPID_PUBLIC_KEY=%s\nVAPID_PRIVATE_KEY=%s\n", pub, priv)
		return
	}

	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "crewmate:", err)
		os.Exit(1)
	}
}

func newLogger(level string) (*zap.Logger, error) {
	lvl, err := zapcore.ParseLevel(level)
	if err != nil {
		lvl = zapcore.InfoLevel
	}
	cfg := zap.NewProductionConfig()
	cfg.Level = zap.NewAtomicLevelAt(lvl)
	cfg.EncoderConfig.TimeKey = "ts"
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	return cfg.Build()
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log, err := newLogger(cfg.LogLevel)
	if err != nil {
		return err
	}
	defer log.Sync()

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(rootCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(rootCtx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	log.Info("database ready")

	// Not fatal — push breaks, the rest of the app doesn't — but every send
	// would 403, so it must not pass quietly.
	if cfg.VAPIDKeyError != "" {
		log.Error("VAPID keys are misconfigured; push will fail",
			zap.String("detail", cfg.VAPIDKeyError))
	}

	box, err := crypto.NewBox(cfg.CrewTokenEncKey)
	if err != nil {
		return err
	}

	// Independent MAC key for rate-limit hashing, derived from the enc key so
	// operators manage a single secret.
	macKey := sha256.Sum256(append([]byte("crewmate-mac:"), cfg.CrewTokenEncKey...))

	pushSvc := &push.Service{
		Store:           st,
		Log:             log,
		VAPIDPublicKey:  cfg.VAPIDPublicKey,
		VAPIDPrivateKey: cfg.VAPIDPrivateKey,
		Subject:         cfg.VAPIDSubject,
	}
	llm := categorize.NewLLM(cfg.AnthropicAPIKey, log)
	pipeline := categorize.NewPipeline(st, llm, pushSvc, log)
	pipeline.Start(rootCtx, 2)

	scheduler := &leases.Scheduler{
		Store:          st,
		Box:            box,
		Log:            log,
		Pipeline:       pipeline,
		Push:           pushSvc,
		LeaseTTL:       cfg.LeaseTTL,
		WatchInterval:  cfg.WatchInterval,
		BackfillMonths: cfg.BackfillMonths,
		MaxPerReplica:  cfg.MaxConnectionsPerReplica,
	}
	go scheduler.Run(rootCtx)

	sessions := &auth.Sessions{
		Store:  st,
		TTL:    cfg.SessionTTL,
		Secure: strings.HasPrefix(cfg.AppBaseURL, "https://"),
	}
	limiter := &auth.Limiter{Store: st, MACKey: macKey[:]}
	authH := &auth.Handlers{
		Store:               st,
		Box:                 box,
		Sessions:            sessions,
		Limiter:             limiter,
		Log:                 log,
		OnConnectionChanged: scheduler.Nudge,
	}
	familyH := &family.Handlers{Store: st, Log: log}
	crewH := &crewlink.Handlers{Store: st, Log: log}
	txnsH := &transactionsapi.Handlers{Store: st, Log: log}
	catsH := &categoriesapi.Handlers{Store: st, Log: log}
	pushH := &push.Handlers{Service: pushSvc}

	dist, err := web.Dist()
	if err != nil {
		return fmt.Errorf("frontend embed: %w", err)
	}

	handler := server.NewRouter(server.Deps{
		Log:          log,
		Store:        st,
		Auth:         authH,
		Sessions:     sessions,
		Family:       familyH,
		Crew:         crewH,
		Txns:         txnsH,
		Categories:   catsH,
		Push:         pushH,
		AppBaseURL:   cfg.AppBaseURL,
		RequireHTTPS: cfg.RequireHTTPS,
		TrustProxy:   cfg.TrustProxy,
		WebDist:      dist,
	})

	go janitor(rootCtx, st, log)

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", zap.String("addr", cfg.ListenAddr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-rootCtx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	// Lease scheduler shutdown (release leases) runs off rootCtx cancellation;
	// give it a moment to finish before the process exits.
	time.Sleep(2 * time.Second)
	return nil
}

// janitor clears expired sessions, pending logins, and old rate-limit windows.
func janitor(ctx context.Context, st *store.Store, log *zap.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for name, fn := range map[string]func(context.Context) error{
				"sessions":       st.DeleteExpiredSessions,
				"pending_logins": st.DeleteExpiredPendingLogins,
				"rate_windows":   st.DeleteExpiredRateWindows,
			} {
				if err := fn(ctx); err != nil {
					log.Warn("janitor "+name, zap.Error(err))
				}
			}
		}
	}
}
