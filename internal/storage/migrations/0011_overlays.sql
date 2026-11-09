-- Overlays (docs/adr/0017-alert-overlays.md): each is one OBS browser
-- source. Only the SHA-256 of its link's token is kept here, so a backup
-- never carries a working link; the token itself is in the secret store.
CREATE TABLE overlays (
	name       TEXT PRIMARY KEY,
	kind       TEXT NOT NULL CHECK (kind IN ('alert')),
	look       TEXT NOT NULL,
	token_hash TEXT NOT NULL UNIQUE,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
) STRICT;
