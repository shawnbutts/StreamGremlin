-- Clock and timer overlays (D-092): a fifth kind of overlay. SQLite
-- can't change a CHECK, so the table is rebuilt with the same rows.
-- Nothing references it. A countdown's or stopwatch's state is a
-- settings document, overlays.timer.<name>.
CREATE TABLE overlays_new (
	name       TEXT PRIMARY KEY,
	kind       TEXT NOT NULL CHECK (kind IN ('alert', 'giveaway', 'wheel', 'song', 'timer')),
	look       TEXT NOT NULL,
	token_hash TEXT NOT NULL UNIQUE,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
) STRICT;
INSERT INTO overlays_new (name, kind, look, token_hash, created_at, updated_at)
	SELECT name, kind, look, token_hash, created_at, updated_at FROM overlays;
DROP TABLE overlays;
ALTER TABLE overlays_new RENAME TO overlays;
