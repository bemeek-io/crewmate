-- Recurring detection reworked.
--
-- The old table keyed a series by (merchant, amount) with a 5% tolerance,
-- which lumped variable spending — biweekly grocery runs of roughly similar
-- size — in with real subscriptions. A merchant now has exactly one series,
-- and the classification distinguishes:
--   subscription: same amount every time, on a steady schedule
--   recurring:    a repeating spend whose amount varies
-- CASCADE drops the dependent FK constraint but leaves transactions.recurring_id
-- in place, so the column has to go explicitly before it can be re-added.
DROP TABLE IF EXISTS recurring_series CASCADE;
ALTER TABLE transactions DROP COLUMN IF EXISTS recurring_id;

CREATE TABLE recurring_series (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    family_id            UUID NOT NULL REFERENCES families(id) ON DELETE CASCADE,
    merchant_key         TEXT NOT NULL,
    kind                 TEXT NOT NULL DEFAULT 'none'
                         CHECK (kind IN ('subscription', 'recurring', 'none')),
    typical_amount_cents BIGINT NOT NULL DEFAULT 0,  -- median of recent charges
    min_amount_cents     BIGINT NOT NULL DEFAULT 0,
    max_amount_cents     BIGINT NOT NULL DEFAULT 0,
    cadence              TEXT NOT NULL DEFAULT 'unknown'
                         CHECK (cadence IN ('weekly','biweekly','monthly','quarterly','yearly','unknown')),
    period_days          INT,
    -- Regularity diagnostics, surfaced in the UI so a classification can be
    -- understood (and tuned) rather than guessed at.
    interval_spread_pct  INT NOT NULL DEFAULT 0,  -- coefficient of variation, %
    amount_spread_pct    INT NOT NULL DEFAULT 0,  -- recent spread / median, %
    day_spread_days      INT NOT NULL DEFAULT 0,  -- day-of-month consistency
    first_seen_at        TIMESTAMPTZ NOT NULL,
    last_seen_at         TIMESTAMPTZ NOT NULL,
    occurrence_count     INT NOT NULL DEFAULT 1,
    dismissed            BOOLEAN NOT NULL DEFAULT false,
    UNIQUE (family_id, merchant_key)
);

-- transactions.recurring_id was dropped by the CASCADE above.
ALTER TABLE transactions
    ADD COLUMN recurring_id UUID REFERENCES recurring_series(id) ON DELETE SET NULL;
