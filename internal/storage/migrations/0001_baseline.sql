-- The baseline schema. Before the first public release this file may be
-- rewritten as slices add tables; after it, migrations are frozen and new
-- changes get new numbered files (docs/adr/0001-storage.md).

-- Small typed settings documents, one row per group (for example "ui").
CREATE TABLE settings (
	key        TEXT PRIMARY KEY,
	value      TEXT NOT NULL CHECK (json_valid(value)),
	updated_at TEXT NOT NULL
) STRICT;
