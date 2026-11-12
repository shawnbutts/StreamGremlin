-- Paired clients (M7 slice 4, docs/adr/0042-paired-clients.md): a
-- Stream Deck plugin that has its own API token, which can only do what
-- its kind allows. The token is never stored, only its SHA-256.
CREATE TABLE api_clients (
	id           TEXT PRIMARY KEY,            -- cl_ and 16 hex digits
	kind         TEXT NOT NULL CHECK (kind IN ('stream_deck')),
	name         TEXT NOT NULL,               -- what the plugin calls itself
	token_hash   TEXT NOT NULL UNIQUE,        -- hex SHA-256 of the token
	created_at   TEXT NOT NULL,
	last_used_at TEXT                         -- written at most once a minute
) STRICT;
