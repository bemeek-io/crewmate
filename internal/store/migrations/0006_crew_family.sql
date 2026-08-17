-- Crew has its own family concept (Account.family). Recording it lets a second
-- member who signs in land in the same crewmate family automatically, sharing
-- categories without passing an invite code around.
ALTER TABLE families ADD COLUMN crew_family_id TEXT;
CREATE UNIQUE INDEX families_crew_family_idx ON families(crew_family_id)
    WHERE crew_family_id IS NOT NULL;

ALTER TABLE users ADD COLUMN crew_family_id TEXT NOT NULL DEFAULT '';
CREATE INDEX users_crew_family_idx ON users(crew_family_id) WHERE crew_family_id <> '';
