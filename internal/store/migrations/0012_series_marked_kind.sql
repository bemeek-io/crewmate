-- "This is a subscription" and "file it under Subscription" are different
-- statements, and conflating them made both unusable.
--
-- The only way to tell crewmate something was a subscription was to label it,
-- and the label also forces the Subscription category. Anyone who files
-- subscriptions somewhere more useful — Tech, say — had to remove the label,
-- which silently dropped the charge from the subscription total.
--
-- The classifier is also stricter than people are: it wants a near-identical
-- amount, so usage-billed services (Anthropic, DigitalOcean, Cloudflare) come
-- out as merely 'recurring' even though they are plainly subscriptions.
--
-- marked_kind is the member overriding that judgement, independently of any
-- category. NULL means "whatever the classifier decided".
ALTER TABLE recurring_series
    ADD COLUMN marked_kind TEXT
    CHECK (marked_kind IN ('subscription', 'recurring'));
