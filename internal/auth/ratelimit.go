package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/bemeek-io/crewmate/internal/store"
)

// Limiter enforces fixed-window rate limits backed by Postgres so they hold
// across replicas. Keys derived from phone numbers are HMAC'd — the raw phone
// is never written to the database.
type Limiter struct {
	Store *store.Store
	// MAC key: reuse of the token encryption key would broaden its purpose,
	// so derive an independent key at construction instead.
	MACKey []byte
}

func (l *Limiter) mac(v string) string {
	m := hmac.New(sha256.New, l.MACKey)
	m.Write([]byte(v))
	return hex.EncodeToString(m.Sum(nil)[:16])
}

func (l *Limiter) AllowIP(ctx context.Context, scope, ip string, window time.Duration, limit int) bool {
	ok, err := l.Store.RateAllow(ctx, scope+":ip:"+l.mac(ip), window, limit)
	return err == nil && ok
}

func (l *Limiter) AllowPhone(ctx context.Context, phone string, window time.Duration, limit int) bool {
	ok, err := l.Store.RateAllow(ctx, "sms:phone:"+l.mac(phone), window, limit)
	return err == nil && ok
}
