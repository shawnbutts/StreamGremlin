-- Giveaways (docs/adr/0025-giveaways.md): equal entries, a fingerprinted
-- entry list, and draws that can be checked. Nothing here is deleted.
CREATE TABLE giveaways (
	id               TEXT PRIMARY KEY,
	title            TEXT NOT NULL,
	command          TEXT NOT NULL,
	claim_command    TEXT NOT NULL DEFAULT '',
	winners          INTEGER NOT NULL CHECK (winners BETWEEN 1 AND 20),
	claim_seconds    INTEGER NOT NULL DEFAULT 0 CHECK (claim_seconds = 0 OR claim_seconds BETWEEN 15 AND 600),
	subscribers_only INTEGER NOT NULL DEFAULT 0 CHECK (subscribers_only IN (0, 1)),
	practice         INTEGER NOT NULL DEFAULT 0 CHECK (practice IN (0, 1)),
	state            TEXT NOT NULL CHECK (state IN ('open', 'closed', 'ended', 'cancelled')),
	opened_by        TEXT NOT NULL,
	opened_at        TEXT NOT NULL,
	closes_at        TEXT,
	closed_at        TEXT,
	ended_at         TEXT,
	entry_count      INTEGER NOT NULL DEFAULT 0,
	fingerprint      TEXT NOT NULL DEFAULT ''
) STRICT;

-- Only one giveaway is unfinished at a time.
CREATE UNIQUE INDEX giveaways_one_unfinished ON giveaways ((state IN ('open', 'closed'))) WHERE state IN ('open', 'closed');

-- Once closed, the entry list and its fingerprint are fixed.
CREATE TRIGGER giveaways_fingerprint_fixed BEFORE UPDATE OF fingerprint, entry_count ON giveaways
	WHEN OLD.fingerprint != ''
	BEGIN SELECT RAISE(ABORT, 'a closed giveaway''s entry list never changes'); END;
CREATE TRIGGER giveaways_finished_stay BEFORE UPDATE OF state ON giveaways
	WHEN OLD.state IN ('ended', 'cancelled')
	BEGIN SELECT RAISE(ABORT, 'a finished giveaway never reopens'); END;
CREATE TRIGGER giveaways_kept BEFORE DELETE ON giveaways
	BEGIN SELECT RAISE(ABORT, 'giveaways are kept'); END;

-- One entry per chat account, in the order they entered.
CREATE TABLE giveaway_entries (
	giveaway_id  TEXT NOT NULL REFERENCES giveaways (id),
	seq          INTEGER NOT NULL,
	platform     TEXT NOT NULL,
	user_id      TEXT NOT NULL,
	login        TEXT NOT NULL,
	display_name TEXT NOT NULL,
	event_id     TEXT NOT NULL,
	entered_at   TEXT NOT NULL,
	announced    INTEGER NOT NULL DEFAULT 0 CHECK (announced IN (0, 1)),
	PRIMARY KEY (giveaway_id, seq),
	UNIQUE (giveaway_id, platform, user_id)
) STRICT;

CREATE TRIGGER giveaway_entries_only_while_open BEFORE INSERT ON giveaway_entries
	WHEN (SELECT state FROM giveaways WHERE id = NEW.giveaway_id) != 'open'
	BEGIN SELECT RAISE(ABORT, 'entries are closed'); END;
CREATE TRIGGER giveaway_entries_fixed BEFORE UPDATE OF giveaway_id, seq, platform, user_id, login, display_name, event_id, entered_at
	ON giveaway_entries
	BEGIN SELECT RAISE(ABORT, 'an entry never changes'); END;
CREATE TRIGGER giveaway_entries_kept BEFORE DELETE ON giveaway_entries
	BEGIN SELECT RAISE(ABORT, 'entries are kept'); END;

-- Someone turned away, once per giveaway and reason.
CREATE TABLE giveaway_refusals (
	giveaway_id  TEXT NOT NULL REFERENCES giveaways (id),
	platform     TEXT NOT NULL,
	user_id      TEXT NOT NULL,
	login        TEXT NOT NULL,
	display_name TEXT NOT NULL,
	reason       TEXT NOT NULL,
	refused_at   TEXT NOT NULL,
	announced    INTEGER NOT NULL DEFAULT 0 CHECK (announced IN (0, 1)),
	PRIMARY KEY (giveaway_id, platform, user_id)
) STRICT;

-- Each draw: who, picked how, and what came of it. Only the outcome
-- changes, and only forward.
CREATE TABLE giveaway_draws (
	id           TEXT PRIMARY KEY,
	giveaway_id  TEXT NOT NULL REFERENCES giveaways (id),
	n            INTEGER NOT NULL,
	entry_seq    INTEGER NOT NULL,
	pick_index   INTEGER NOT NULL,
	candidates   INTEGER NOT NULL CHECK (pick_index >= 0 AND pick_index < candidates),
	outcome      TEXT NOT NULL CHECK (outcome IN ('won', 'pending_claim', 'claimed', 'forfeited', 'set_aside')),
	claim_until  TEXT,
	drawn_at     TEXT NOT NULL,
	settled_at   TEXT,
	settled_by   TEXT NOT NULL DEFAULT '',
	UNIQUE (giveaway_id, n),
	UNIQUE (giveaway_id, entry_seq),
	FOREIGN KEY (giveaway_id, entry_seq) REFERENCES giveaway_entries (giveaway_id, seq)
) STRICT;

CREATE TRIGGER giveaway_draws_fixed BEFORE UPDATE OF id, giveaway_id, n, entry_seq, pick_index, candidates, claim_until, drawn_at
	ON giveaway_draws
	BEGIN SELECT RAISE(ABORT, 'a draw never changes'); END;
CREATE TRIGGER giveaway_draws_forward BEFORE UPDATE OF outcome ON giveaway_draws
	WHEN NOT (
		(OLD.outcome = 'pending_claim' AND NEW.outcome IN ('claimed', 'forfeited', 'set_aside')) OR
		(OLD.outcome IN ('won', 'claimed') AND NEW.outcome = 'set_aside')
	)
	BEGIN SELECT RAISE(ABORT, 'a draw''s outcome settles once'); END;
CREATE TRIGGER giveaway_draws_kept BEFORE DELETE ON giveaway_draws
	BEGIN SELECT RAISE(ABORT, 'draws are kept'); END;

-- What a giveaway says in chat, claimed before sending and never resent.
-- A practice giveaway's messages are kept here and never sent.
CREATE TABLE giveaway_messages (
	id          INTEGER PRIMARY KEY,
	giveaway_id TEXT NOT NULL REFERENCES giveaways (id),
	text        TEXT NOT NULL,
	state       TEXT NOT NULL CHECK (state IN ('queued', 'sending', 'sent', 'failed', 'unknown', 'practice')),
	detail      TEXT NOT NULL DEFAULT '',
	created_at  TEXT NOT NULL,
	sent_at     TEXT
) STRICT;
