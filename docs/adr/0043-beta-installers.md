# ADR-0043: Beta installers for Windows and macOS

- **Status:** Accepted
- **Date:** 2026-11-12
- **Based on:** D-084 (beta testers; amends D-005's signing plan for the beta); D-005 (signing, the Twitch app); D-008 (macOS 13); [ADR-0042](0042-paired-clients.md) (the Stream Deck plugin's installer). StreamWisp's ADR-0052 planned release builds without building them.

## Context

The user wants a small group of beta testers on Windows and macOS. Until now StreamGremlin was a folder of programs copied by hand (`make windows`, `make desktop`), with no icon on Windows, no app bundle on macOS, and a Twitch app ID only when the builder remembered it.

D-005 planned to sign both systems' builds before outside testers. For this first small group the user chose unsigned builds, with instructions past the warnings (D-084).

## Decision

### One command, on macOS

`make release VERSION=0.1.0-beta.1 TWITCH_CLIENT_ID=…` runs `make verify`, then writes to `bin/release/`:
- `StreamGremlin-VERSION-windows-x64-setup.exe`;
- `StreamGremlin-VERSION-macos.dmg`;
- `SHA256SUMS.txt`.

It refuses to start without:
- **the Twitch app ID:** testers sign in with StreamGremlin's own app and never paste their own;
- **a plain version:** `X.Y.Z` or `X.Y.Z-word.N`, nothing else, so it's safe inside the plist and NSIS;
- **macOS:** for the dmg and `codesign`;
- **NSIS:** `makensis` cross-builds the Windows installer, so no Windows machine is needed.

Then `tests/release` (build tag `release`) checks what was built:
- every file is in each installer;
- each program is for the right system and architecture (the Windows app a window program, its CLI a console one; the Mac app and CLI both Apple silicon and Intel);
- each program holds the version and the Twitch app ID (compared, never printed);
- the macOS app's Info.plist, its icon and its ad hoc signature (`codesign --verify --strict --deep`);
- the Mac CLI runs and reports the version;
- the checksums.

### Windows: an NSIS installer, per user

`packaging/windows/StreamGremlin.nsi`.

- **Where it installs:** for the current user only, to `%LocalAppData%\Programs\StreamGremlin`, with no administrator prompt. There's no folder picker, so it can never be pointed at the data folder, `%LocalAppData%\StreamGremlin`.
- **What it installs:**
  - the app;
  - the CLI in `cli\`, because Windows file names ignore case;
  - the Stream Deck plugin in `Stream Deck\`;
  - the icon, the licences, and the beta guide as "Read me first.txt".
- **Shortcuts and Apps & features:** a Start menu shortcut, a desktop shortcut that can be unticked, and an entry in Apps & features.
- **The last page:** boxes to open StreamGremlin, show the beta guide, and **Install the Stream Deck plugin**. That third box is added beside Modern UI's two and starts unticked. Ticking it opens the plugin file, which Stream Deck takes and asks to install; when nothing opens it, the installer says to install Stream Deck 7.1 or later and where the file is (added 2026-11-12, at the user's request).
- **Updating:** installing over an older version replaces the programs and keeps the data. A running StreamGremlin holds its `.exe` open, so the installer and uninstaller ask to close it first and retry.
- **WebView2:** checked at Microsoft's documented registry key. When it's missing, the installer offers Microsoft's download page.
- **The uninstaller removes only what was installed:** each file by name, then only empty folders, never a whole folder. It also removes WebView2's cache for the window (`%AppData%\StreamGremlin.exe`, an exact path) and its Apps & features entry. It keeps the data folder and Credential Manager, and says where they are.
- **The icon:** `cmd/release ico` builds it from the brand kit's PNGs, 16 to 256 pixels, as 32-bit bitmaps every Windows version reads. The shortcuts and Apps & features use it, and Wails sets the window's icon itself.

### macOS: a universal app in a dmg

- **The app:** `StreamGremlin.app` is built for Apple silicon and Intel and joined with `lipo`. It runs on macOS 13 or later. Its Info.plist comes from `cmd/release plist`, and its icon from the brand kit through `iconutil`.
- **The CLI** is at `Contents/Helpers/streamgremlin`. In `Contents/MacOS`, `streamgremlin` and `StreamGremlin` would be the same file.
- **Signing:** the CLI is signed ad hoc, then the app. A downloaded app with no valid signature is called "damaged" by macOS, with no way to open it. With an ad hoc signature, System Settings offers **Open Anyway**.
- **The dmg** holds the app, a link to Applications, the Stream Deck plugin and the beta guide.

### The beta guide

[docs/BETA.md](../BETA.md) goes in both installers as "Read me first.txt". It covers:
- installing past SmartScreen and Gatekeeper;
- getting started;
- updating;
- what to put in a report (version, system, logs) and where the logs are;
- where the data is;
- what isn't there yet;
- uninstalling.

It also warns that beta data may need a fresh start at the first public release, when migrations are squashed (ADR-0001).

## Alternatives considered

- **Wails' own packaging (`wails3 package`):** it needs the `wails3` tool and Task, and its NSIS template removes the whole install folder on uninstall. Our script is short, removes only its own files, and builds with plain `makensis`.
- **An MSIX package for Windows:** it needs signing to install at all.
- **A per-machine install under Program Files:** it needs an administrator, and per-user is enough for one streamer's computer.
- **An icon resource in the `.exe` (a `.syso`):** it needs another build tool. The shortcuts and the window already show the icon; only the `.exe` file in Explorer doesn't. That waits for signed releases.
- **Putting the CLI on the PATH:** NSIS's string limit can cut a long PATH short. The guide gives the CLI's full path instead.

## Consequences

- **Unsigned:** testers see SmartScreen's and Gatekeeper's warnings, and the guide says how to get past them. Signing comes before a wider beta (D-005). Then `!finalize` in the NSIS script and a Developer ID with notarization on macOS replace the ad hoc signature, and the warnings go.
- **No automatic updates:** testers install each new version over the old one.
- **Release builds need a Mac** with Xcode's command-line tools and NSIS. Linux packages wait for the public release (D-003).
- **The Stream Deck plugin's own version** is still `0.1.0.0`, apart from the app's.
