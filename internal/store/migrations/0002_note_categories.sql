-- Crew's per-transaction `note` field becomes the source of truth for a
-- transaction's category. Crewmate stores only the family's reusable category
-- list; a transaction's category is derived by matching its note against that
-- list. The local category_id/category_source columns and the merchant_rules
-- cache are therefore redundant and removed.

ALTER TABLE transactions ADD COLUMN note TEXT NOT NULL DEFAULT '';
ALTER TABLE transactions DROP COLUMN category_id;
ALTER TABLE transactions DROP COLUMN category_source;

DROP INDEX IF EXISTS txns_family_cat_idx;
CREATE INDEX txns_family_note_idx ON transactions(family_id, lower(note));

-- Merchant→category mappings are now derived from the notes of prior
-- transactions for the same merchant, so no separate rule table is needed.
DROP TABLE IF EXISTS merchant_rules;

-- Writing a note requires the Crew client for that transaction's connection,
-- which lives on exactly one replica (the lease holder). API requests and the
-- categorization pipeline enqueue work here; the holder drains it.
CREATE TABLE crew_write_jobs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id UUID NOT NULL REFERENCES crew_connections(id) ON DELETE CASCADE,
    crew_txn_id   TEXT NOT NULL,
    note          TEXT NOT NULL,
    attempts      INT NOT NULL DEFAULT 0,
    last_error    TEXT NOT NULL DEFAULT '',
    run_after     TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- One pending write per transaction; a newer request supersedes the older.
    UNIQUE (connection_id, crew_txn_id)
);
CREATE INDEX crew_write_jobs_conn_idx ON crew_write_jobs(connection_id, run_after);
