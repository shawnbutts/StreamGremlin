-- The event inbox (docs/adr/0004-events.md). Applied files are never edited;
-- 0001's header predates that rule (D-010). Before the first public release
-- every file is squashed into one baseline.

-- Every event, once, in the order it arrived. seq is the cursor every
-- reader resumes from.
CREATE TABLE events (
	seq              INTEGER PRIMARY KEY AUTOINCREMENT,
	event_id         TEXT NOT NULL UNIQUE,
	type             TEXT NOT NULL,
	schema_version   INTEGER NOT NULL CHECK (schema_version >= 1),
	source           TEXT NOT NULL,
	source_event_id  TEXT,
	occurred_at      TEXT NOT NULL,
	received_at      TEXT NOT NULL,
	channel_id       TEXT NOT NULL DEFAULT '',
	actor            TEXT CHECK (actor IS NULL OR json_valid(actor)),
	payload          TEXT NOT NULL CHECK (json_valid(payload)),
	correlation_id   TEXT NOT NULL,
	causation_id     TEXT NOT NULL DEFAULT '',
	delivery_quality TEXT NOT NULL CHECK (delivery_quality IN ('confirmed', 'estimated', 'synthetic')),
	content_hash     TEXT NOT NULL
) STRICT;

-- A platform's redelivery carries the same (source, source_event_id).
CREATE UNIQUE INDEX events_source_event ON events (source, source_event_id)
	WHERE source_event_id IS NOT NULL;

CREATE INDEX events_type ON events (type, seq);

-- Events are facts: never changed once stored.
CREATE TRIGGER events_no_update BEFORE UPDATE ON events
	BEGIN SELECT RAISE(ABORT, 'events are append-only'); END;
