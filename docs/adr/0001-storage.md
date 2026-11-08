# ADR-0001: Storage

- **Status:** Accepted
- **Date:** 2026-11-07
- **Based on:** StreamWisp ADR-0002 (SQLite driver) and ADR-0005 (storage conventions)

## Context

StreamGremlin keeps everything on the streamer's machine (PRINCIPLES.md). It needs one database that survives crashes, can't be opened by two engines at once, refuses data from a newer build, and is backed up before the schema changes. StreamWisp's storage held up without rewrites, but it ended with 95 frozen migrations in two weeks (lesson 10).

## Decision

**Data folder** (`internal/datadir`)
- `$STREAMGREMLIN_DATA_DIR` if set. Otherwise:
  - macOS: `~/Library/Application Support/StreamGremlin`
  - Windows: `%LocalAppData%\StreamGremlin`
  - Linux: `$XDG_DATA_HOME/streamgremlin`, or `~/.local/share/streamgremlin`
- The folder is created with mode 0700. The database, API token, address file and logs are 0600.
- Test binaries refuse to pick the default folder unless the variable is set, so tests can't touch the user's data.
- **One engine per folder:** an OS-level exclusive lock on `<data>/lock`. `flock` is used on macOS and Linux, and `LockFileEx` on Windows. The OS drops the lock when the process dies, so a crash never leaves a stale lock. The holder's process ID is written into the file for error messages.

**SQLite** (`internal/storage`)
- The driver is pure-Go `modernc.org/sqlite`, so the engine and CLI build without cgo.
- **Connection settings:**
  - `busy_timeout=5000`, `foreign_keys=ON`, `synchronous=FULL`.
  - Write transactions use `BEGIN IMMEDIATE`.
  - `Open` checks that each setting took effect.
  - `journal_mode=WAL` is set once, with a short retry, because switching on a brand-new file can return `SQLITE_BUSY` without using the busy handler.
- **Tables:**
  - All tables are `STRICT`.
  - Timestamps are text in UTC with milliseconds (`2006-01-02T15:04:05.000Z`), so they sort as strings.
  - JSON columns carry a `json_valid` check.
- **Migrations:**
  - Each migration is an embedded `internal/storage/migrations/NNNN_name.sql` file, numbered from 0001 with no gaps. There are no down migrations: going back means restoring the automatic backup.
  - Each migration runs in its own transaction, which re-reads the current version first, so two processes racing on a new file apply it exactly once.
  - `schema_migrations` records each migration's SHA-256. Triggers make that table append-only.
  - The engine refuses to start if an applied migration has changed (`ErrChecksumMismatch`), or if the database is newer than the build (`ErrDatabaseNewer`).
  - Before changing a database that already has a schema, the engine writes `VACUUM INTO <data>/backups/<time>-before-migration-NNNN.db`.
- **Numbered files during development, one baseline at release** (changed 2026-11-08, D-010). Each slice adds a new numbered file and never edits an applied one, so the streamer's development data folder keeps working from slice to slice. At the first public release, all files so far are squashed once into a new `0001_baseline.sql`. Development data folders are moved aside that one time, and from then on files are frozen. This keeps StreamWisp's lesson (it shipped 95 frozen migrations) without making every slice cost a fresh data folder.
- **Tables arrive with the slice that uses them.** Slice 1 adds only `settings`: small typed JSON documents keyed by group, such as `ui`.

## Consequences

- Restoring from a backup is the only way back from a migration. The backup is taken automatically, and the Backups feature (M1) will expose it.
- Squashing at the first public release costs every development data folder once. Slice 2 rewrote the baseline before this change, which cost one fresh data folder.
- Windows file permissions are left to the default ACL on `%LocalAppData%`, which is already private to the user.

## Alternatives considered

- **Up/down migration pairs (StreamWisp):** every down migration had to be written and tested, and none was ever used in practice. A backup is simpler and covers every case.
- **A lock file holding a PID:** that leaves stale locks after a crash and needs guesswork to clear them. OS locks don't.
- **cgo SQLite (`mattn/go-sqlite3`):** a little faster, but it breaks cgo-free CLI builds and cross-compilation.
