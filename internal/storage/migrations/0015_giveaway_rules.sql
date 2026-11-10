-- Giveaway entry rules (docs/adr/0026-giveaway-entry-rules.md): who may
-- enter, set when a giveaway opens and fixed after, and what each entry
-- was checked against. Rules decide who may enter, never anyone's chance.
ALTER TABLE giveaways ADD COLUMN rules TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(rules));
ALTER TABLE giveaways ADD COLUMN enter_by_chat INTEGER NOT NULL DEFAULT 0 CHECK (enter_by_chat IN (0, 1));

CREATE TRIGGER giveaways_rules_fixed BEFORE UPDATE OF rules, enter_by_chat ON giveaways
	BEGIN SELECT RAISE(ABORT, 'a giveaway''s rules never change once it opens'); END;

-- What the entry was checked against: follow and account dates, months
-- subscribed, messages seen. Not part of the entry list's fingerprint.
ALTER TABLE giveaway_entries ADD COLUMN qualified TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(qualified));

CREATE TRIGGER giveaway_entries_qualified_fixed BEFORE UPDATE OF qualified ON giveaway_entries
	BEGIN SELECT RAISE(ABORT, 'an entry never changes'); END;
