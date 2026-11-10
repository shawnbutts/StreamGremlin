-- Time in chat (docs/adr/0034-time-in-chat.md).

-- When each account last chatted, from real chat (never test chat or the
-- streamer's own), so a tick knows who chatted recently.
CREATE TABLE earning_active (
	platform     TEXT NOT NULL,
	user_id      TEXT NOT NULL,
	login        TEXT NOT NULL,
	display_name TEXT NOT NULL,
	last_chat_at TEXT NOT NULL,
	PRIMARY KEY (platform, user_id)
) STRICT;
CREATE INDEX earning_active_at ON earning_active (last_chat_at);

-- Each time-in-chat tick: when, how many were paid, and why not, if not.
-- Kept 30 days.
CREATE TABLE earning_ticks (
	at     TEXT PRIMARY KEY,
	active INTEGER NOT NULL,
	quiet  INTEGER NOT NULL,
	note   TEXT NOT NULL DEFAULT ''
) STRICT;
