-- Timers (docs/adr/0013-timers.md): Streamer.bot's Timed Actions. Each
-- fire records a timers.timer.fired event; workflows match its timer_id.
-- The schedule itself lives in memory, so a restart never fires a burst.
CREATE TABLE timers (
	id            TEXT PRIMARY KEY,
	name          TEXT NOT NULL,
	enabled       INTEGER NOT NULL CHECK (enabled IN (0, 1)),
	every_seconds INTEGER NOT NULL DEFAULT 0, -- 0 when cron is set
	up_to_seconds INTEGER NOT NULL DEFAULT 0, -- 0: exactly every_seconds; else at random between
	cron          TEXT NOT NULL DEFAULT '',   -- five fields, the computer's local time
	chat_lines    INTEGER NOT NULL DEFAULT 0, -- chat messages to wait for since the last fire
	live_only     INTEGER NOT NULL DEFAULT 0 CHECK (live_only IN (0, 1)),
	run_once      INTEGER NOT NULL DEFAULT 0 CHECK (run_once IN (0, 1)),
	fire_count    INTEGER NOT NULL DEFAULT 0, -- Streamer.bot's %counter%
	last_fired_at TEXT,
	created_at    TEXT NOT NULL,
	updated_at    TEXT NOT NULL, -- changes on save and on/off, never on a fire
	CHECK ((cron = '') = (every_seconds > 0))
) STRICT;
