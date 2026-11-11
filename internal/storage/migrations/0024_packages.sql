-- Packages: installed overlay themes, and later Lua plugins
-- (docs/adr/0036-packages-plugins-and-themes.md). Each version's files are
-- kept here, with their SHA-256, so backups carry them and what runs is
-- what was checked. An id and version are one fixed thing: a version's
-- files never change. The audit outlives the package.
CREATE TABLE packages (
	id              TEXT PRIMARY KEY,
	kind            TEXT NOT NULL CHECK (kind IN ('theme', 'lua')),
	publisher       TEXT NOT NULL,
	current_version TEXT NOT NULL,
	installed_at    TEXT NOT NULL,
	updated_at      TEXT NOT NULL
) STRICT;

CREATE TABLE package_versions (
	package_id   TEXT NOT NULL REFERENCES packages (id) ON DELETE CASCADE,
	version      TEXT NOT NULL,
	digest       TEXT NOT NULL,
	manifest     TEXT NOT NULL CHECK (json_valid(manifest)),
	size         INTEGER NOT NULL,
	installed_at TEXT NOT NULL,
	PRIMARY KEY (package_id, version)
) STRICT;

CREATE TABLE package_files (
	package_id TEXT NOT NULL,
	version    TEXT NOT NULL,
	path       TEXT NOT NULL,
	sha256     TEXT NOT NULL,
	data       BLOB NOT NULL,
	PRIMARY KEY (package_id, version, path),
	FOREIGN KEY (package_id, version) REFERENCES package_versions (package_id, version) ON DELETE CASCADE
) STRICT;

CREATE TRIGGER package_versions_fixed BEFORE UPDATE ON package_versions
	BEGIN SELECT RAISE(ABORT, 'a package version never changes'); END;
CREATE TRIGGER package_files_fixed BEFORE UPDATE ON package_files
	BEGIN SELECT RAISE(ABORT, 'a package file never changes'); END;

-- Append-only: installs, removals and refusals, kept after a package goes.
CREATE TABLE package_audit (
	seq        INTEGER PRIMARY KEY AUTOINCREMENT,
	at         TEXT NOT NULL,
	package_id TEXT NOT NULL,
	version    TEXT NOT NULL DEFAULT '',
	action     TEXT NOT NULL CHECK (action IN ('install', 'update', 'rollback', 'remove', 'refuse')),
	detail     TEXT NOT NULL DEFAULT ''
) STRICT;
CREATE INDEX package_audit_package ON package_audit (package_id, seq);
CREATE TRIGGER package_audit_no_update BEFORE UPDATE ON package_audit
	BEGIN SELECT RAISE(ABORT, 'the package audit is append-only'); END;
CREATE TRIGGER package_audit_no_delete BEFORE DELETE ON package_audit
	BEGIN SELECT RAISE(ABORT, 'the package audit is append-only'); END;
