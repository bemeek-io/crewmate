// Package server wires the chi router, middleware stack, and embedded SPA.
package server

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/bemeek-io/crewmate/internal/auth"
	"github.com/bemeek-io/crewmate/internal/categoriesapi"
	"github.com/bemeek-io/crewmate/internal/crewlink"
	"github.com/bemeek-io/crewmate/internal/family"
	"github.com/bemeek-io/crewmate/internal/httpx"
	"github.com/bemeek-io/crewmate/internal/push"
	"github.com/bemeek-io/crewmate/internal/store"
	"github.com/bemeek-io/crewmate/internal/transactionsapi"
)

type Deps struct {
	Log        *zap.Logger
	Store      *store.Store
	Auth       *auth.Handlers
	Sessions   *auth.Sessions
	Family     *family.Handlers
	Crew       *crewlink.Handlers
	Txns       *transactionsapi.Handlers
	Categories *categoriesapi.Handlers
	Push       *push.Handlers
	AppBaseURL string
	// RequireHTTPS enables HSTS. Comes from config, which has already refused
	// to boot on a non-loopback http base URL.
	RequireHTTPS bool
	TrustProxy   bool
	WebDist      fs.FS // nil disables SPA serving (API-only dev mode)
}

func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	if d.TrustProxy {
		r.Use(middleware.RealIP)
	}
	r.Use(middleware.RequestID)
	r.Use(zapRequestLogger(d.Log))
	r.Use(middleware.Recoverer)
	r.Use(securityHeaders(d.RequireHTTPS))
	r.Use(middleware.Compress(5))
	r.Use(httpx.MaxBytes(64 << 10))

	r.Route("/api", func(api chi.Router) {
		api.Use(auth.CSRF(d.AppBaseURL))
		api.NotFound(func(w http.ResponseWriter, r *http.Request) {
			httpx.Error(w, http.StatusNotFound, "not_found", "no such endpoint")
		})

		api.Get("/health", func(w http.ResponseWriter, r *http.Request) {
			if err := d.Store.Ping(r.Context()); err != nil {
				httpx.Error(w, http.StatusServiceUnavailable, "db_down", "database unreachable")
				return
			}
			httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
		})

		// Public auth endpoints (rate-limited internally).
		api.Post("/auth/sms", d.Auth.StartSMS)
		api.Post("/auth/sms/verify", d.Auth.VerifySMS)
		api.Post("/auth/email/verify", d.Auth.VerifyEmail)

		// Session-scoped (no family required yet).
		api.Group(func(pr chi.Router) {
			pr.Use(d.Sessions.RequireSession)
			pr.Post("/auth/logout", d.Auth.Logout)
			pr.Get("/me", d.Crew.Me)
			pr.Get("/crew/status", d.Crew.Status)
			pr.Delete("/crew/connection", d.Crew.Disconnect)
			pr.Get("/push/vapid-public-key", d.Push.VAPIDPublicKey)
			pr.Post("/push/subscriptions", d.Push.Subscribe)
			pr.Delete("/push/subscriptions", d.Push.Unsubscribe)
			pr.Post("/push/test", d.Push.Test)
		})

		// Family-scoped.
		api.Group(func(fr chi.Router) {
			fr.Use(d.Sessions.RequireSession)
			fr.Use(d.Family.RequireMembership)

			fr.Get("/accounts", d.Crew.Accounts)
			fr.Put("/cards/{cardID}/pocket", d.Crew.MoveCardPocket)

			fr.Get("/cashflow", d.Txns.CashFlow)
			fr.Get("/transactions", d.Txns.List)
			fr.Get("/transactions/{id}", d.Txns.Get)
			fr.Patch("/transactions/{id}/category", d.Txns.SetCategory)

			fr.Get("/categories", d.Categories.List)
			fr.Post("/categories", d.Categories.Create)
			fr.Patch("/categories/{id}", d.Categories.Update)
			fr.Delete("/categories/{id}", d.Categories.Delete)

			fr.Post("/categorize/reassess", d.Categories.Reassess)

			fr.Get("/rules", d.Categories.ListRules)
			fr.Post("/rules", d.Categories.CreateRule)
			fr.Patch("/rules/{id}", d.Categories.UpdateRule)
			fr.Delete("/rules/{id}", d.Categories.DeleteRule)

			fr.Get("/notes/unmatched", d.Categories.Unmatched)
			fr.Post("/notes/ignore", d.Categories.Ignore)
			fr.Delete("/notes/ignore", d.Categories.Unignore)

			fr.Get("/recurring", d.Txns.ListRecurring)
			fr.Get("/recurring/{id}/transactions", d.Txns.RecurringTransactions)
			fr.Put("/recurring/{id}/label", d.Txns.LabelRecurring)
			fr.Patch("/recurring/{id}", d.Txns.PatchRecurring)
		})
	})

	if d.WebDist != nil {
		spa := SPAHandler(d.WebDist)
		r.NotFound(func(w http.ResponseWriter, req *http.Request) {
			if strings.HasPrefix(req.URL.Path, "/api/") {
				httpx.Error(w, http.StatusNotFound, "not_found", "no such endpoint")
				return
			}
			spa.ServeHTTP(w, req)
		})
	}
	return r
}
