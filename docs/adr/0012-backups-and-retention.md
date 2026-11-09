# ADR-0012: Backups, restore and retention

- **Status:** Accepted
- **Date:** 2026-11-08
- **Based on:** StreamWisp ADR-0047 (backups and restore) and the retention sweep in its ADR-0037. Adapted: StreamGremlin has no plugins or sound library yet, so a backup is the database alone, in a container that can take more files later.

## Context

Until now the only backups were the database copies taken before a schema change (ADR-0001), and restoring meant copying files by hand. A streamer needs to:
- get back to yesterday after a mistake;
- move to a new computer;
- keep the data folder from growing forever.

Secrets must never be in a backup (AGENTS.md). They live in the OS keyring or the encrypted secrets file, never in the database (ADR-0003).

## Decision

### A backup (`internal/backup`)

A backup is one ZIP archive, `<data>/backups/streamgremlin-<kind>-<UTC time>.zip`, mode 0600. It holds:
- `streamgremlin.db`: a consistent copy, made with `VACUUM INTO` while the engine runs;
- `manifest.json` (`streamgremlin.backup/v1`): the kind, the time, the app version, the schema version, and each file's path, size and SHA-256.

**Nothing else is ever read into a backup:** not the secrets file or its key, the API token, the address file, the lock, logs or other backups. A test puts a sign-in in the encrypted secrets file and checks that its value is in no file of the archive.

**Kinds:**

| Kind | Made by | Removed |
|---|---|---|
| `manual` | "Back up now", `POST /v1/backups`, `streamgremlin backups create` | only by the streamer |
| `scheduled` | the engine, every 24 hours by default, the first a minute after start; a failure is shown and retried after an hour | beyond the newest 7 (adjustable) |
| `pre-restore` | every restore, before it changes anything | only by the streamer |
| `pre-migration` | schema changes (ADR-0001), as plain `.db` files | only by the streamer; listed and restorable |

### Checking a backup (`POST /v1/backups/{name}/verify`, and before every restore)

A backup is refused when:
- the name isn't a backup's name in the backups folder (so `../` and other paths can't be named);
- the manifest is missing, malformed or another format;
- a file is missing, listed twice, not in the manifest, or doesn't match its size and hash (reading at most one byte past the declared size);
- SQLite's integrity check fails (opened read-only and immutable, so checking never changes a backup);
- its schema is newer than this build, or a schema step differs from this build's.

### Restoring is staged

`POST /v1/backups/{name}/restore` checks the backup and marks it. Nothing changes until the engine next starts.
1. **At start, before anything opens the database,** the mark is removed first, so a bad backup can't stop every later start.
2. The backup is checked again and extracted inside the data folder.
3. The current state is saved as a `pre-restore` backup. If the current database can't even be opened (it's damaged), its files are kept as they are in `backups/before-restore-<time>/` instead.
4. The database and its `-wal` and `-shm` files are moved aside, and the backup's database is moved in. All renames stay inside the data folder, and any failure moves everything back.
5. An older schema is migrated as usual, taking its own pre-migration copy.
6. The outcome is kept in `restore-result.json` and shown on Settings and by `streamgremlin backups`.

**Restarting:**
- **The desktop app** restarts its engine right away (`POST /desktop/restart-engine`, a bridge endpoint) and reloads.
- **The CLI** says to restart StreamGremlin.
- A waiting restore can be cancelled (`DELETE /v1/backups/{name}/restore`).

**Secrets stay where they are.** On the same computer, sign-ins keep working after a restore. On a new one, the streamer signs in again; Home's checklist and Needs you show it.

### Retention (`internal/maintenance`)

A sweep runs a minute after start and every 6 hours, or on demand ("Clear old history now", `POST /v1/maintenance/sweep`).
- **Events** received longer ago than the setting (default 30 days) are deleted, except those of runs still waiting or running.
- **Finished runs** (default 90 days) are deleted with their traces.
- **Never swept:** workflows, their versions, saved variables and settings.
- **0 keeps forever.**

Deleting old events doesn't move the inbox's cursor: `seq` keeps counting up (AUTOINCREMENT). Migration `0007_retention.sql` adds indexes on `events.received_at` and `jobs.finished_at`.

### Settings, API and screens

- **Settings** (key `maintenance`, `PUT /v1/settings/maintenance`): back up every 1 to 168 hours or never (0), keep 1 to 60 automatic backups, keep events and runs for 0 to 3650 days.
- **API:**
  - `GET /v1/backups`: the list, the waiting restore, the last restore's outcome, the settings and the status (last and next automatic backup, last sweep);
  - `POST /v1/backups`, `POST …/verify`, `POST`/`DELETE …/restore`, `DELETE /v1/backups/{name}`;
  - permissions `backups.read` and `backups.write`.
- **Settings → Backups:**
  - each backup in words ("Made by you", "Automatic", "Before a restore", "Before an update"), with Check, Restore (after the app's dialog, which says what's kept and that sign-ins aren't changed) and Delete;
  - "Back up now";
  - a waiting restore, with "Restart now" and "Cancel";
  - the last restore's outcome.
- **Settings → Automatic backups and history:** the schedule and retention settings, "Clear old history now", and the status.
- **CLI:** `streamgremlin backups [create | verify NAME | restore NAME | cancel-restore | delete NAME]`.

## Alternatives considered

- **Restoring while the engine runs:** every service holds the database open. Stopping first, by restarting, is simpler and can't leave a half-restored engine.
- **A bare `.db` file instead of an archive:** simpler today, but a manifest gives integrity checking now, and room for overlays, sounds and plugin files later without a new format.
- **Including the secrets file:** convenient on one computer, but it breaks "secrets never appear in backups", and keyring secrets can't be copied anyway.
- **Encrypting backups:** the key would have to live somewhere else and could be lost with the computer. Backups are as private as the database itself (mode 0600 in the private data folder).
- **Redacting old events instead of deleting them (StreamWisp's YouTube sweep):** that was for a platform policy on YouTube data. For local history, deleting is what "keep for 30 days" means, and jobs don't depend on their event rows.

## Consequences

- **Disk use:** up to 7 daily archives, plus the streamer's own, pre-restore and pre-migration ones. Only automatic ones are pruned.
- **What a backup exposes:** a backup holds viewers' names and chat history, like the database. Moving one off the computer is the streamer's choice.
- **YouTube (M3)** will add its 30-day policy to the sweep.
- **Sounds are in the database** (ADR-0016), so backups carry them with no change to the archive. Overlays are in the database too (ADR-0017), with only their links' hashes, so backups carry no working link.
