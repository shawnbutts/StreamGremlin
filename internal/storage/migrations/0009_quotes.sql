-- Quotes (docs/adr/0014-quotes.md): numbered sayings from the stream.
-- Numbers count up and are never reused: deleting #3 leaves a gap, as in
-- Streamer.bot. request_key makes a retried add return the first one.
CREATE TABLE quotes (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	text        TEXT NOT NULL,
	added_by    TEXT NOT NULL DEFAULT '', -- a name to show
	added_by_id TEXT NOT NULL DEFAULT '', -- '<platform>:<user id>', or '' when you added it
	platform    TEXT NOT NULL DEFAULT '',
	category    TEXT NOT NULL DEFAULT '', -- what you were streaming, if known
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL,
	request_key TEXT UNIQUE
) STRICT;

CREATE INDEX quotes_created ON quotes (created_at);
