# ADR-0016: Sounds

- **Status:** Accepted
- **Date:** 2026-11-08
- **Based on:** StreamWisp ADR-0042 (the desktop-hosted agent's sounds), D-057 and D-066 there. Changed: StreamGremlin plays sounds in the app's window, not with each system's own player, and keeps the library in the database.

## Context

F2 needs sounds: a follow plays a chime, a redemption an air horn (D-004; FEATURES "Sounds, played by the desktop app, with volume per sound").

**StreamWisp ran each system's own player:** `afplay` on macOS, PowerShell `System.Media.SoundPlayer` on Windows, and `pw-play`, `paplay` or `aplay` on Linux. On Windows that player plays WAV only and has no volume. StreamWisp marked it "limited", and its backends other than macOS were only tested against a fake player.

**Streamer.bot** has a "Play Sound" sub-action, with a volume for each use.

**The app's window is a full browser** (WKWebView on macOS, WebView2 on Windows):
- Its `<audio>` element plays MP3, WAV and M4A in both, and OGG in WebView2 and recent WKWebView.
- It has a volume, and plays several sounds at once.
- Wails v3 can let it play without a click first:
  - macOS: `MacWebviewPreferences.EnableAutoplayWithoutUserAction`, which sets `mediaTypesRequiringUserActionForPlayback` to none;
  - Windows: WebView2's `--autoplay-policy=no-user-gesture-required` browser flag.
- The app quits when its window closes (`ApplicationShouldTerminateAfterLastWindowClosed`), so while the engine runs in the app, the window exists.

## Decision

### The library (`internal/sounds`, migration `0010_sounds.sql`)

- **Kept in the database** (a BLOB per sound), so backups and restores carry it without a new archive format (ADR-0012), and there are no file paths to check.
- **Each sound:**
  - a name (`[a-z0-9][a-z0-9_-]{0,63}`, suggested from the file's name);
  - a format sniffed from the first bytes, never the file's name: WAV (`RIFF…WAVE`), OGG (`OggS`), M4A (`ftyp` with an audio brand), MP3 (`ID3` or an MPEG audio frame);
  - its size, SHA-256 and volume (0–100, default 80).
- **Limits:** 5 MB a sound, 100 sounds, 100 MB in all. Retention never sweeps sounds.
- **Adding:**
  - The same audio under the same name again is harmless (200).
  - Other audio under a taken name is refused (409): nothing is replaced by accident.

### Playing (`sounds.Player`)

**The engine never plays anything.** The app's window is the player:
1. It asks `GET /v1/player/next?after=&wait=` (a long poll, up to 25 seconds, under the server's write timeout). The first ask uses `after=-1`, so a window that opens plays nothing queued before it.
2. It plays each command with an `<audio>` element at the command's volume, from `GET /v1/sounds/{name}/audio`. That route serves ranges, and goes through the desktop bridge, so the token never reaches the page. The app's CSP gains `media-src 'self'`.
3. It reports whether `play()` started (`POST /v1/player/reports`), with the browser's reason when it didn't (such as `NotAllowedError`).

**Honest results.** A play waits up to 5 seconds for the report:

| What happened | The play says |
|---|---|
| it started | played |
| it didn't start | failed, with the reason |
| no window has asked for 40 seconds | "no StreamGremlin window is open to play sounds", at once (503 from the API) |
| no answer | "the app didn't say whether it played" (504, `unknown_outcome`) |

- **Stale plays are skipped:** commands older than 10 seconds aren't played, so a window that was asleep doesn't play a burst.
- **Limits in the window:**
  - up to 4 sounds at once (a fifth is reported as refused);
  - a sound stops after 10 minutes;
  - "Stop" stops them all;
  - asks are at least 50 ms apart, so an engine answering at once can't make the window ask in a tight loop.

### Steps, API, CLI and screen

- **Steps:**
  - `sounds.play {sound, volume?}` "Play a sound";
  - `sounds.stop` "Stop sounds".

  Both are External: a test event never makes a sound, and the trace shows what would have played. A retried step with the same idempotency key doesn't play again for an hour, including when the window didn't answer (it may have played). A refused play isn't remembered, so a retry tries again.
- **API**, with new permissions `sounds.read` and `sounds.write`:
  - `GET /v1/sounds` (with `player`: whether a window is playing);
  - `POST /v1/sounds/{name}?volume=`: the body is the audio. Routes can now set their own body limit; this one is 5 MB, and the rest keep 1 MiB.
  - `PUT /v1/sounds/{name} {volume}`;
  - `DELETE /v1/sounds/{name}`;
  - `GET /v1/sounds/{name}/audio`;
  - `POST /v1/sounds/{name}/play`;
  - `POST /v1/player/stop`;
  - the player's `GET /v1/player/next` and `POST /v1/player/reports`.
- **CLI:** `streamgremlin sounds`, `sounds add [-name] [-volume] FILE`, `play [-volume] NAME`, `volume NAME V`, `delete NAME`, `stop`.
- **The Sounds screen** (Ctrl/⌘+7):
  - whether this window plays sounds;
  - each sound with its format, size, a volume slider (saved when let go), Play and Delete (in the app's dialog);
  - Stop all sounds;
  - an Add form: a file, a name suggested from it, a volume. Files over 5 MB are refused before sending.

  The editor's "Play a sound" picks from the library.
- **Navigation:** with ten screens, the numbers go to the first nine (Help is Ctrl/⌘+9). Settings is Ctrl/⌘+comma only, as in most apps.

## Alternatives considered

- **Each system's player (StreamWisp):**
  - Windows would be WAV-only with no volume, against the per-sound volume this feature needs.
  - It runs a process per sound.
  - Its Windows and Linux backends were never heard on real systems.
- **A Go audio library:** native dependencies on each system, and its own device handling.
- **Files in `<data>/sounds`:** they'd need the backup archive to grow a sounds section, plus path checks. The database already does both jobs.
- **Fire and forget, without reports:** a trace would say "played" for a sound blocked by the webview, which is the unknown-as-confirmed mistake AGENTS.md forbids.

## Consequences

- **Sounds play only while the app is open.** The headless `engine` command, used for development and tests, has no window: plays fail and say why. A remote or headless player is for later (D-057 in StreamWisp; not decided here).
- **OBS hears sounds through Desktop Audio,** as with any app. A separate output device isn't offered.
- **Backups grow with the library:** up to 100 MB in each.
- **Not yet confirmed on real systems:**
  - OGG in WKWebView on older macOS;
  - autoplay without a click on both systems.

  The live check confirms them. If a sound doesn't play, its trace and the Sounds screen show the browser's reason.
