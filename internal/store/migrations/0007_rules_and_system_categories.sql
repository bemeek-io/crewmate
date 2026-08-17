-- System categories: a small set crewmate owns. They can be recolored but not
-- renamed or deleted, because other features (labeling a recurring series)
-- refer to them by meaning rather than by name.
ALTER TABLE categories ADD COLUMN system_key TEXT;
CREATE UNIQUE INDEX categories_system_idx ON categories(family_id, system_key)
    WHERE system_key IS NOT NULL;

-- Adopt any same-named category the family already made, so seeding doesn't
-- collide with it and their existing notes keep working.
UPDATE categories SET system_key = 'subscription'
 WHERE system_key IS NULL AND lower(name) = 'subscription';
UPDATE categories SET system_key = 'loan_payment'
 WHERE system_key IS NULL AND lower(name) IN ('loan payment', 'loan');

INSERT INTO categories (family_id, name, color, system_key)
SELECT f.id, 'Subscription', '#a78bfa', 'subscription'
  FROM families f
 WHERE NOT EXISTS (SELECT 1 FROM categories c
                    WHERE c.family_id = f.id
                      AND (c.system_key = 'subscription' OR lower(c.name) = 'subscription'));

INSERT INTO categories (family_id, name, color, system_key)
SELECT f.id, 'Loan Payment', '#38bdf8', 'loan_payment'
  FROM families f
 WHERE NOT EXISTS (SELECT 1 FROM categories c
                    WHERE c.family_id = f.id
                      AND (c.system_key = 'loan_payment' OR lower(c.name) = 'loan payment'));

-- Categorization rules run after a manual category and before the LLM, so a
-- family can encode "this vendor, in this amount range, is always X" without
-- paying for a model call — and without a notification, since a rule match is
-- an expected outcome rather than something to review.
CREATE TABLE category_rules (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    family_id        UUID NOT NULL REFERENCES families(id) ON DELETE CASCADE,
    category_id      UUID NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    -- Lower runs first; ties broken by most-specific, then oldest.
    priority         INT NOT NULL DEFAULT 100,
    -- Empty payee_match means "any vendor".
    payee_match      TEXT NOT NULL DEFAULT '',
    match_type       TEXT NOT NULL DEFAULT 'contains'
                     CHECK (match_type IN ('contains', 'equals', 'prefix')),
    mcc              TEXT NOT NULL DEFAULT '',
    -- Bounds compare against the absolute amount, so they read the way people
    -- describe them ("between $20 and $50") regardless of sign.
    min_amount_cents BIGINT,
    max_amount_cents BIGINT,
    direction        TEXT NOT NULL DEFAULT 'any'
                     CHECK (direction IN ('any', 'spend', 'income')),
    enabled          BOOLEAN NOT NULL DEFAULT true,
    -- 'series' rules are created by labeling a recurring series, and are
    -- removed again when that label is cleared.
    source           TEXT NOT NULL DEFAULT 'user' CHECK (source IN ('user', 'series')),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX category_rules_family_idx ON category_rules(family_id, enabled, priority);
-- One series-created rule per merchant.
CREATE UNIQUE INDEX category_rules_series_idx ON category_rules(family_id, payee_match)
    WHERE source = 'series';
