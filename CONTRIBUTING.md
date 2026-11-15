# Contributing

## Sign your commits (DCO)

Every commit needs a `Signed-off-by` line, which certifies the [Developer Certificate of Origin](https://developercertificate.org) (D-005). `git commit -s` adds it.

## Set up

You need:

- **Go 1.27**
- **Node 22 or 24.** Homebrew's Node 26.13 ships an npm that fails with `ERR_INVALID_PACKAGE_CONFIG`. `brew install node@24` is enough: `make` finds it.
- **On Linux,** the WebKitGTK and GTK development packages Wails v3 needs, to build the desktop app.

```sh
make bootstrap     # pinned tools into ./bin/tools
make verify        # licences, lint, unit tests, acceptance tests, desktop UI checks, desktop build
```

Before a release, and after changing a parser or the event path ([ADR-0050](docs/adr/0050-security-and-load-testing.md)):

```sh
make test-fuzz     # every fuzz target, FUZZTIME each (15s by default)
make test-load     # the chat surge load test, with budgets
make vuln          # govulncheck and npm audit (needs the network)
```

The website ([D-112](docs/DECISIONS.md)):

```sh
make screenshots   # photograph the app with made-up data into website/screenshots (needs Chromium or Chrome)
make site          # build bin/site/www (streamgremlin.com) and bin/site/code (code.streamgremlin.com)
```

Help is one text for the app and the site: `cmd/desktop/frontend/src/help/guide.html`. Change it there. Take the screenshots again after a screen changes. They use the stand-in Twitch and made-up names only, never real stream data.

## Run it

```sh
make desktop-ui desktop
./bin/app/StreamGremlin                    # the app, using your real data folder
STREAMGREMLIN_DATA_DIR=/tmp/sg ./bin/app/StreamGremlin   # or a throwaway one
make build && ./bin/streamgremlin status
```

Set `STREAMGREMLIN_DESKTOP_DEBUG=1` to log every request from the window.

To try Twitch sign-in without a registered Twitch app, run the stand-in:

```sh
make mocktwitch && ./bin/mocktwitch                      # http://127.0.0.1:7441
STREAMGREMLIN_TWITCH_AUTH_URL=http://127.0.0.1:7441 ./bin/app/StreamGremlin
```

Paste any Client ID (ten or more lowercase letters and digits). Sign-in links open the mock's /activate page, where any login can be approved. The Connections screen says "Test mode" the whole time.

To type chat as a viewer, open http://127.0.0.1:7441/chat, set the channel to the login you signed in as, and send. Messages the app sends show there too.

To try OBS without OBS, run `make mockobs && ./bin/mockobs` (ws://127.0.0.1:4456, add `-password x` to need one), and connect to it on the Connections screen. http://127.0.0.1:4456/panel switches scenes by hand, as you would in OBS.

To build with a real Client ID, pass it at build time; never commit it: `make desktop TWITCH_CLIENT_ID=…`.

Tests never touch your real keyring. To check it on purpose, run the one opt-in test, which writes, reads and deletes a single item:

```sh
STREAMGREMLIN_TEST_OS_KEYRING=1 go test ./internal/secrets -run TestRealKeyring -v
```

## Beta installers

On a Mac with Xcode's command-line tools, NSIS (`brew install makensis`) and Podman or Docker:

```sh
make release VERSION=0.1.0-beta.1 TWITCH_CLIENT_ID=...
```

It runs `make verify`, then writes the Windows installer, the macOS disk image, the Linux `.deb`, `.rpm` and `.tar.gz`, and `SHA256SUMS.txt` to `bin/release/`, and checks them (`tests/release`). All are unsigned during the beta ([ADR-0043](docs/adr/0043-beta-installers.md)). Send testers the installer and [docs/BETA.md](docs/BETA.md), which is also inside each as "Read me first.txt".

## Build for Windows from a Mac

The Windows app and CLI cross-compile from macOS (or Linux) with no Windows tools:

```sh
make windows                         # bin/windows/app/StreamGremlin.exe and bin/windows/streamgremlin.exe
make windows WINDOWS_ARCH=arm64      # for Windows on ARM
make windows TWITCH_CLIENT_ID=...    # with the built-in Client ID, as with make desktop
```

Copy the whole `bin/windows` folder to the Windows computer, and run `app\StreamGremlin.exe`. The app and the CLI stay in separate folders because Windows (and macOS) file names ignore case: `StreamGremlin.exe` and `streamgremlin.exe` would be the same file. The app needs Microsoft's WebView2, which Windows 10 and 11 include. The build isn't code-signed yet, so Windows SmartScreen may warn the first time: choose "More info", then "Run anyway".

## Build for Linux

The Linux app links GTK 4 and WebKitGTK 6.0 with cgo, so it's compiled in a container (Ubuntu 24.04, `packaging/linux/Containerfile`) with Podman or Docker, from macOS or Linux. The command line cross-compiles without one.

```sh
make linux                           # bin/linux/amd64/streamgremlin-desktop and streamgremlin
make linux LINUX_ARCH=arm64          # for ARM (fast on an Apple silicon Mac; amd64 runs emulated, slowly)
make release-linux VERSION=... TWITCH_CLIENT_ID=...   # the .deb, .rpm and .tar.gz in bin/release
make linux-smoke VERSION=...         # install them in fresh Ubuntu 24.04, Debian 13 and Fedora 42 and start the app
```

The packages are the same bytes each time they're built from the same commit. Linux is "lightly tested" (D-003): each release gets `make linux-smoke`, not a real stream.

## How work is done

Read [AGENTS.md](AGENTS.md). In short:

- Work in thin slices. Each slice comes with its tests, its screen and its docs.
- `make verify` must pass before every push.
- Adding or updating a Go module: run `make notices`, and add any new module to `THIRD-PARTY-NOTICES.md`. `make verify` fails until both are done.
- Every decision goes in [docs/DECISIONS.md](docs/DECISIONS.md).
