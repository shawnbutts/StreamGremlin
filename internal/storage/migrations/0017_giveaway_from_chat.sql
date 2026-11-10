-- Drawing from recent chat (docs/adr/0028-giveaway-from-chat.md): a
-- giveaway whose entrants are everyone who chatted in the last N minutes,
-- entered, closed and drawn at once. Fixed once open.
ALTER TABLE giveaways ADD COLUMN from_chat_minutes INTEGER NOT NULL DEFAULT 0 CHECK (from_chat_minutes BETWEEN 0 AND 60);

CREATE TRIGGER giveaways_from_chat_fixed BEFORE UPDATE OF from_chat_minutes ON giveaways
	BEGIN SELECT RAISE(ABORT, 'a giveaway''s chat window never changes'); END;
