-- Savings suggestions.
--
-- The suggestions themselves are computed on request from transactions and
-- are never stored: stored numbers would go stale the moment a charge lands.
-- What is kept is what the family and the model said about them.

-- "Not applicable": tithing, taxes, the mortgage — spending the family has
-- decided is necessary to them, whatever it looks like from the numbers. A
-- dismissal covers one merchant, or one whole category ("never flag Taxes").
CREATE TABLE insight_dismissals (
    family_id    UUID NOT NULL REFERENCES families(id) ON DELETE CASCADE,
    subject_type TEXT NOT NULL CHECK (subject_type IN ('merchant', 'category')),
    subject_key  TEXT NOT NULL,  -- merchant_key, or the category's id
    label        TEXT NOT NULL,  -- what it was called, for the restore list
    dismissed_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (family_id, subject_type, subject_key)
);

-- The model's judgement of whether a flagged spend looks necessary, cached so
-- that opening the page doesn't cost a model call each time. fingerprint is
-- what the judgement was made about; when it changes (a merchant moves
-- category, a subscription becomes a habit) the verdict is made again.
CREATE TABLE insight_verdicts (
    family_id    UUID NOT NULL REFERENCES families(id) ON DELETE CASCADE,
    subject_type TEXT NOT NULL CHECK (subject_type IN ('merchant', 'category')),
    subject_key  TEXT NOT NULL,
    fingerprint  TEXT NOT NULL,
    verdict      TEXT NOT NULL CHECK (verdict IN ('essential', 'discretionary', 'unclear')),
    note         TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (family_id, subject_type, subject_key)
);
