-- Some categories are meaningful only when a person chooses them.
--
-- A catch-all like "Misc" is the obvious case: auto-assigning it would look
-- like the transaction had been dealt with while actually burying it, and the
-- whole point of that category is that someone decided it belongs nowhere
-- else. Excluded categories stay fully usable by hand and by rules; they are
-- only withheld from the model.
ALTER TABLE categories
    ADD COLUMN exclude_from_llm BOOLEAN NOT NULL DEFAULT false;
