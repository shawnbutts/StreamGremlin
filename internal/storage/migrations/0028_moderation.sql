-- Moderation (M7 slice 3, ADR-0041): every moderating request sent to
-- Twitch from StreamGremlin, and how Twitch answered. A request id from
-- the app or CLI makes a repeat return the same row instead of sending
-- again. Rows are never deleted; one left 'pending' by a crash becomes
-- 'unknown' at the next start.
CREATE TABLE moderation (
	id          TEXT PRIMARY KEY,
	request_id  TEXT NOT NULL UNIQUE,
	action      TEXT NOT NULL,              -- delete, timeout, ban, unban, warn, shoutout, vip, unvip, mod, unmod, monitor, restrict, unsuspect, chat_modes
	platform    TEXT NOT NULL,
	user_id     TEXT NOT NULL DEFAULT '',   -- '' for chat modes
	user_login  TEXT NOT NULL DEFAULT '',
	message_id  TEXT NOT NULL DEFAULT '',
	seconds     INTEGER NOT NULL DEFAULT 0, -- a timeout's length
	reason      TEXT NOT NULL DEFAULT '',
	detail_json TEXT NOT NULL DEFAULT '',   -- chat modes: the change asked for
	from_where  TEXT NOT NULL,              -- live, cli
	state       TEXT NOT NULL,              -- pending, done, refused, unknown
	answer      TEXT NOT NULL DEFAULT '',   -- why, in words, when not done
	created_at  TEXT NOT NULL,
	answered_at TEXT
) STRICT;
CREATE INDEX moderation_user ON moderation (platform, user_id, created_at);
