# ADR-0003: Secrets

- **Status:** Accepted
- **Date:** 2026-11-08
- **Based on:** StreamWisp ADR-0009 (secrets and connections). The connection model in that ADR is left for the Twitch and OBS slices.

## Context

From M1 slice 4 on, StreamGremlin holds Twitch OAuth tokens (for the streamer and the bot account) and the OBS WebSocket password. These must never appear in logs, exports, backups, bug reports, traces or API answers (PRINCIPLES.md). They should sit in the OS credential store where there is one, with an encrypted file as the documented fallback (D-021 in StreamWisp). Tests must never touch the developer's real keyring (AGENTS.md).

StreamGremlin is desktop-only for now (D-003), but headless Linux is a possible future. So the fallback must work without a desktop session, and nothing here may depend on Wails.

## Decision

**Values** (`internal/secrets`)
- A secret is a `secrets.Value`. Every way of printing, logging or encoding it gives `[redacted]`: `String`, `GoString`, every `fmt` verb, `slog`, JSON and text. It can't be decoded from JSON or text. Only `Reveal()` returns the contents, where the value leaves for the service that needs it.
- Keys are lowercase segments joined by `/`, for example `twitch/<user id>/oauth` or `obs/password`. Each segment uses `a-z 0-9 _ . -` and can't start with `.`. A key has at most 128 characters and a value at most 2048 bytes. That fits Windows Credential Manager's 2560-byte limit and the macOS `security` tool's 4096-byte command line after encoding.

**Stores**
- **`keyring`** is the OS credential store, through `github.com/zalando/go-keyring` v0.2.8 (pure Go):
  - macOS Keychain, through `/usr/bin/security`; values go over stdin, never argv;
  - Windows Credential Manager;
  - Secret Service over D-Bus on Linux.

  Each secret is one item, with service `StreamGremlin (<installation id>)` and the key as the account, so two data folders never share items. Every call has a 25-second limit, so a locked keyring that nobody unlocks can't hang the engine and the API still answers within its 30-second write timeout.
- **`file`** is one AES-256-GCM file, `<data>/secrets.enc`:
  - The key is 32 random bytes in `<data>/secrets.key` (mode 0600). It is made with the first secret and is never replaced.
  - The installation id is authenticated as associated data, so files copied from another data folder don't decrypt.
  - Every save uses a fresh random nonce and writes atomically: a synced temporary file is renamed over the old one.
  - On Unix, either file is refused if group or other users can read it, as ssh does.
  - A file that can't be read (wrong key, changed bytes, missing key) is reported as damaged. Saving then refuses, rather than replacing secrets it couldn't read.

**Choosing the store**
- The store is chosen on the data folder's first start and recorded in the `secret_store` table, together with a random installation id. After that it's fixed. Asking for the other store is an error (`ErrStoreMismatch`), so secrets never end up split between two stores. Moving between stores is a later, explicit command if anyone needs it.
- `auto` (the default) uses the keyring if a read-only lookup of a missing item answers within 5 seconds. Otherwise it uses the file.
  - On Linux and BSD, `auto` skips the keyring when `DBUS_SESSION_BUS_ADDRESS` is unset, so the D-Bus library doesn't start a stray daemon.
  - The lookup never writes.
- Later starts don't touch the keyring at all, so a locked keyring can't stop the engine from starting. Only reading, saving and forgetting a secret reach it.
- The desktop app always uses `auto`. The dev-only `engine` command takes `-secret-store auto|keyring|file`, which defaults to `$STREAMGREMLIN_SECRET_STORE`.

**Index**
- The `secrets` table holds keys and times, never values. It's needed because the keyring can't list items by service on every OS.
- **Saving** writes the index row first, then the value. A crash in between leaves a listed secret that checks as missing, never a hidden item in the keyring.
- **Forgetting** removes the value first, then the row. It's harmless to repeat.
- **Database backups** (`VACUUM INTO`) therefore never contain secret values.

**API** (permissions `secrets.read` and `secrets.write`)
- `GET /v1/secrets`: the store (`kind`, plus a `label` such as "macOS Keychain"), and the saved keys with their times.
- `GET /v1/secrets?check=true`: also tries reading each one. Each result is `ok`, `missing` or `unreadable`, with a reason. The whole check is limited to 25 seconds, and secrets not reached in time are reported as unreadable.
- `DELETE /v1/secrets/{key}`: forgets one. It answers 204 even when the secret was already gone.
- No route returns or accepts a value. Secrets are saved by the features that use them (Twitch sign-in, the OBS connection), inside the engine.

**Tests**
- Unit tests use a fake keyring.
- In a test binary (`testing.Testing()`), `auto` picks the file without probing, and asking for the real keyring is refused unless `STREAMGREMLIN_TEST_OS_KEYRING=1`. That variable runs one opt-in test, which writes, reads and deletes a single item, then cleans up.
- Acceptance tests set `STREAMGREMLIN_SECRET_STORE=file` for every engine they start.

## Alternatives considered

- **`99designs/keyring`:** it has more backends, but its Keychain support needs cgo, and its last release was in 2022.
- **A plaintext 0600 file only:** simplest, but a copy of the data folder, for example in a cloud-synced backup, would expose every token. The separate key file at least keeps copies of `secrets.enc` alone useless.
- **A key derived from a passphrase:** a headless engine would need it typed at every start.
- **Choosing the store on every start:** secrets would silently move between stores depending on whether the keyring answered that day.
- **Index first for forgetting too:** a crash would leave a value in the keyring that no list shows.

## Consequences

- Secrets are protected from other users of the computer and from accidental output. They are **not** protected from malicious code running as the same user. Keychain items created through `/usr/bin/security` can be read by any process of that user, just as a 0600 file can.
- With the key file next to `secrets.enc`, encryption only protects copies that leave the key behind. That limit is written on the Settings screen.
- Moving a data folder aside leaves its Keychain items behind, under the old installation id. They're harmless, and can be removed in Keychain Access or Credential Manager by searching for "StreamGremlin (".

## Open questions

- Whether to offer moving secrets between stores, and key rotation for the file.
- Whether the desktop app should warn more strongly when it falls back to the file.
