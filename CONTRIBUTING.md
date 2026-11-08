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
make verify        # lint, unit tests, acceptance tests, desktop UI checks, desktop build
```

## Run it

```sh
make desktop-ui desktop
./bin/StreamGremlin                    # the app, using your real data folder
STREAMGREMLIN_DATA_DIR=/tmp/sg ./bin/StreamGremlin   # or a throwaway one
make build && ./bin/streamgremlin status
```

Set `STREAMGREMLIN_DESKTOP_DEBUG=1` to log every request from the window.

Tests never touch your real keyring. To check it on purpose, run the one opt-in test, which writes, reads and deletes a single item:

```sh
STREAMGREMLIN_TEST_OS_KEYRING=1 go test ./internal/secrets -run TestRealKeyring -v
```

## How work is done

Read [AGENTS.md](AGENTS.md). In short:

- Work in thin slices. Each slice comes with its tests, its screen and its docs.
- `make verify` must pass before every push.
- Every decision goes in [docs/DECISIONS.md](docs/DECISIONS.md).
