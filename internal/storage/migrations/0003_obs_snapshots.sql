-- Scene snapshots (docs/adr/0007-obs.md): the scene to switch back to, and
-- how each snapshot ended. Settled rows never change and are never deleted.

CREATE TABLE obs_snapshots (
	id             TEXT PRIMARY KEY,
	key            TEXT NOT NULL,
	previous_scene TEXT NOT NULL,
	target_scene   TEXT NOT NULL,
	switched       INTEGER NOT NULL CHECK (switched IN (0, 1)),
	status         TEXT NOT NULL CHECK (status IN ('open', 'restored', 'skipped', 'failed', 'released')),
	reason         TEXT NOT NULL DEFAULT '',
	created_at     TEXT NOT NULL,
	settled_at     TEXT
) STRICT;

-- One open snapshot per key.
CREATE UNIQUE INDEX obs_snapshots_open_key ON obs_snapshots (key) WHERE status = 'open';

CREATE TRIGGER obs_snapshots_settled_final BEFORE UPDATE ON obs_snapshots
	WHEN OLD.status <> 'open'
	BEGIN SELECT RAISE(ABORT, 'a settled snapshot never changes'); END;

CREATE TRIGGER obs_snapshots_no_delete BEFORE DELETE ON obs_snapshots
	BEGIN SELECT RAISE(ABORT, 'snapshots are kept'); END;
