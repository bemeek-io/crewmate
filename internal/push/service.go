// Package push sends Web Push notifications (VAPID) and manages subscriptions.
package push

import (
	"context"
	"encoding/json"
	"net/http"

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

func (s *Service) sendAll(ctx context.Context, subs []store.PushSubscription, n Notification) {
	payload, err := json.Marshal(n)
	if err != nil {
		return
	}
	for _, sub := range subs {
		s.sendOne(ctx, sub, payload)
	}
}

func (s *Service) sendOne(ctx context.Context, sub store.PushSubscription, payload []byte) {
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
		return
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotFound, http.StatusGone:
		// Subscription is dead — prune it.
		if err := s.Store.DeletePushSubscriptionByEndpoint(ctx, sub.Endpoint); err != nil {
			s.Log.Warn("prune dead subscription", zap.Error(err))
		} else {
			s.Log.Info("pruned dead push subscription", zap.String("endpoint", sub.Endpoint))
		}
	default:
		if resp.StatusCode >= 400 {
			s.Log.Warn("web push non-success", zap.Int("status", resp.StatusCode))
		}
	}
}
