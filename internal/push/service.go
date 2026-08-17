// Package push sends Web Push notifications (VAPID) and manages subscriptions.
package push

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bemeek-io/crewmate/internal/store"
)

// Notification is the payload the service worker renders. The SW always calls
// showNotification from it — silent pushes are forbidden on iOS.
type Notification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
}

// Sender is the internal send interface consumed by the categorization
// pipeline and watcher. The only HTTP-exposed send is /api/push/test.
type Sender interface {
	SendToFamily(ctx context.Context, familyID uuid.UUID, n Notification)
	SendToUser(ctx context.Context, userID uuid.UUID, n Notification)
}

type Service struct {
	Store           *store.Store
	Log             *zap.Logger
	VAPIDPublicKey  string
	VAPIDPrivateKey string
	Subject         string // mailto: or https:
}

func (s *Service) SendToFamily(ctx context.Context, familyID uuid.UUID, n Notification) {
	subs, err := s.Store.ListPushSubscriptionsForFamily(ctx, familyID)
	if err != nil {
		s.Log.Error("list family push subscriptions", zap.Error(err))
		return
	}
	s.sendAll(ctx, subs, n)
}

func (s *Service) SendToUser(ctx context.Context, userID uuid.UUID, n Notification) {
	subs, err := s.Store.ListPushSubscriptionsForUser(ctx, userID)
	if err != nil {
		s.Log.Error("list user push subscriptions", zap.Error(err))
		return
	}
	s.sendAll(ctx, subs, n)
}

// SendResult is what one device's push gateway said. The test endpoint returns
// these so a silent phone can be explained from the phone itself — every other
// signal here is a log line on the server.
type SendResult struct {
	Status int    `json:"status"`
	Err    string `json:"error,omitempty"`
}

// SendToUserVerbose sends to the user's devices and reports each outcome.
// Used only by the test endpoint; normal sends are fire-and-forget.
func (s *Service) SendToUserVerbose(ctx context.Context, userID uuid.UUID, n Notification) ([]SendResult, error) {
	subs, err := s.Store.ListPushSubscriptionsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(n)
	if err != nil {
		return nil, err
	}
	out := make([]SendResult, 0, len(subs))
	for _, sub := range subs {
		out = append(out, s.sendOne(ctx, sub, payload))
	}
	return out, nil
}

func (s *Service) sendAll(ctx context.Context, subs []store.PushSubscription, n Notification) {
	payload, err := json.Marshal(n)
	if err != nil {
		return
	}
	for _, sub := range subs {
		s.sendOne(ctx, sub, payload)
	}
}

func (s *Service) sendOne(ctx context.Context, sub store.PushSubscription, payload []byte) SendResult {
	resp, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys:     webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
	}, &webpush.Options{
		Subscriber:      s.Subject,
		VAPIDPublicKey:  s.VAPIDPublicKey,
		VAPIDPrivateKey: s.VAPIDPrivateKey,
		TTL:             3600,
		Urgency:         webpush.UrgencyNormal,
	})
	if err != nil {
		s.Log.Warn("web push send failed", zap.Error(err))
		return SendResult{Err: err.Error()}
	}
	defer resp.Body.Close()
	// Push gateways explain a rejection in the body — Apple returns things like
	// BadJwtToken, FCM a JSON error. Without it a 403 is unactionable, so it's
	// captured (bounded) and reported rather than discarded.
	detail := ""
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		detail = strings.TrimSpace(string(body))
		host := "push service"
		if u, err := url.Parse(sub.Endpoint); err == nil && u.Host != "" {
			host = u.Host
		}
		s.Log.Warn("web push rejected",
			zap.Int("status", resp.StatusCode), zap.String("host", host),
			zap.String("body", detail))
		detail = host + ": " + detail
	}
	switch resp.StatusCode {
	case http.StatusNotFound, http.StatusGone:
		// Subscription is dead — prune it.
		if err := s.Store.DeletePushSubscriptionByEndpoint(ctx, sub.Endpoint); err != nil {
			s.Log.Warn("prune dead subscription", zap.Error(err))
		} else {
			s.Log.Info("pruned dead push subscription", zap.String("endpoint", sub.Endpoint))
		}
	}
	return SendResult{Status: resp.StatusCode, Err: detail}
}
