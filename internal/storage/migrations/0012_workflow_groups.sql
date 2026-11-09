-- Workflow groups (docs/adr/0020-workflow-groups.md). A workflow's group is
-- in its definition; grp copies the current version's, for lookups. A
-- group's own switch is here: a workflow runs only when it and its group
-- are on, so turning a group off and on restores each workflow's switch.
ALTER TABLE workflows ADD COLUMN grp TEXT NOT NULL DEFAULT '';

CREATE TABLE workflow_groups (
	name       TEXT PRIMARY KEY COLLATE NOCASE,
	enabled    INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
	updated_at TEXT NOT NULL
) STRICT;
