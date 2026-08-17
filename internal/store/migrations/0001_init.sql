CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE users (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    crew_user_id  TEXT NOT NULL UNIQUE,
    first_name    TEXT NOT NULL DEFAULT '',
    last_name     TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Deliberately NOT stored: phone, email, OTP codes. Crew PII stays out of the DB.

CREATE TABLE sessions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash    BYTEA NOT NULL UNIQUE,
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL,
    user_agent    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user_idx ON sessions(user_id);
CREATE INDEX sessions_expires_idx ON sessions(expires_at);

-- Replica-safe pending OTP logins: any replica can serve any step.
CREATE TABLE pending_logins (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    stage            TEXT NOT NULL CHECK (stage IN ('sms','email')),
    phone_id         TEXT NOT NULL DEFAULT '',
    email_id         TEXT NOT NULL DEFAULT '',
    token_ciphertext BYTEA,
    attempts         INT NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at       TIMESTAMPTZ NOT NULL
);
CREATE INDEX pending_logins_expires_idx ON pending_logins(expires_at);

CREATE TABLE families (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE family_members (
    family_id  UUID NOT NULL REFERENCES families(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT NOT NULL CHECK (role IN ('admin','member')),
    joined_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (family_id, user_id),
    UNIQUE (user_id)
);

CREATE TABLE family_invites (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    family_id   UUID NOT NULL REFERENCES families(id) ON DELETE CASCADE,
    code        TEXT NOT NULL UNIQUE,
    created_by  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at  TIMESTAMPTZ NOT NULL,
    max_uses    INT NOT NULL DEFAULT 1,
    use_count   INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE crew_connections (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    token_ciphertext BYTEA NOT NULL,
    key_version      INT NOT NULL DEFAULT 1,
    status           TEXT NOT NULL DEFAULT 'active'
                     CHECK (status IN ('active','needs_relogin','disabled')),
    last_rotated_at  TIMESTAMPTZ,
    last_polled_at   TIMESTAMPTZ,
    -- Lease: exactly one replica owns the *crew.Client for this connection.
    lease_holder     TEXT,
    lease_epoch      BIGINT NOT NULL DEFAULT 0,
    lease_expires_at TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX crew_connections_lease_idx ON crew_connections(status, lease_expires_at);

CREATE TABLE account_snapshots (
    connection_id UUID PRIMARY KEY REFERENCES crew_connections(id) ON DELETE CASCADE,
    payload       JSONB NOT NULL,
    fetched_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE categories (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    family_id   UUID NOT NULL REFERENCES families(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    emoji       TEXT NOT NULL DEFAULT '',
    color       TEXT NOT NULL DEFAULT '',
    created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX categories_family_name_idx ON categories(family_id, lower(name));

CREATE TABLE merchant_rules (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    family_id    UUID NOT NULL REFERENCES families(id) ON DELETE CASCADE,
    merchant_key TEXT NOT NULL,
    category_id  UUID NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    source       TEXT NOT NULL CHECK (source IN ('user','llm')),
    confidence   TEXT NOT NULL DEFAULT 'high' CHECK (confidence IN ('high','low')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (family_id, merchant_key)
);

CREATE TABLE recurring_series (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    family_id         UUID NOT NULL REFERENCES families(id) ON DELETE CASCADE,
    merchant_key      TEXT NOT NULL,
    amount_cents      BIGINT NOT NULL,
    amount_tolerance  INT NOT NULL DEFAULT 0,
    cadence           TEXT NOT NULL DEFAULT 'unknown'
                      CHECK (cadence IN ('weekly','biweekly','monthly','quarterly','yearly','unknown')),
    period_days       INT,
    first_seen_at     TIMESTAMPTZ NOT NULL,
    last_seen_at      TIMESTAMPTZ NOT NULL,
    occurrence_count  INT NOT NULL DEFAULT 1,
    is_subscription   BOOLEAN NOT NULL DEFAULT false,
    dismissed         BOOLEAN NOT NULL DEFAULT false,
    UNIQUE (family_id, merchant_key, amount_cents)
);

-- Transaction cache AND processed-transaction ledger.
-- UNIQUE(connection_id, crew_txn_id) + INSERT ... ON CONFLICT DO NOTHING gives
-- exactly-once processing on top of at-least-once delivery (watcher + reconcile).
CREATE TABLE transactions (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    family_id        UUID NOT NULL REFERENCES families(id) ON DELETE CASCADE,
    connection_id    UUID NOT NULL REFERENCES crew_connections(id) ON DELETE CASCADE,
    crew_txn_id      TEXT NOT NULL,
    amount_cents     BIGINT NOT NULL,
    payee            TEXT NOT NULL DEFAULT '',
    merchant_key     TEXT NOT NULL DEFAULT '',   -- normalized payee, computed in Go at ingest
    title            TEXT NOT NULL DEFAULT '',
    description      TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL DEFAULT '',
    txn_type         TEXT NOT NULL DEFAULT '',
    mcc              TEXT NOT NULL DEFAULT '',
    image_url        TEXT NOT NULL DEFAULT '',
    subaccount_id    TEXT,
    subaccount_name  TEXT NOT NULL DEFAULT '',
    occurred_at      TIMESTAMPTZ NOT NULL,
    cleared_at       TIMESTAMPTZ,
    category_id      UUID REFERENCES categories(id) ON DELETE SET NULL,
    category_source  TEXT NOT NULL DEFAULT 'none'
                     CHECK (category_source IN ('none','rule','llm','user')),
    recurring_id     UUID REFERENCES recurring_series(id) ON DELETE SET NULL,
    notified_at      TIMESTAMPTZ,
    processed_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    raw              JSONB,
    UNIQUE (connection_id, crew_txn_id)
);
CREATE INDEX txns_family_time_idx ON transactions(family_id, occurred_at DESC, id DESC);
CREATE INDEX txns_family_cat_idx ON transactions(family_id, category_id);
CREATE INDEX txns_family_merchant_idx ON transactions(family_id, merchant_key);

CREATE TABLE push_subscriptions (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    endpoint     TEXT NOT NULL UNIQUE,
    p256dh       TEXT NOT NULL,
    auth         TEXT NOT NULL,
    user_agent   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX push_user_idx ON push_subscriptions(user_id);

-- Fixed-window rate limit counters (replica-safe).
CREATE TABLE rate_limits (
    key          TEXT NOT NULL,
    window_start TIMESTAMPTZ NOT NULL,
    count        INT NOT NULL DEFAULT 0,
    PRIMARY KEY (key, window_start)
);
