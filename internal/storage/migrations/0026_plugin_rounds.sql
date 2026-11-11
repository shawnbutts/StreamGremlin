-- Plugins' rounds (docs/adr/0039-points-for-plugins.md): stakes held for a
-- game, paid out or refunded. The ledger holds the points; these say what
-- each round was, its stated rules, its rolls and how it ended. They
-- outlive the plugin, as its audit does.
CREATE TABLE plugin_rounds (
	id         TEXT PRIMARY KEY,
	package_id TEXT NOT NULL,
	version    TEXT NOT NULL,
	title      TEXT NOT NULL,
	rules      TEXT NOT NULL,
	currency   TEXT NOT NULL,
	state      TEXT NOT NULL CHECK (state IN ('open', 'settling', 'settled', 'refunded')),
	reason     TEXT NOT NULL DEFAULT '',
	opened_at  TEXT NOT NULL,
	closes_by  TEXT NOT NULL,
	closed_at  TEXT,
	payouts    TEXT CHECK (payouts IS NULL OR json_valid(payouts))
) STRICT;
CREATE INDEX plugin_rounds_package ON plugin_rounds (package_id, opened_at);
CREATE INDEX plugin_rounds_state ON plugin_rounds (state);

CREATE TABLE plugin_round_stakes (
	round_id  TEXT NOT NULL REFERENCES plugin_rounds (id),
	viewer_id TEXT NOT NULL,
	name      TEXT NOT NULL,
	amount    INTEGER NOT NULL CHECK (amount > 0),
	staked_at TEXT NOT NULL,
	PRIMARY KEY (round_id, viewer_id)
) STRICT;

CREATE TABLE plugin_round_rolls (
	round_id TEXT NOT NULL REFERENCES plugin_rounds (id),
	seq      INTEGER NOT NULL,
	label    TEXT NOT NULL,
	sides    INTEGER NOT NULL,
	value    INTEGER NOT NULL,
	at       TEXT NOT NULL,
	PRIMARY KEY (round_id, seq)
) STRICT;
CREATE TRIGGER plugin_round_rolls_no_update BEFORE UPDATE ON plugin_round_rolls
	BEGIN SELECT RAISE(ABORT, 'rolls are append-only'); END;
CREATE TRIGGER plugin_round_rolls_no_delete BEFORE DELETE ON plugin_round_rolls
	BEGIN SELECT RAISE(ABORT, 'rolls are append-only'); END;
