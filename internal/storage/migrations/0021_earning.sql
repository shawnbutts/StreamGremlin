-- Earning rules (docs/adr/0033-earning-rules.md).

-- Where earning has read the inbox to. Made at the head on first start, so
-- turning earning on never pays for what happened before.
CREATE TABLE earning_cursor (
	id  INTEGER PRIMARY KEY CHECK (id = 1),
	seq INTEGER NOT NULL
) STRICT;

-- When each viewer last earned for chatting, so the wait between chat
-- awards survives a restart.
CREATE TABLE earning_chat (
	viewer  TEXT PRIMARY KEY,
	last_at TEXT NOT NULL
) STRICT;
