-- Generalize the Crew write queue. It was note-only; moving a debit card to a
-- different pocket needs the same machinery (only the lease holder owns that
-- connection's Crew client), so the columns become kind/target/value.
ALTER TABLE crew_write_jobs RENAME COLUMN crew_txn_id TO target_id;
ALTER TABLE crew_write_jobs RENAME COLUMN note TO value;
ALTER TABLE crew_write_jobs ADD COLUMN kind TEXT NOT NULL DEFAULT 'note'
    CHECK (kind IN ('note', 'card_subaccount'));

-- Drop the old one-pending-write-per-transaction constraint by looking it up
-- rather than assuming Postgres's generated name.
DO $$
DECLARE
    conname_found TEXT;
BEGIN
    SELECT c.conname INTO conname_found
      FROM pg_constraint c
     WHERE c.conrelid = 'crew_write_jobs'::regclass
       AND c.contype = 'u'
     LIMIT 1;
    IF conname_found IS NOT NULL THEN
        EXECUTE format('ALTER TABLE crew_write_jobs DROP CONSTRAINT %I', conname_found);
    END IF;
END $$;

-- The uniqueness that matters now is one pending write per (kind, target).
ALTER TABLE crew_write_jobs ADD CONSTRAINT crew_write_jobs_target_key
    UNIQUE (connection_id, kind, target_id);
