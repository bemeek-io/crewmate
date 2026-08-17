// Package config loads and validates all environment configuration at boot.
package config

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DatabaseURL string
	ListenAddr  string
	LogLevel    string
	AppBaseURL  string // e.g. https://crew.example.com — used for Origin checks and push URLs
	// RequireHTTPS is set when AppBaseURL is https, which is everywhere except
	// loopback development. It turns on HSTS.
	RequireHTTPS bool
	TrustProxy   bool

	// CrewTokenEncKey is the 32-byte AES-256-GCM key protecting Crew bearer tokens at rest.
	CrewTokenEncKey []byte

	AnthropicAPIKey string // optional; empty disables LLM categorization

	VAPIDPublicKey  string
	VAPIDPrivateKey string
	VAPIDSubject    string // mailto: or https:

	SessionTTL               time.Duration
	WatchInterval            time.Duration
	LeaseTTL                 time.Duration
	BackfillMonths           int
	MaxConnectionsPerReplica int // 0 = unlimited
}

func Load() (*Config, error) {
	c := &Config{
		DatabaseURL:              os.Getenv("DATABASE_URL"),
		ListenAddr:               getenv("LISTEN_ADDR", ":8080"),
		LogLevel:                 getenv("LOG_LEVEL", "info"),
		AppBaseURL:               strings.TrimRight(os.Getenv("APP_BASE_URL"), "/"),
		TrustProxy:               os.Getenv("TRUST_PROXY") == "true",
		AnthropicAPIKey:          os.Getenv("ANTHROPIC_API_KEY"),
		VAPIDPublicKey:           os.Getenv("VAPID_PUBLIC_KEY"),
		VAPIDPrivateKey:          os.Getenv("VAPID_PRIVATE_KEY"),
		VAPIDSubject:             os.Getenv("VAPID_SUBJECT"),
		SessionTTL:               getdur("SESSION_TTL", 180*24*time.Hour),
		WatchInterval:            getdur("WATCH_INTERVAL", 60*time.Second),
		LeaseTTL:                 getdur("LEASE_TTL", 60*time.Second),
		BackfillMonths:           getint("BACKFILL_MONTHS", 12),
		MaxConnectionsPerReplica: getint("MAX_CONNECTIONS_PER_REPLICA", 0),
	}

	if c.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if c.AppBaseURL == "" {
		return nil, fmt.Errorf("APP_BASE_URL is required (e.g. https://crew.example.com)")
	}
	u, err := url.Parse(c.AppBaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("APP_BASE_URL must be an absolute URL")
	}
	// This is banking data: it never travels in the clear. Plain http is
	// refused outright rather than degraded, so a misconfigured deploy fails at
	// boot instead of quietly serving balances over HTTP. Loopback is the one
	// exception — browsers treat it as a secure context, which is what makes
	// local development possible without certificates.
	if u.Scheme != "https" && !isLoopback(u.Hostname()) {
		return nil, fmt.Errorf(
			"APP_BASE_URL must use https (got %q); plain http is only allowed on localhost", c.AppBaseURL)
	}
	c.RequireHTTPS = u.Scheme == "https"

	rawKey := os.Getenv("CREW_TOKEN_ENC_KEY")
	if rawKey == "" {
		return nil, fmt.Errorf("CREW_TOKEN_ENC_KEY is required (base64 of 32 random bytes)")
	}
	key, err := base64.StdEncoding.DecodeString(rawKey)
	if err != nil {
		key, err = base64.URLEncoding.DecodeString(rawKey)
	}
	if err != nil {
		return nil, fmt.Errorf("CREW_TOKEN_ENC_KEY is not valid base64: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("CREW_TOKEN_ENC_KEY must decode to exactly 32 bytes, got %d", len(key))
	}
	c.CrewTokenEncKey = key

	if c.VAPIDPublicKey == "" || c.VAPIDPrivateKey == "" || c.VAPIDSubject == "" {
		return nil, fmt.Errorf("VAPID_PUBLIC_KEY, VAPID_PRIVATE_KEY and VAPID_SUBJECT are required (run with -generate-vapid to create keys)")
	}
	if !strings.HasPrefix(c.VAPIDSubject, "mailto:") && !strings.HasPrefix(c.VAPIDSubject, "https:") {
		return nil, fmt.Errorf("VAPID_SUBJECT must start with mailto: or https:")
	}

	if c.LeaseTTL < 15*time.Second {
		return nil, fmt.Errorf("LEASE_TTL must be at least 15s")
	}
	return c, nil
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getdur(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func getint(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// isLoopback reports whether a hostname is the local machine. Browsers treat
// loopback as a secure context, so service workers and Web Push work over
// plain http there and nowhere else.
func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
