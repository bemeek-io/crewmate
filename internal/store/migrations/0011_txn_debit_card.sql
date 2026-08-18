-- Which card paid, so a notification can go to the person who spent.
--
-- A card swipe is one member's business: the other doesn't need a push every
-- time their partner buys lunch. Bank transactions and the per-merchant
-- virtual cards are household-level and still go to everyone.
ALTER TABLE transactions ADD COLUMN debit_card_id TEXT NOT NULL DEFAULT '';

-- Backfill from the raw payload, which has carried debitCard all along.
UPDATE transactions
   SET debit_card_id = raw #>> '{debitCard,id}'
 WHERE debit_card_id = ''
   AND raw #>> '{debitCard,id}' IS NOT NULL;

CREATE INDEX IF NOT EXISTS transactions_debit_card_idx
    ON transactions (debit_card_id) WHERE debit_card_id <> '';
