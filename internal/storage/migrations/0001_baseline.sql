-- The baseline schema. Before the first public release this file may be
-- rewritten as slices add tables; after it, migrations are frozen and new
-- changes get new numbered files (docs/adr/0001-storage.md).

-- Small typed settings documents, one row per group (for example "ui").
CREATE TABLE settings (
	key        TEXT PRIMARY KEY,
	value      TEXT NOT NULL CHECK (json_valid(value)),
	updated_at TEXT NOT NULL
) STRICT;

-- Secrets (docs/adr/0003-secrets.md). Values never live in this database:
-- only which store holds them, and an index of keys and times.

-- Which store this data folder uses. Chosen on first start, then fixed.
CREATE TABLE secret_store (
	id              INTEGER PRIMARY KEY CHECK (id = 1),
	kind            TEXT NOT NULL CHECK (kind IN ('keyring', 'file')),
	installation_id TEXT NOT NULL CHECK (length(installation_id) = 32),
	chosen_at       TEXT NOT NULL
) STRICT;

-- One row per saved secret: its key and times, never its value.
CREATE TABLE secrets (
	key        TEXT PRIMARY KEY,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
) STRICT;
