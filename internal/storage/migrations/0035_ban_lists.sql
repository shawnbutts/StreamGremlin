-- Ban lists (M8 slice 2, D-106, docs/adr/0049-ban-lists.md): a streamer's
-- own lists of accounts to ban on Twitch, read from a file, with each
-- name's state saved as it goes so a run picks up where it left off.

CREATE TABLE ban_lists (
	id          TEXT PRIMARY KEY,                 -- bl_<hex>
	name        TEXT NOT NULL UNIQUE COLLATE NOCASE,
	reason      TEXT NOT NULL,                    -- sent with each ban
	-- reading, checking, ready, banning, undoing, copying, comparing, done
	phase       TEXT NOT NULL,
	paused      INTEGER NOT NULL DEFAULT 0,       -- paused by the streamer
	pause_live  INTEGER NOT NULL DEFAULT 1,       -- wait while you're live on Twitch
	keep_points INTEGER NOT NULL DEFAULT 1,       -- leave viewers with points alone
	file_path   TEXT NOT NULL DEFAULT '',         -- the file being read, until it's read
	file_name   TEXT NOT NULL DEFAULT '',
	generation  INTEGER NOT NULL DEFAULT 0,       -- the import names were last seen in
	skipped     INTEGER NOT NULL DEFAULT 0,       -- lines that weren't a Twitch name
	cursor      TEXT NOT NULL DEFAULT '',         -- copying Twitch's list: where it's up to
	problem     TEXT NOT NULL DEFAULT '',         -- why it stopped, in words
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL
) STRICT;

CREATE TABLE ban_list_entries (
	list_id    TEXT NOT NULL REFERENCES ban_lists (id) ON DELETE CASCADE,
	name       TEXT NOT NULL,                     -- a login, or id:<user id>
	user_id    TEXT NOT NULL DEFAULT '',
	login      TEXT NOT NULL DEFAULT '',
	-- new, not_found, kept, already, waiting, sent, banned, refused,
	-- by_hand, removed, unban, unban_sent, unbanned
	state      TEXT NOT NULL,
	detail     TEXT NOT NULL DEFAULT '',
	generation INTEGER NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY (list_id, name)
) STRICT, WITHOUT ROWID;
CREATE INDEX ban_list_entries_state ON ban_list_entries (list_id, state);
