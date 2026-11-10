-- Extra chances (docs/adr/0027-giveaway-chances.md): subscribers can hold
-- more than one ticket. Set when a giveaway opens, and for each entry when
-- it enters; neither changes after. A weighted giveaway's fingerprint
-- includes each entry's chances.
ALTER TABLE giveaways ADD COLUMN subscriber_chances INTEGER NOT NULL DEFAULT 1 CHECK (subscriber_chances BETWEEN 1 AND 10);

CREATE TRIGGER giveaways_chances_fixed BEFORE UPDATE OF subscriber_chances ON giveaways
	BEGIN SELECT RAISE(ABORT, 'a giveaway''s chances never change once it opens'); END;

ALTER TABLE giveaway_entries ADD COLUMN chances INTEGER NOT NULL DEFAULT 1 CHECK (chances BETWEEN 1 AND 10);

CREATE TRIGGER giveaway_entries_chances_fixed BEFORE UPDATE OF chances ON giveaway_entries
	BEGIN SELECT RAISE(ABORT, 'an entry never changes'); END;
