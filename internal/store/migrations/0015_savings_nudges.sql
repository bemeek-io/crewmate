-- Savings nudges: a push when a purchase matches a savings suggestion.

-- Per person, not per family: one partner may want the nudge and the other
-- not. On by default; it's the point of the feature.
ALTER TABLE users ADD COLUMN savings_nudges BOOLEAN NOT NULL DEFAULT true;

-- Suggestions are computed in calendar months, and the months are the
-- family's. The browser reports its zone whenever suggestions are loaded, so
-- the background pipeline can cut months the same way the page does.
ALTER TABLE families ADD COLUMN timezone TEXT;

-- When each suggestion last nudged each person. A takeout habit is a purchase
-- most days; one reminder a day about it is plenty. Per person, like the
-- pushes themselves: a card swipe notifies only its cardholder, so a partner's
-- nudge in the morning mustn't use up yours for the evening.
CREATE TABLE insight_nudges (
    family_id    UUID NOT NULL REFERENCES families(id) ON DELETE CASCADE,
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subject_type TEXT NOT NULL,
    subject_key  TEXT NOT NULL,
    last_sent_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (family_id, user_id, subject_type, subject_key)
);
