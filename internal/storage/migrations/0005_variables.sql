-- Saved variables (docs/adr/0009-variables.md): values workflows keep
-- between runs and restarts, in one of three scopes.
--   global:   owner is ''.
--   viewer:   owner is '<platform>:<user id>', never a login, so a rename
--             keeps the values; owner_label is the last name seen.
--   workflow: owner is the workflow's id; only that workflow sees them.

CREATE TABLE variables (
	scope       TEXT NOT NULL CHECK (scope IN ('global', 'viewer', 'workflow')),
	owner       TEXT NOT NULL,
	name        TEXT NOT NULL,
	value       TEXT NOT NULL CHECK (json_valid(value)),
	owner_label TEXT NOT NULL DEFAULT '',
	updated_at  TEXT NOT NULL,
	updated_by  TEXT NOT NULL, -- 'workflow:<id>' or 'you' (the app or CLI)
	PRIMARY KEY (scope, owner, name),
	CHECK ((scope = 'global') = (owner = ''))
) STRICT;
