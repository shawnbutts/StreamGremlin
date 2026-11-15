# ADR-0051: Release builds on GitHub, and an SBOM for each download

- **Status:** Accepted
- **Date:** 2026-11-15
- **Based on:** D-117 (SBOMs), D-118 (release builds on GitHub). StreamWisp's ADR-0052 was read for the design; nothing was copied.

## Context

The wider beta (D-110) goes to testers the user hasn't met, from a public repository (D-115). They need to know a download is what the code says it is, and what it contains. Until now, installers were built on the user's Mac, and only checksums described them.

## Decision

### SBOMs

Each download gets a CycloneDX 1.6 JSON SBOM beside it, `FILE.cdx.json`, made by `go run ./cmd/release sbom` from `internal/release/sbom.go`. The command takes the download, the version, the kind of system and the build's time. Then come its parts:

- `go:NAME=FILE` for a Go program at NAME in the download;
- `file:NAME=FILE` for any other file;
- `npm:NAME=LIST` for npm packages bundled into NAME.

The SBOM's metadata names the download: its file name, SHA-256 and address on GitHub. Its components are:

| Component | bom-ref | From |
|---|---|---|
| each part | `part:NAME` | its SHA-256; for Go, GOOS, GOARCH, CGO_ENABLED and the commit, from the build record |
| Go's standard library | `pkg:golang/stdlib@VERSION` | the build record |
| each Go module | its purl | the build record, replacements followed, StreamGremlin's own modules left out |
| each npm package | its purl | `bin/sbom/*.json`, written by `packaging/sbom/bundled.mjs` from the modules the bundler included |
| each vendored part | its purl | listed in the bundler's config (the Nerd Fonts symbols) |
| each system need | `system:NAME` | a fixed list per system, `required` or `optional`, with `streamgremlin:shipped=false` |

The dependencies say the download holds its parts and needs the system's components, and each part holds its modules and packages.

The output is deterministic. The timestamp is `SOURCE_DATE_EPOCH`. The serial number is a UUID (version 5 layout) made from the SHA-256 of the rest of the SBOM. Components are sorted.

### Release builds

`.github/workflows/release.yml` runs on a pushed tag `v*`, on github.com only.

| Job | Runner | Does |
|---|---|---|
| `macos` | macos-15 | `make verify`, `make release-windows release-macos` |
| `linux` | ubuntu-24.04 | `make release-linux`, `make linux-smoke`, with Docker |
| `publish` | macos-15 | `make release-sums`, then a draft GitHub release with every file |

Its supply chain is kept short:

- only `actions/checkout`, `setup-go`, `setup-node`, `upload-artifact` and `download-artifact`, pinned by commit;
- Go: the newest patch of the version go.mod names, for its security fixes;
- Node 24;
- nfpm and the container's base images as the Makefile pins them;
- no caches.

Permissions are none by default. The build jobs read the repository; `publish` may also write its contents. Secrets reach only the steps that build or check the Client ID.

## Consequences

- Anyone can check a download against `SHA256SUMS.txt`, see what's inside it, and scan it for known vulnerabilities with any CycloneDX tool.
- A release no longer needs the user's Mac, but the user still publishes it.
- The installers aren't signed, and neither are the checksums or SBOMs (D-110). Signing, and GitHub's build provenance attestations, come with code signing.
- A Go module's licence isn't in the SBOM; `THIRD-PARTY-LICENSES.txt` has them all.
