# Crewmate

A self-hostable family banking companion for [Crew](https://trycrew.com), built on
[go-crew](https://github.com/bemeek-io/go-crew). Go backend (chi + zap + Postgres) with an
embedded React PWA, optimized for iPhones installed via **Share → Add to Home Screen**.

What it does:

- **Balances & pockets** — every family member's account and pocket balances in one place,
  refreshed continuously by a server-side watcher.
- **Instant transaction push** — Web Push (iOS-compatible) the moment a new transaction lands:
  *"$42.97 from Costco, auto-categorized as Groceries"*, or a *tap-to-categorize* prompt when
  the category isn't known. Tapping opens that transaction in the app.
- **Smart categorization, stored in Crew** — a transaction's category is written to its **note
  field in Crew**, so it shows up in the Crew app and there's no second copy to drift. Crewmate
  stores only your family's reusable category list. A new transaction reuses whatever category
  that merchant last got (history is the cache — including categories you set in the Crew app);
  a merchant nobody has ever categorized goes to Claude Haiku once. A note you wrote by hand is
  never overwritten automatically.
- **Two-way with the Crew app** — type `Subscription` into a transaction's note in Crew and
  crewmate picks it up. If the note doesn't match a category, crewmate offers to add it as one
  (labeling every transaction carrying that note at once) or to ignore it, for genuinely
  personal notes. Anything without a recognized category reads as **Misc**.
- **Subscription detection** — recurring charges (steady merchant + amount + cadence) are
  detected and surfaced.
- **Families** — one deployment serves many families; invite codes (single-use, 48 h) share
  categories and visibility between members.

## Architecture (multi-replica by design)

Replicas are symmetric and coordinate exclusively through Postgres:

- Crew issues **rotating bearer tokens** — only one process may use a connection's token.
  Each connection is claimed via a **lease** (`lease_holder`, `lease_epoch`, `lease_expires_at`);
  the holder runs that connection's watcher and persists token rotations with **epoch-fenced
  writes**, so a stale replica can never clobber state. Crash → lease expires → another replica
  takes over and reconciles missed transactions (idempotent ledger keyed on
  `(connection_id, crew_txn_id)`).
- **HTTP reads never touch Crew.** Balances are served from `account_snapshots` maintained by
  the holder each poll (≤ ~60 s stale, early refresh via `NOTIFY`). Any replica answers any
  request; no sticky sessions.
- **Writes to Crew are queued, not direct.** Setting a category means writing a note through the
  Crew client for *that transaction's* connection, which lives on one replica. API handlers
  enqueue into `crew_write_jobs`; a `NOTIFY` wakes the holder, which performs the mutation,
  mirrors the result locally, and retries with backoff. So any family member can categorize any
  member's transaction from any replica.
- Login is the **Crew OTP flow relayed through the backend** (SMS, then email if required).
  Phone numbers and OTP codes are never persisted; only the bearer token is stored, encrypted
  with AES-256-GCM (key = `CREW_TOKEN_ENC_KEY`, AAD-bound to its row). Sessions are httpOnly
  cookies backed by server-side rows. The Crew token is never sent to the browser.

## Quick start

Requirements: Docker (or Podman) with compose, and a public HTTPS hostname — **push
notifications, secure cookies, and the service worker do not work over plain HTTP**.

```sh
cp .env.example .env

# 1. Fill in APP_BASE_URL / APP_DOMAIN (your public hostname)
# 2. Generate secrets:
openssl rand -base64 32          # -> CREW_TOKEN_ENC_KEY
docker compose build app
docker compose run --rm --entrypoint /crewmate app -generate-vapid   # -> VAPID keys
# 3. Optional: set ANTHROPIC_API_KEY for AI categorization

docker compose up -d --scale app=2
```

The bundled Caddy terminates TLS (automatic certificates for real domains) and round-robins
across `app` replicas. Already have a proxy? Drop the `proxy` service, publish `app`'s port,
and load-balance yourself — keep `TRUST_PROXY=true` only when a proxy sets `X-Forwarded-For`.

First run: open the app on your phone → sign in with your Crew phone number (SMS + email
codes) → create your family → add categories → **Share → Add to Home Screen** → launch from
the icon → Settings → *Enable notifications*.

### Verifying a deployment

- `curl -s https://<host>/api/health` → `{"ok":true}`
- `docker compose exec db psql -U crewmate -c "select id, status, lease_holder, lease_expires_at from crew_connections;"`
  — each active connection is held by exactly one replica; kill that replica and another
  claims it within ~75 s.
- Settings → *Send test notification* should land as a banner on the installed PWA.

## Development

```sh
# backend (needs Postgres + env vars from .env.example)
go run ./cmd/crewmate

# frontend dev server (proxies /api to :8080)
cd web && npm install && npm run dev

# tests
go vet ./... && go test ./...
```

The binary embeds `web/dist` (`go:embed`), so `npm run build` before `go build` for a
self-contained artifact. There is no Makefile by design — the commands above are the whole
build surface.

## Configuration

| Variable | Required | Purpose |
|---|---|---|
| `DATABASE_URL` | ✅ | Postgres DSN (compose wires it) |
| `APP_BASE_URL` | ✅ | Public origin, e.g. `https://crew.example.com` — Origin/CSRF checks |
| `CREW_TOKEN_ENC_KEY` | ✅ | base64, 32 bytes — AES-256-GCM key for Crew tokens |
| `VAPID_PUBLIC_KEY` / `VAPID_PRIVATE_KEY` / `VAPID_SUBJECT` | ✅ | Web Push (subject = `mailto:` or `https:` URL) |
| `ANTHROPIC_API_KEY` | — | Enables Claude Haiku categorization; unset = manual prompts only |
| `TRUST_PROXY` | — | `true` when behind a proxy setting `X-Forwarded-*` |
| `WATCH_INTERVAL` | — | Crew poll cadence (default `60s`; don't lower it aggressively) |
| `LEASE_TTL` | — | Failover window (default `60s`) |
| `BACKFILL_MONTHS` | — | History imported on first connect (default `12`) |
| `MAX_CONNECTIONS_PER_REPLICA` | — | Cap held connections per replica (`0` = unlimited) |
| `SESSION_TTL`, `LISTEN_ADDR`, `LOG_LEVEL` | — | Tuning |

## Security notes

- Real banking data — deploy only behind HTTPS, keep `.env` out of version control, and treat
  `CREW_TOKEN_ENC_KEY` like a master key (losing it just forces everyone to re-login; leaking
  it plus a DB dump exposes Crew sessions).
- Phone numbers/OTPs transit the backend during login only; they are never written to disk or DB.
- Every family-scoped query derives the family from the session, never from request input.
- Auth endpoints are rate-limited (DB-backed, replica-safe); state-changing requests require a
  same-origin `Origin` and an `X-Crewmate` header (CSRF).
- Crew rate limits are undocumented — the default 60 s poll interval is deliberate.
