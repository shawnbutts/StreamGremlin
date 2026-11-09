-- Ads (docs/adr/0024-ads.md): what StreamGremlin asked Twitch for, and
-- what Twitch said happened, kept apart and never deleted. An answered
-- request never changes; only an unknown one may later be found accepted.
CREATE TABLE ad_requests (
	id              TEXT PRIMARY KEY,
	op_key          TEXT NOT NULL UNIQUE,
	action          TEXT NOT NULL CHECK (action IN ('commercial', 'snooze')),
	length          INTEGER NOT NULL DEFAULT 0,
	requested_by    TEXT NOT NULL,
	status          TEXT NOT NULL CHECK (status IN ('pending', 'accepted', 'refused', 'unknown')),
	reason          TEXT NOT NULL DEFAULT '',
	message         TEXT NOT NULL DEFAULT '',
	accepted_length INTEGER,
	retry_until     TEXT,
	next_ad_at      TEXT,
	snooze_count    INTEGER,
	confirmed_by    TEXT NOT NULL DEFAULT '' CHECK (confirmed_by IN ('', 'event', 'schedule')),
	created_at      TEXT NOT NULL,
	settled_at      TEXT
) STRICT;

CREATE TRIGGER ad_requests_answered_stay BEFORE UPDATE OF status ON ad_requests
	WHEN OLD.status IN ('accepted', 'refused')
	BEGIN SELECT RAISE(ABORT, 'an answered ad request never changes'); END;
CREATE TRIGGER ad_requests_kept BEFORE DELETE ON ad_requests
	BEGIN SELECT RAISE(ABORT, 'ad requests are kept'); END;

CREATE TABLE ad_breaks (
	key              TEXT PRIMARY KEY,
	started_at       TEXT NOT NULL,
	duration         INTEGER NOT NULL,
	is_automatic     INTEGER NOT NULL CHECK (is_automatic IN (0, 1)),
	request_id       TEXT REFERENCES ad_requests (id),
	end_estimated_at TEXT NOT NULL,
	end_announced    INTEGER NOT NULL DEFAULT 0 CHECK (end_announced IN (0, 1)),
	created_at       TEXT NOT NULL
) STRICT;

CREATE TRIGGER ad_breaks_kept BEFORE DELETE ON ad_breaks
	BEGIN SELECT RAISE(ABORT, 'ad breaks are kept'); END;
