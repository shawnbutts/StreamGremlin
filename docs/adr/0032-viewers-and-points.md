# ADR-0032: Viewers and the points ledger

- **Status:** Accepted
- **Date:** 2026-11-10
- **Based on:** StreamWisp ADR-0016 (viewer records) and ADR-0017 (the points ledger), and its rule D-115 (the native save dialog). Changed:
  - viewers follow the inbox with a saved cursor in the same transaction, rather than at ingest;
  - one currency, `points`, renamable (more currencies are F4 slice 4);
  - the points steps are External, so a test event never changes a real balance.

## Context

F4 is points (D-004, D-006). AGENTS.md:
- ledgers are append-only;
- balances never go below zero unless the streamer allows it;
- duplicate delivery never pays twice;
- secret tokens never appear in exports.

The feature cut lists viewer records with export and delete (Spine), but they weren't built. Points need them, so they come first.

## Decision

### Viewer records (migration 0020)

- **One row per platform account,** with a local id `v_<hex>` that's never reused. Each row keeps:
  - the newest login and name;
  - first and last seen, and last chat;
  - a count of chat messages.

  Chat text isn't kept here.
- **Made only from real events with a viewer:**
  - never test events;
  - never StreamGremlin's own (`core`) events.
- **Read in order from the inbox,** with a cursor saved in the same transaction as each batch, so an event never counts twice, even across a restart.
- **Out-of-order events** never move times the wrong way. A rename follows the newest event.
- **Ensure** makes a record for an account a step gives points to before any event recorded them.
- **Search:** by the start of a login or name, any case, with `%` and `_` matched literally.
- **Export:** `streamgremlin.viewer-export/v1`, the record and the full points history. No tokens, nothing secret.
- **Forget:** blanks the account id (to `forgotten:<id>`), the names and the activity, and stamps `forgotten_at`.
  - The row stays, so the ledger that refers to it still adds up.
  - If the account takes part again, it's a new, unconnected viewer.

### The ledger (migration 0020)

- **`currencies`:** `points` (its name can change; its id never does), and whether it may go below zero (not, for points).
- **`ledger_accounts`:** per currency and owner, either a viewer's id or `system:issuer` (where points come from) and `system:sink` (where spent points go). The balance is kept by a trigger on each entry, in the same transaction.
- **`ledger_transactions` and `ledger_entries`:**
  - two entries per transaction, summing to zero;
  - both tables are append-only (triggers refuse UPDATE and DELETE);
  - `op_id` is unique;
  - a reversal names the original (`reverses`, unique), so it happens once.
- **No negatives:** a trigger refuses a viewer's balance below zero in a currency that doesn't allow it, even from a direct SQL write.
- **Operations:**
  - **Credit** (earned);
  - **Debit** (spent): not enough is `not enough points: has 50, needs 80`, with nothing changed;
  - **Adjust** (by hand, either way);
  - **Undo:** reverses once. It can't take a viewer below zero, and an undo can't itself be undone.
- **Repeats:**
  - the same operation id with the same content returns the original (`applied: false`);
  - the same id with other content is refused.

  40 concurrent credits over 4 ids pay exactly 4 times.
- **Audit:** every transaction has both entries and sums to zero, and every balance equals the sum of its entries. A balance changed by hand is caught.

### Steps

- **"Give points" and "Take points"** are External, so a test event never changes a real balance.
  - The operation id is the step's idempotency key.
  - The viewer is the event's, or `@name`.
  - The output is the balance, the amount and the currency's name.
- **"Check points"** only reads, so it runs for tests too.
- **A template, "Check your points" (`!points`),** replies with the balance and the currency's name.

### Screen, API, CLI

- **The Points screen** (in the main list, after Giveaways; Variables moves to Ctrl/⌘+9):
  - who holds how many, and the top viewers;
  - rename;
  - Check the ledger;
  - find a viewer;
  - their history with Undo (asks first);
  - give or take with a reason;
  - Export their record, through the system's save dialog;
  - Forget them (asks first).
- **The native save dialog** (`/desktop/save-file`, the rule D-115 here):
  - the UI suggests a file name and sends the text;
  - the system's dialog decides where;
  - the file is written with mode 0600;
  - a cancel saves nothing;
  - bad names are refused.
- **API:**
  - viewers (`viewers.read`, `viewers.write`): `GET /v1/viewers?q=`, `GET /v1/viewers/{id}`, `GET /v1/viewers/{id}/export`, `POST /v1/viewers/{id}/forget`;
  - points (`points.read`, `points.write`): `POST /v1/viewers/{id}/points {amount, reason, request_id}`, `GET /v1/points`, `PUT /v1/points/currency`, `POST /v1/points/transactions/{id}/undo`, `GET /v1/points/audit`.
- **CLI:** `viewers [NAME] | show | export | forget`, and `points | give | take | undo | rename | audit`.

## Alternatives considered

- **Balances as a column updated by the code:** a trigger on the entries keeps them in the same transaction, whoever writes.
- **Deleting a forgotten viewer:** the ledger would no longer add up. Blanking keeps it whole.
- **Points steps that run for tests and keep the change only for the run** (as variables do): a points balance is what viewers trust. A test never touches it.

## Consequences

- **Viewers appear as they take part,** from the events still in the inbox when this update first runs (retention keeps 30 days by default).
- **Earning rules, time in chat, more currencies, the shop, and points in giveaways and wheels** are the next F4 slices.
