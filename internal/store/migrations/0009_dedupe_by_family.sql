-- A Crew transaction belongs to a household, not to whoever's connection
-- happened to fetch it.
--
-- Both members of a family connect to the same Crew accounts, so both watchers
-- see the same cashTransactions. Keyed on (connection_id, crew_txn_id) each
-- watcher inserted its own copy, so every transaction appeared twice in
-- Activity and counted twice in the cash flow report. Re-keying on
-- (family_id, crew_txn_id) makes the second watcher's insert the no-op it
-- should always have been.

-- Collapse existing duplicates, keeping the most complete copy: a categorized
-- one over an uncategorized one (the note only got mirrored onto whichever
-- connection performed the write), then one that has already notified, then
-- the oldest. Losing the wrong copy would resurface an old transaction as a
-- push or drop its category from the local cache.
--
-- The deletion is recoverable in the worst case: this table is a cache of
-- Crew's data, categories live in Crew's note field, and reconciliation
-- re-ingests anything missing.
WITH ranked AS (
    SELECT id,
           row_number() OVER (
               PARTITION BY family_id, crew_txn_id
               ORDER BY (note <> '') DESC,
                        (notified_at IS NOT NULL) DESC,
                        processed_at,
                        id
           ) AS rn
    FROM transactions
)
DELETE FROM transactions t
USING ranked
WHERE t.id = ranked.id AND ranked.rn > 1;

-- Drop the old key by definition rather than by name, since the constraint may
-- have been created either as a table constraint or an index.
DO $$
DECLARE conname_found TEXT;
BEGIN
    SELECT c.conname INTO conname_found
      FROM pg_constraint c
     WHERE c.conrelid = 'transactions'::regclass
       AND c.contype = 'u'
       AND (
           -- attname is `name`, so cast before comparing to a text[].
           SELECT array_agg(a.attname::text ORDER BY a.attname::text)
             FROM unnest(c.conkey) AS k(attnum)
             JOIN pg_attribute a
               ON a.attrelid = c.conrelid AND a.attnum = k.attnum
       ) = ARRAY['connection_id', 'crew_txn_id']
     LIMIT 1;

    IF conname_found IS NOT NULL THEN
        EXECUTE format('ALTER TABLE transactions DROP CONSTRAINT %I', conname_found);
    END IF;
END $$;

ALTER TABLE transactions
    ADD CONSTRAINT transactions_family_crew_txn_key UNIQUE (family_id, crew_txn_id);

-- The holder-side sync paths look transactions up by Crew's ID within a
-- household now, not within a connection.
CREATE INDEX IF NOT EXISTS transactions_family_crew_txn_idx
    ON transactions (family_id, crew_txn_id);
