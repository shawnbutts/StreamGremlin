-- Workflows (docs/adr/0008-workflows.md): immutable versions, one job per
-- workflow and event, an append-only trace per job, and the dispatcher's
-- place in the inbox.

CREATE TABLE workflows (
	id              TEXT PRIMARY KEY,
	name            TEXT NOT NULL,
	enabled         INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
	current_version INTEGER NOT NULL,
	created_at      TEXT NOT NULL,
	updated_at      TEXT NOT NULL
) STRICT;

CREATE TABLE workflow_versions (
	workflow_id TEXT NOT NULL REFERENCES workflows (id),
	version     INTEGER NOT NULL,
	definition  TEXT NOT NULL CHECK (json_valid(definition)),
	checksum    TEXT NOT NULL,
	created_at  TEXT NOT NULL,
	PRIMARY KEY (workflow_id, version)
) STRICT;

CREATE TRIGGER workflow_versions_no_update BEFORE UPDATE ON workflow_versions
	BEGIN SELECT RAISE(ABORT, 'workflow versions never change'); END;
CREATE TRIGGER workflow_versions_no_delete BEFORE DELETE ON workflow_versions
	BEGIN SELECT RAISE(ABORT, 'workflow versions are kept'); END;

-- One job per workflow and event: redispatching an event never runs twice.
CREATE TABLE jobs (
	id          TEXT PRIMARY KEY,
	workflow_id TEXT NOT NULL REFERENCES workflows (id),
	version     INTEGER NOT NULL,
	event_id    TEXT NOT NULL,
	event_seq   INTEGER NOT NULL,
	status      TEXT NOT NULL CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled',
	                                            'interrupted', 'rejected', 'dropped', 'superseded')),
	reason      TEXT NOT NULL DEFAULT '',
	dry_run     INTEGER NOT NULL CHECK (dry_run IN (0, 1)),
	error       TEXT NOT NULL DEFAULT '',
	created_at  TEXT NOT NULL,
	started_at  TEXT,
	finished_at TEXT,
	UNIQUE (workflow_id, event_id)
) STRICT;

CREATE INDEX jobs_workflow ON jobs (workflow_id, created_at);
CREATE INDEX jobs_status ON jobs (status);

CREATE TRIGGER jobs_final BEFORE UPDATE ON jobs
	WHEN OLD.status NOT IN ('pending', 'running')
	BEGIN SELECT RAISE(ABORT, 'a finished job never changes'); END;

-- The trace: one row per step that ran or was skipped.
CREATE TABLE job_steps (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	job_id      TEXT NOT NULL REFERENCES jobs (id),
	path        TEXT NOT NULL,
	step_id     TEXT NOT NULL DEFAULT '',
	kind        TEXT NOT NULL,
	action      TEXT NOT NULL DEFAULT '',
	status      TEXT NOT NULL CHECK (status IN ('ok', 'failed', 'skipped')),
	reason      TEXT NOT NULL DEFAULT '',
	error       TEXT NOT NULL DEFAULT '',
	input       TEXT CHECK (input IS NULL OR json_valid(input)),
	output      TEXT CHECK (output IS NULL OR json_valid(output)),
	started_at  TEXT NOT NULL,
	finished_at TEXT NOT NULL
) STRICT;

CREATE INDEX job_steps_job ON job_steps (job_id, id);

CREATE TRIGGER job_steps_no_update BEFORE UPDATE ON job_steps
	BEGIN SELECT RAISE(ABORT, 'traces are append-only'); END;

-- The dispatcher's place in the inbox.
CREATE TABLE workflow_dispatch (
	id     INTEGER PRIMARY KEY CHECK (id = 1),
	cursor INTEGER NOT NULL
) STRICT;
