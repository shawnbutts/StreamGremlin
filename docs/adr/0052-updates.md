# ADR-0052: Updates from GitHub releases, signed with the user's key

- **Status:** Accepted
- **Date:** 2026-11-16
- **Based on:** D-120. StreamWisp had no updater; nothing was copied.

## Context

The wider beta (D-110) goes to testers the user hasn't met. Without a way to update, they stay on old betas, and reports describe bugs already fixed. Releases are built by GitHub (D-118) and published on the repository's releases page. The installers aren't code-signed (D-110), so each manual install shows Windows' and macOS's warnings, and testers learn to click through them.

An updater that downloads and runs code is a way into every tester's computer. It must install only what the user released.

## Decision

### Checking

`internal/updates` asks `GET https://api.github.com/repos/shawnbutts/StreamGremlin/releases?per_page=30` with `X-GitHub-Api-Version: 2026-03-10` and `User-Agent: StreamGremlin/VERSION`, unauthenticated. It does this a minute after start, then once a day, while the switch (setting `updates`, `{"check": true}` by default) is on. Check now is allowed once a minute. A 403 or 429 is respected until `x-ratelimit-reset`. Drafts aren't listed to the public. Each release's `tag_name` is read as `v` and a SemVer version, and the newest one newer than the running version wins. A full release only moves to full releases; a pre-release moves to either. Development builds (version `dev`) never check.

A release can be installed from the app when it has, among its uploaded assets, this system's download (`StreamGremlin-V-windows-x64-setup.exe` for Windows x64, `StreamGremlin-V-macos.dmg` for macOS), `SHA256SUMS.txt` and `SHA256SUMS.txt.sig`. Otherwise the app says why and links to the release page. GitHub's page is the only one the app may open there (`desktop.AllowedURL`).

### Signatures

The release key is Ed25519, made by `go run ./cmd/release keygen FILE`. The private key file is one line, `streamgremlin-release-key-v1 SEED`, with the 32-byte seed in base64. It lives on the user's Mac, mode 0600, never in the repository or on GitHub.

`SHA256SUMS.txt.sig` is one line:

```
streamgremlin-sig-v1 KEYID SIGNATURE
```

- `KEYID` is the first 8 bytes of SHA-256 of the public key, in hex.
- `SIGNATURE` is base64 (standard, padded) of the Ed25519 signature over `StreamGremlin release SHA256SUMS v1\n` followed by the exact bytes of `SHA256SUMS.txt`. The prefix keeps a signature from meaning anything else.

The app accepts a signature from any key in `internal/updates/keys.go`, and the file is at most 1 KiB. To change keys:
1. add the new public key;
2. release a version signed with the old key;
3. sign with the new key from then on;
4. remove the old key later.

If the key is lost, testers install the next version by hand.

The signature covers names and hashes. Each download's name includes its version, and the app looks up the name for the release it chose. So an older signed `SHA256SUMS.txt` can't stand in for a newer release.

### Downloading

Download fetches `SHA256SUMS.txt` (at most 1 MiB) and the signature, and verifies them. It then streams the download into `DATA/updates/NAME.part`, hashing as it goes, at most 1 GiB, over HTTPS only (redirects included). It renames the file only if the hash matches the signed line. Anything else is deleted and reported. The folder is cleared at every start. Before installing, the file is hashed again.

### Installing

`POST /desktop/install-update` is the desktop app's alone, since only the app can quit and be reopened. The engine:
1. refuses while the streamer is live on Twitch or YouTube;
2. checks the file again;
3. makes a `pre-update` backup;
4. starts the installer, which waits for the app to quit.

The app then quits.

- **Windows:** `setup.exe /S /update`, detached. In update mode the NSIS installer waits up to a minute for `StreamGremlin.exe` to be free and installs silently to the same place. It leaves the desktop shortcut off if there wasn't one, then `Exec`s the app. It installs for the current user and asks for no administrator.
- **macOS:**
  1. `hdiutil attach -nobrowse -readonly` to a temporary folder;
  2. check `CFBundleIdentifier` is `com.streamgremlin.desktop`;
  3. `ditto` the app to `.StreamGremlin-update.app` beside the installed one;
  4. rename the installed app to `.StreamGremlin-old.app`, and the new one into its place, putting the old one back if that fails;
  5. a detached `/bin/sh` waits for the app's process to end, removes the old app and `open`s the new one.

  Running from `/Volumes` or an AppTranslocation path is refused with words saying what to do. A leftover old or staged app is removed at the next start. An ad-hoc signature changes with each build, so macOS may ask again for keychain access; the help says so.
- **Linux:** not installed by the app.

### API

| Route | What |
|---|---|
| `GET /v1/updates` | the running version, the switch, the last check, the newest release with notes, and the download's state |
| `POST /v1/updates/check` | check now |
| `POST /v1/updates/download` | download and verify in the background |
| `PUT /v1/settings/updates` | `{check}` |

All are settings permissions. The CLI's `streamgremlin updates` uses them; installing stays in the app.

## Consequences

- An update from a compromised GitHub account, a CDN or a network in between is refused. A compromised release Mac with the key could ship one, as it could build a malicious installer today.
- Updates skip Windows SmartScreen and macOS Gatekeeper, because the app downloads the file and no browser marks it. That's acceptable only because of the signature. It's recorded in D-120.
- Every release now needs one step on the user's Mac before publishing (`make release-sign`). The workflow can't do it.
- A re-run of the release workflow after signing replaces `SHA256SUMS.txt`, and the app then refuses that release until it's signed again. That fails safe.
- Testers on beta.5 or earlier update by hand once, to beta.6.
