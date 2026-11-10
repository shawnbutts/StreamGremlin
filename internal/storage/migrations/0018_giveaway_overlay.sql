-- The giveaway overlay (docs/adr/0029-giveaway-overlay.md): a second kind
-- of overlay, and chat that waits for a draw's roll to land.

-- SQLite can't change a CHECK, so the table is rebuilt with the same rows.
-- Nothing references it.
CREATE TABLE overlays_new (
	name       TEXT PRIMARY KEY,
	kind       TEXT NOT NULL CHECK (kind IN ('alert', 'giveaway')),
	look       TEXT NOT NULL,
	token_hash TEXT NOT NULL UNIQUE,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
) STRICT;
INSERT INTO overlays_new (name, kind, look, token_hash, created_at, updated_at)
	SELECT name, kind, look, token_hash, created_at, updated_at FROM overlays;
DROP TABLE overlays;
ALTER TABLE overlays_new RENAME TO overlays;

-- A winner's message waits until the roll on a giveaway overlay lands.
ALTER TABLE giveaway_messages ADD COLUMN send_after TEXT;
