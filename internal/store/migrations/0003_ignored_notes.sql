-- A note that doesn't name a category is either (a) a label the user wants
-- promoted into crewmate's category list, or (b) a hand-written annotation
-- they never want treated as a category. Crewmate prompts for the first case;
-- this table remembers the second so it stops asking.
CREATE TABLE ignored_notes (
    family_id  UUID NOT NULL REFERENCES families(id) ON DELETE CASCADE,
    note_key   TEXT NOT NULL,   -- lower(note)
    note       TEXT NOT NULL,   -- original casing, for display
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (family_id, note_key)
);
