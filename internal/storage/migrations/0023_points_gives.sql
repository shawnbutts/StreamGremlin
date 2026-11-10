-- Giving points to everyone in a group at once (docs/adr/0035-giving-to-everyone.md).

-- One give: who to (the audience, resolved once, at the time), how many
-- each, and why. Each viewer's payment is a ledger credit with the operation
-- id give:<id>:<viewer>, so a retry pays nobody twice.
CREATE TABLE points_gives (
	id         TEXT PRIMARY KEY,               -- g_<hex>
	request_id TEXT NOT NULL UNIQUE,
	audience   TEXT NOT NULL CHECK (audience IN ('chat', 'recent', 'subs')),
	minutes    INTEGER NOT NULL DEFAULT 0,     -- for recent
	amount     INTEGER NOT NULL CHECK (amount > 0),
	memo       TEXT NOT NULL,
	by         TEXT NOT NULL,
	viewers    INTEGER NOT NULL,
	created_at TEXT NOT NULL,
	paid_at    TEXT,                           -- every member paid
	undone_at  TEXT
) STRICT;
CREATE INDEX points_gives_created ON points_gives (created_at);

-- Who was in the group, fixed when the give was made.
CREATE TABLE points_give_members (
	give   TEXT NOT NULL REFERENCES points_gives (id),
	viewer TEXT NOT NULL,
	PRIMARY KEY (give, viewer)
) STRICT;
