-- Viewers and the points ledger (docs/adr/0032-viewers-and-points.md).

-- One row per platform account, made only from real events (never test
-- events). Forgetting blanks the account and its names but keeps the row,
-- so the ledger that refers to it still adds up.
CREATE TABLE viewers (
	id           TEXT PRIMARY KEY,               -- v_<hex>, never reused
	platform     TEXT NOT NULL,
	user_id      TEXT NOT NULL,                  -- forgotten:<id> once forgotten
	login        TEXT NOT NULL,
	display_name TEXT NOT NULL,
	first_seen   TEXT NOT NULL,
	last_seen    TEXT NOT NULL,
	last_chat_at TEXT,
	messages     INTEGER NOT NULL DEFAULT 0,
	forgotten_at TEXT,
	UNIQUE (platform, user_id)
) STRICT;
CREATE INDEX viewers_login ON viewers (lower(login));
CREATE INDEX viewers_seen ON viewers (last_seen);

-- Where viewer records have read the inbox to, so nothing counts twice.
CREATE TABLE viewer_cursor (
	id  INTEGER PRIMARY KEY CHECK (id = 1),
	seq INTEGER NOT NULL
) STRICT;
INSERT INTO viewer_cursor (id, seq) VALUES (1, 0);

-- Currencies: "points" to begin with. Its id never changes; its name can.
CREATE TABLE currencies (
	id             TEXT PRIMARY KEY,
	name           TEXT NOT NULL,
	allow_negative INTEGER NOT NULL DEFAULT 0 CHECK (allow_negative IN (0, 1)),
	created_at     TEXT NOT NULL
) STRICT;
INSERT INTO currencies (id, name, created_at) VALUES ('points', 'points', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));

-- An account per currency and owner: a viewer's id, or system:issuer (where
-- points come from) and system:sink (where spent points go). The balance is
-- kept by the entries' trigger, in the same transaction.
CREATE TABLE ledger_accounts (
	id       INTEGER PRIMARY KEY,
	currency TEXT NOT NULL REFERENCES currencies (id),
	owner    TEXT NOT NULL,
	balance  INTEGER NOT NULL DEFAULT 0,
	UNIQUE (currency, owner)
) STRICT;
CREATE INDEX ledger_accounts_top ON ledger_accounts (currency, balance);

-- Each transaction's entries sum to zero. Nothing is changed or deleted; a
-- mistake is undone by a reversal, once.
CREATE TABLE ledger_transactions (
	id         TEXT PRIMARY KEY,
	op_id      TEXT NOT NULL UNIQUE,
	kind       TEXT NOT NULL CHECK (kind IN ('credit', 'debit', 'adjust', 'reverse')),
	currency   TEXT NOT NULL REFERENCES currencies (id),
	viewer     TEXT NOT NULL,
	amount     INTEGER NOT NULL,             -- to the viewer: + earned, - spent
	memo       TEXT NOT NULL DEFAULT '',
	by         TEXT NOT NULL,
	reverses   TEXT UNIQUE REFERENCES ledger_transactions (id),
	created_at TEXT NOT NULL
) STRICT;
CREATE INDEX ledger_transactions_viewer ON ledger_transactions (viewer, created_at);

CREATE TABLE ledger_entries (
	tx      TEXT NOT NULL REFERENCES ledger_transactions (id),
	account INTEGER NOT NULL REFERENCES ledger_accounts (id),
	amount  INTEGER NOT NULL,
	PRIMARY KEY (tx, account)
) STRICT;

CREATE TRIGGER ledger_transactions_kept BEFORE UPDATE ON ledger_transactions
	BEGIN SELECT RAISE(ABORT, 'the ledger is append-only'); END;
CREATE TRIGGER ledger_transactions_not_deleted BEFORE DELETE ON ledger_transactions
	BEGIN SELECT RAISE(ABORT, 'the ledger is append-only'); END;
CREATE TRIGGER ledger_entries_kept BEFORE UPDATE ON ledger_entries
	BEGIN SELECT RAISE(ABORT, 'the ledger is append-only'); END;
CREATE TRIGGER ledger_entries_not_deleted BEFORE DELETE ON ledger_entries
	BEGIN SELECT RAISE(ABORT, 'the ledger is append-only'); END;

-- An entry moves its account's balance.
CREATE TRIGGER ledger_entries_balance AFTER INSERT ON ledger_entries
	BEGIN UPDATE ledger_accounts SET balance = balance + NEW.amount WHERE id = NEW.account; END;

-- A viewer never goes below zero unless the currency allows it, even from
-- a direct write.
CREATE TRIGGER ledger_accounts_not_negative BEFORE UPDATE OF balance ON ledger_accounts
	WHEN NEW.balance < 0 AND OLD.owner NOT LIKE 'system:%'
		AND (SELECT allow_negative FROM currencies WHERE id = OLD.currency) = 0
	BEGIN SELECT RAISE(ABORT, 'not enough points'); END;
