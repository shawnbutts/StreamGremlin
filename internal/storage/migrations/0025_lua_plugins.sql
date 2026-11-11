-- Lua plugins (docs/adr/0038-lua-plugins.md): on or off, why one failed,
-- its settings, the permissions the streamer approved, and its activity
-- (every call it made to StreamGremlin, the last 1,000 kept).
ALTER TABLE packages ADD COLUMN enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1));
ALTER TABLE packages ADD COLUMN state_error TEXT NOT NULL DEFAULT '';

CREATE TABLE package_settings (
	package_id TEXT PRIMARY KEY REFERENCES packages (id) ON DELETE CASCADE,
	settings   TEXT NOT NULL CHECK (json_valid(settings))
) STRICT;

CREATE TABLE package_permissions (
	package_id TEXT NOT NULL REFERENCES packages (id) ON DELETE CASCADE,
	permission TEXT NOT NULL,
	PRIMARY KEY (package_id, permission)
) STRICT;

CREATE TABLE package_activity (
	seq        INTEGER PRIMARY KEY AUTOINCREMENT,
	at         TEXT NOT NULL,
	package_id TEXT NOT NULL REFERENCES packages (id) ON DELETE CASCADE,
	version    TEXT NOT NULL,
	call       TEXT NOT NULL,
	outcome    TEXT NOT NULL CHECK (outcome IN ('ok', 'refused', 'failed')),
	detail     TEXT NOT NULL DEFAULT ''
) STRICT;
CREATE INDEX package_activity_package ON package_activity (package_id, seq);

-- The audit gains on, off, settings and failed. Its CHECK can't be
-- altered, so it's rebuilt with its rows.
DROP TRIGGER package_audit_no_update;
DROP TRIGGER package_audit_no_delete;
CREATE TABLE package_audit_new (
	seq        INTEGER PRIMARY KEY AUTOINCREMENT,
	at         TEXT NOT NULL,
	package_id TEXT NOT NULL,
	version    TEXT NOT NULL DEFAULT '',
	action     TEXT NOT NULL CHECK (action IN ('install', 'update', 'rollback', 'remove', 'refuse', 'on', 'off', 'settings', 'failed')),
	detail     TEXT NOT NULL DEFAULT ''
) STRICT;
INSERT INTO package_audit_new (seq, at, package_id, version, action, detail)
	SELECT seq, at, package_id, version, action, detail FROM package_audit;
DROP TABLE package_audit;
ALTER TABLE package_audit_new RENAME TO package_audit;
CREATE INDEX package_audit_package ON package_audit (package_id, seq);
CREATE TRIGGER package_audit_no_update BEFORE UPDATE ON package_audit
	BEGIN SELECT RAISE(ABORT, 'the package audit is append-only'); END;
CREATE TRIGGER package_audit_no_delete BEFORE DELETE ON package_audit
	BEGIN SELECT RAISE(ABORT, 'the package audit is append-only'); END;
