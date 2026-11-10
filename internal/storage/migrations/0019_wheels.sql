-- Wheels (docs/adr/0030-wheels.md): a wheel of weighted choices, spun by
-- the engine with crypto/rand. Each spin keeps the wheel as it was, the
-- number drawn and the result; nothing about a spin changes but its
-- settling, once, and spins are never deleted.
CREATE TABLE wheels (
	name         TEXT PRIMARY KEY,
	title        TEXT NOT NULL,
	choices      TEXT NOT NULL CHECK (json_valid(choices)),
	spin_seconds INTEGER NOT NULL CHECK (spin_seconds BETWEEN 2 AND 20),
	version      INTEGER NOT NULL DEFAULT 1,
	created_at   TEXT NOT NULL,
	updated_at   TEXT NOT NULL
) STRICT;

-- A spin outlives its wheel, so there's no foreign key: the snapshot is
-- what it was spun from.
CREATE TABLE wheel_spins (
	id            TEXT PRIMARY KEY,
	op_key        TEXT NOT NULL UNIQUE,
	wheel         TEXT NOT NULL,
	title         TEXT NOT NULL,
	wheel_version INTEGER NOT NULL,
	choices       TEXT NOT NULL CHECK (json_valid(choices)),
	total         INTEGER NOT NULL CHECK (total > 0),
	draw          INTEGER NOT NULL CHECK (draw >= 0 AND draw < total),
	winner        INTEGER NOT NULL CHECK (winner >= 0),
	label         TEXT NOT NULL,
	started_by    TEXT NOT NULL,
	spun_at       TEXT NOT NULL,
	seconds       INTEGER NOT NULL,
	settled_at    TEXT,
	display       TEXT NOT NULL DEFAULT '' CHECK (display IN ('', 'shown', 'no_audience', 'recovered'))
) STRICT;
CREATE INDEX wheel_spins_wheel ON wheel_spins (wheel, spun_at);

CREATE TRIGGER wheel_spins_fixed BEFORE UPDATE OF id, op_key, wheel, title, wheel_version, choices, total, draw, winner, label,
	started_by, spun_at, seconds ON wheel_spins
	BEGIN SELECT RAISE(ABORT, 'a spin never changes'); END;
CREATE TRIGGER wheel_spins_settle_once BEFORE UPDATE OF settled_at, display ON wheel_spins
	WHEN OLD.settled_at IS NOT NULL
	BEGIN SELECT RAISE(ABORT, 'a spin settles once'); END;
CREATE TRIGGER wheel_spins_kept BEFORE DELETE ON wheel_spins
	BEGIN SELECT RAISE(ABORT, 'spins are kept'); END;

-- A third kind of overlay. SQLite can't change a CHECK, so the table is
-- rebuilt with the same rows. Nothing references it.
CREATE TABLE overlays_new (
	name       TEXT PRIMARY KEY,
	kind       TEXT NOT NULL CHECK (kind IN ('alert', 'giveaway', 'wheel')),
	look       TEXT NOT NULL,
	token_hash TEXT NOT NULL UNIQUE,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
) STRICT;
INSERT INTO overlays_new (name, kind, look, token_hash, created_at, updated_at)
	SELECT name, kind, look, token_hash, created_at, updated_at FROM overlays;
DROP TABLE overlays;
ALTER TABLE overlays_new RENAME TO overlays;
