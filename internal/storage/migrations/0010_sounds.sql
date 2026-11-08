-- Sounds (docs/adr/0016-sounds.md): the sound library, kept in the
-- database so backups and restores carry it. The desktop app plays them.
CREATE TABLE sounds (
	name       TEXT PRIMARY KEY,
	format     TEXT NOT NULL CHECK (format IN ('mp3', 'wav', 'ogg', 'm4a')),
	size       INTEGER NOT NULL,
	sha256     TEXT NOT NULL,
	volume     INTEGER NOT NULL CHECK (volume BETWEEN 0 AND 100),
	data       BLOB NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
) STRICT;
