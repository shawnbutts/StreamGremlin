# StreamGremlin developer entry points. `make verify` runs every check.
# Needs Go and Node 22 or 24 (Homebrew's Node 26.13 ships a broken npm).
# Compatible with GNU Make 3.81 (macOS default).

GO       ?= go
BIN      := $(CURDIR)/bin
TOOLS    := $(BIN)/tools
DESKTOP  := cmd/desktop
FRONTEND := $(DESKTOP)/frontend

GOLANGCI_LINT_VERSION := v2.14.0
GOLANGCI_LINT := $(TOOLS)/golangci-lint
GOVULNCHECK_VERSION := v1.9.0
GOVULNCHECK := $(TOOLS)/govulncheck
NFPM_VERSION := v2.48.0
NFPM := $(TOOLS)/nfpm
# How long each fuzz target runs in `make test-fuzz`.
FUZZTIME ?= 15s

VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -buildid= \
	-X code.streamgremlin.com/streamgremlin/internal/buildinfo.Version=$(VERSION) \
	-X code.streamgremlin.com/streamgremlin/internal/buildinfo.Commit=$(COMMIT)
# StreamGremlin's registered Twitch app, from the environment at build time.
# Never commit it (AGENTS.md). Without it, the app asks for a pasted one.
ifneq ($(TWITCH_CLIENT_ID),)
LDFLAGS += -X code.streamgremlin.com/streamgremlin/internal/twitch.BuiltInClientID=$(TWITCH_CLIENT_ID)
endif
# StreamGremlin's own Google project, once Google approves it (D-095).
# Never commit them. Without them, streamers use their own project.
ifneq ($(YOUTUBE_CLIENT_ID),)
LDFLAGS += -X code.streamgremlin.com/streamgremlin/internal/youtube.BuiltInClientID=$(YOUTUBE_CLIENT_ID)
LDFLAGS += -X code.streamgremlin.com/streamgremlin/internal/youtube.BuiltInClientSecret=$(YOUTUBE_CLIENT_SECRET)
endif

# Prefer a Homebrew Node 24 or 22 when the default node is another version.
NODE_BIN ?= $(firstword $(wildcard /opt/homebrew/opt/node@24/bin /opt/homebrew/opt/node@22/bin /usr/local/opt/node@24/bin /usr/local/opt/node@22/bin))
ifneq ($(NODE_BIN),)
NPM_ENV := PATH="$(NODE_BIN):$$PATH"
endif
NPM := $(NPM_ENV) npm --prefix $(FRONTEND)
STREAMDECK := streamdeck
NPM_DECK := $(NPM_ENV) npm --prefix $(STREAMDECK)

# The desktop app needs cgo on macOS; macOS 13 is the minimum (D-008).
ifeq ($(shell uname -s),Darwin)
DESKTOP_ENV := MACOSX_DEPLOYMENT_TARGET=13.0 CGO_CFLAGS="-mmacosx-version-min=13.0" CGO_LDFLAGS="-mmacosx-version-min=13.0"
endif

.PHONY: linux linux-smoke release-linux release-sums release-fetch release-version site screenshots test-fuzz test-load vuln release release-check release-windows release-macos notices notices-check windows streamdeck mockobs mocktwitch mockdiscord mockyoutube help bootstrap fmt lint vet test test-acceptance build node-check desktop-ui desktop-test desktop verify clean

help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "}; {printf "  %-16s %s\n", $$1, $$2}'

bootstrap: $(GOLANGCI_LINT) $(GOVULNCHECK) ## Install pinned tools into ./bin/tools
	$(GO) mod download
	cd $(DESKTOP) && $(GO) mod download

$(GOLANGCI_LINT):
	GOBIN=$(TOOLS) $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

$(GOVULNCHECK):
	GOBIN=$(TOOLS) $(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

$(NFPM):
	GOBIN=$(TOOLS) $(GO) install github.com/goreleaser/nfpm/v2/cmd/nfpm@$(NFPM_VERSION)

fmt: $(GOLANGCI_LINT) ## Format Go code
	$(GOLANGCI_LINT) fmt ./...

lint: $(GOLANGCI_LINT) ## Lint Go code (includes the format check)
	$(GOLANGCI_LINT) run ./...
	$(GOLANGCI_LINT) run --build-tags release ./tests/release
	cd $(DESKTOP) && $(GOLANGCI_LINT) run --config ../../.golangci.yml ./...

vet: ## go vet, with the acceptance tests included
	$(GO) vet ./...
	$(GO) vet -tags acceptance ./tests/acceptance
	$(GO) vet -tags 'acceptance load' ./tests/acceptance
	$(GO) vet -tags release ./tests/release

test: ## Unit tests with the race detector
	$(GO) test -race ./...

test-acceptance: ## Acceptance tests through the real binary
	$(GO) test -tags acceptance -count=1 ./tests/acceptance

test-fuzz: ## Run every fuzz target for FUZZTIME each (default 15s)
	GO=$(GO) FUZZTIME=$(FUZZTIME) ./scripts/fuzz.sh

test-load: ## The chat surge load test through the real binary, with budgets
	$(GO) test -tags 'acceptance load' -count=1 -run TestLoad -v ./tests/acceptance

screenshots: desktop-ui ## Photograph the app with made-up data for the website (needs Chromium or Chrome; CHROME=path)
	$(GO) run ./cmd/screenshots -out website/screenshots

site: ## Build the website into ./bin/site (www and code); offers the newest installers in ./bin/release, linked to GitHub
	$(GO) run ./cmd/site -out bin/site

# GitHub builds releases (D-118); the site needs their sizes and SHA-256.
GITHUB_REPO := shawnbutts/StreamGremlin
release-fetch: ## Download a release's files from GitHub into ./bin/release, for make site (VERSION=...; needs gh, and gh auth login for a draft)
	@test "$(VERSION)" != dev || { echo "Set the version: make release-fetch VERSION=0.1.0-beta.5"; exit 1; }
	gh release download v$(VERSION) --repo $(GITHUB_REPO) --dir $(RELEASE) --clobber
	cd $(RELEASE) && shasum -a 256 -c SHA256SUMS.txt

vuln: $(GOVULNCHECK) ## Known vulnerabilities in code that's called, and in npm packages (needs the network)
	$(GOVULNCHECK) ./...
	cd $(DESKTOP) && $(GOVULNCHECK) ./...
	$(NPM) audit --audit-level=moderate
	$(NPM_DECK) audit --audit-level=moderate

mockobs: ## Build the stand-in for OBS's WebSocket server into ./bin
	CGO_ENABLED=0 $(GO) build -trimpath -o $(BIN)/mockobs ./cmd/mockobs

mocktwitch: ## Build the stand-in for Twitch's sign-in server into ./bin
	CGO_ENABLED=0 $(GO) build -trimpath -o $(BIN)/mocktwitch ./cmd/mocktwitch

mockdiscord: ## Build the stand-in for Discord's webhooks into ./bin
	CGO_ENABLED=0 $(GO) build -trimpath -o $(BIN)/mockdiscord ./cmd/mockdiscord

mockyoutube: ## Build the stand-in for Google's sign-in and YouTube's API into ./bin
	CGO_ENABLED=0 $(GO) build -trimpath -o $(BIN)/mockyoutube ./cmd/mockyoutube

build: ## Build the CLI into ./bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)/streamgremlin ./cmd/streamgremlin

node-check:
	@$(NPM_ENV) node -e 'const m=+process.versions.node.split(".")[0]; if (m!==22 && m!==24) { console.error("Node " + process.versions.node + " found; use Node 22 or 24 (set NODE_BIN)."); process.exit(1) }'

desktop-ui: node-check ## Install, check, test and build the desktop UI
	$(NPM) ci --no-audit --no-fund
	$(NPM) run check
	$(NPM) test
	$(NPM) run build

desktop-test: ## Vet and test the desktop module
	cd $(DESKTOP) && $(DESKTOP_ENV) $(GO) vet ./... && $(DESKTOP_ENV) $(GO) test ./...

# The app goes in its own folder: macOS and Windows file names ignore case,
# so bin/StreamGremlin would be the same file as the CLI's bin/streamgremlin.
desktop: ## Build the desktop app into ./bin/app (run desktop-ui first)
	cd $(DESKTOP) && $(DESKTOP_ENV) $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)/app/StreamGremlin .

# Windows builds cross-compile from macOS or Linux: no cgo, no Windows tools.
# Like on macOS, the app has its own folder: Windows file names ignore case.
# The app needs Microsoft's WebView2, which Windows 10 and 11 include.
WINDOWS_ARCH ?= amd64
windows: desktop-ui ## Build the Windows app and CLI into ./bin/windows (WINDOWS_ARCH=arm64 for ARM)
	cd $(DESKTOP) && GOOS=windows GOARCH=$(WINDOWS_ARCH) CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS) -H windowsgui" -o $(BIN)/windows/app/StreamGremlin.exe .
	GOOS=windows GOARCH=$(WINDOWS_ARCH) CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)/windows/streamgremlin.exe ./cmd/streamgremlin
	@echo "Copy the bin/windows folder to the Windows computer: app\StreamGremlin.exe is the app, streamgremlin.exe the command line."

# Linux builds (D-114) compile the app in a container, since it links GTK 4
# and WebKitGTK 6.0 with cgo: Ubuntu 24.04, so it runs on Ubuntu 24.04,
# Debian 13 and Fedora 42 or newer (packaging/linux/Containerfile). Needs
# Podman or Docker; another architecture runs under emulation, slowly. The
# command line needs no cgo and cross-compiles here.
CONTAINER ?= $(firstword $(shell command -v podman docker 2>/dev/null))
LINUX_ARCH ?= amd64
GO_VERSION := $(patsubst go%,%,$(shell $(GO) env GOVERSION))
LINUX_IMAGE := streamgremlin-linux-build:$(GO_VERSION)-$(LINUX_ARCH)
linux: desktop-ui ## Build the Linux app and CLI into ./bin/linux/ARCH in a container (LINUX_ARCH=arm64 for ARM; needs Podman or Docker)
	@test -n "$(CONTAINER)" || { echo "make linux needs Podman or Docker."; exit 1; }
	mkdir -p $(BIN)/linux/$(LINUX_ARCH)
	$(CONTAINER) build -q --platform linux/$(LINUX_ARCH) --build-arg GO_VERSION=$(GO_VERSION) -t $(LINUX_IMAGE) packaging/linux
	$(CONTAINER) run --rm --platform linux/$(LINUX_ARCH) -v "$(CURDIR)":/src -v "$$($(GO) env GOMODCACHE)":/go/pkg/mod \
		-v streamgremlin-gocache-$(LINUX_ARCH):/root/.cache/go-build -w /src/$(DESKTOP) \
		-e GOFLAGS=-mod=readonly -e GOTOOLCHAIN=local -e LDFLAGS='$(LDFLAGS)' $(LINUX_IMAGE) \
		sh -c 'go build -trimpath -ldflags "$$LDFLAGS" -o /src/bin/linux/$(LINUX_ARCH)/streamgremlin-desktop .'
	GOOS=linux GOARCH=$(LINUX_ARCH) CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)/linux/$(LINUX_ARCH)/streamgremlin ./cmd/streamgremlin
	@echo "bin/linux/$(LINUX_ARCH)/streamgremlin-desktop is the app, streamgremlin the command line."

# The Stream Deck plugin (M7 slice 5, ADR-0042). Its tests run the plugin
# against the CLI just built and a stand-in Stream Deck; the installer is
# bin/streamdeck/com.streamgremlin.streamdeck.streamDeckPlugin, for macOS
# and Windows alike.
streamdeck: node-check build ## Check, test and pack the Stream Deck plugin into ./bin/streamdeck
	$(NPM_DECK) ci --no-audit --no-fund
	$(NPM_DECK) run check
	$(NPM_DECK) run build
	cd $(STREAMDECK) && STREAMGREMLIN_BIN=$(BIN)/streamgremlin $(NPM_ENV) npm test
	$(NPM_DECK) run validate
	$(NPM_DECK) run pack

# Beta and release installers (D-084), built on macOS:
#   make release VERSION=0.1.0-beta.1 TWITCH_CLIENT_ID=...
# writes bin/release/StreamGremlin-VERSION-windows-x64-setup.exe (NSIS:
# brew install makensis), StreamGremlin-VERSION-macos.dmg (a universal app),
# the Linux packages, a CycloneDX SBOM beside each (FILE.cdx.json, D-117)
# and SHA256SUMS.txt, then checks them (tests/release). All are unsigned
# during the beta. GitHub builds the same on a version tag (D-118).
RELEASE := $(BIN)/release
MAC_STAGE := $(RELEASE)/macos-stage
MAC_APP := $(MAC_STAGE)/StreamGremlin.app
MAC_TMP := $(RELEASE)/macos-build
WIN_STAGE := $(RELEASE)/windows-stage
MAC_CGO := MACOSX_DEPLOYMENT_TARGET=13.0 CGO_ENABLED=1 CGO_CFLAGS="-mmacosx-version-min=13.0" CGO_LDFLAGS="-mmacosx-version-min=13.0"
ICONS := brand-kit/streamgremlin-icon
# Builds are dated by the commit, so a rebuild is the same bytes.
SOURCE_DATE_EPOCH ?= $(shell git log -1 --format=%ct 2>/dev/null || echo 0)
# What the UI and Stream Deck builds bundled, for the SBOMs.
SBOM_UI := $(BIN)/sbom/desktop-ui.json
SBOM_DECK := $(BIN)/sbom/streamdeck.json
SBOM := $(GO) run ./cmd/release sbom
WIN_OUT := $(RELEASE)/StreamGremlin-$(VERSION)-windows-x64-setup.exe
MAC_OUT := $(RELEASE)/StreamGremlin-$(VERSION)-macos.dmg
DECK_PLUGIN := com.streamgremlin.streamdeck.streamDeckPlugin

release-version:
	@test -n "$(TWITCH_CLIENT_ID)" || { echo "Set TWITCH_CLIENT_ID: release builds sign in with StreamGremlin's own Twitch app (D-005)."; exit 1; }
	@test "$(VERSION)" != dev || { echo "Set the version: make release VERSION=0.1.0-beta.1 TWITCH_CLIENT_ID=..."; exit 1; }
	@$(GO) run ./cmd/release winversion "$(VERSION)" >/dev/null

release-check: release-version
	@test "$$(uname -s)" = Darwin || { echo "Release builds need macOS, for the dmg."; exit 1; }
	@command -v makensis >/dev/null || { echo "Release builds need NSIS: brew install makensis"; exit 1; }

release: release-check verify release-windows release-macos release-linux release-sums ## Beta installers for Windows, macOS and Linux into ./bin/release (VERSION=0.1.0-beta.1 TWITCH_CLIENT_ID=...)
	@echo "Installers in $(RELEASE): send testers the .exe, .dmg, .deb, .rpm or .tar.gz and docs/BETA.md."

# Checksums of every download and SBOM, then the checks of tests/release.
release-sums: release-version
	cd $(RELEASE) && shasum -a 256 StreamGremlin-$(VERSION)-* > SHA256SUMS.txt
	RELEASE_DIR=$(RELEASE) RELEASE_VERSION=$(VERSION) $(GO) test -tags release -count=1 ./tests/release

release-windows: release-check windows streamdeck
	rm -rf $(WIN_STAGE) && mkdir -p $(WIN_STAGE)/cli "$(WIN_STAGE)/Stream Deck"
	cp $(BIN)/windows/app/StreamGremlin.exe $(WIN_STAGE)/
	cp $(BIN)/windows/streamgremlin.exe $(WIN_STAGE)/cli/
	cp $(BIN)/streamdeck/$(DECK_PLUGIN) "$(WIN_STAGE)/Stream Deck/"
	cp docs/BETA.md "$(WIN_STAGE)/Read me first.txt"
	cp LICENSE $(WIN_STAGE)/LICENSE.txt
	cp THIRD-PARTY-NOTICES.md THIRD-PARTY-LICENSES.txt $(WIN_STAGE)/
	$(GO) run ./cmd/release ico $(WIN_STAGE)/StreamGremlin.ico $(ICONS)-16.png $(ICONS)-32.png $(ICONS)-48.png $(ICONS)-64.png $(ICONS)-128.png $(ICONS)-256.png
	makensis -V2 -DVERSION=$(VERSION) -DWINVERSION=$$($(GO) run ./cmd/release winversion $(VERSION)) \
		-DSRC=$(WIN_STAGE) -DOUT=$(WIN_OUT) packaging/windows/StreamGremlin.nsi
	$(SBOM) $(WIN_OUT).cdx.json $(WIN_OUT) $(VERSION) windows $(SOURCE_DATE_EPOCH) \
		go:StreamGremlin.exe=$(WIN_STAGE)/StreamGremlin.exe npm:StreamGremlin.exe=$(SBOM_UI) \
		go:cli/streamgremlin.exe=$(WIN_STAGE)/cli/streamgremlin.exe \
		"file:Stream Deck/$(DECK_PLUGIN)=$(WIN_STAGE)/Stream Deck/$(DECK_PLUGIN)" "npm:Stream Deck/$(DECK_PLUGIN)=$(SBOM_DECK)"

# A universal app (Apple silicon and Intel), with the command line in
# Contents/Helpers: in Contents/MacOS, streamgremlin and StreamGremlin would
# be the same file. Signed ad hoc only, which a downloaded app needs to open
# at all; the beta guide says how to allow it.
release-macos: release-check desktop-ui streamdeck
	rm -rf $(MAC_STAGE) $(MAC_TMP)
	mkdir -p $(MAC_APP)/Contents/MacOS $(MAC_APP)/Contents/Helpers $(MAC_APP)/Contents/Resources $(MAC_TMP)/StreamGremlin.iconset
	cd $(DESKTOP) && $(MAC_CGO) GOARCH=arm64 CC="clang -arch arm64" $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(MAC_TMP)/app-arm64 .
	cd $(DESKTOP) && $(MAC_CGO) GOARCH=amd64 CC="clang -arch x86_64" $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(MAC_TMP)/app-amd64 .
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(MAC_TMP)/cli-arm64 ./cmd/streamgremlin
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(MAC_TMP)/cli-amd64 ./cmd/streamgremlin
	lipo -create -output $(MAC_APP)/Contents/MacOS/StreamGremlin $(MAC_TMP)/app-arm64 $(MAC_TMP)/app-amd64
	lipo -create -output $(MAC_APP)/Contents/Helpers/streamgremlin $(MAC_TMP)/cli-arm64 $(MAC_TMP)/cli-amd64
	$(GO) run ./cmd/release plist $(VERSION) $(MAC_APP)/Contents/Info.plist
	for s in 16 32 128 256 512; do \
		cp $(ICONS)-$$s.png $(MAC_TMP)/StreamGremlin.iconset/icon_$${s}x$${s}.png && \
		cp $(ICONS)-$$((s * 2)).png $(MAC_TMP)/StreamGremlin.iconset/icon_$${s}x$${s}@2x.png || exit 1; \
	done
	iconutil -c icns -o $(MAC_APP)/Contents/Resources/StreamGremlin.icns $(MAC_TMP)/StreamGremlin.iconset
	cp LICENSE THIRD-PARTY-NOTICES.md THIRD-PARTY-LICENSES.txt $(MAC_APP)/Contents/Resources/
	codesign --force --sign - $(MAC_APP)/Contents/Helpers/streamgremlin
	codesign --force --sign - $(MAC_APP)
	ln -s /Applications $(MAC_STAGE)/Applications
	cp $(BIN)/streamdeck/$(DECK_PLUGIN) $(MAC_STAGE)/
	cp docs/BETA.md "$(MAC_STAGE)/Read me first.txt"
	hdiutil create -quiet -ov -volname "StreamGremlin $(VERSION)" -srcfolder $(MAC_STAGE) -fs HFS+ -format UDZO \
		-o $(MAC_OUT)
	$(SBOM) $(MAC_OUT).cdx.json $(MAC_OUT) $(VERSION) macos $(SOURCE_DATE_EPOCH) \
		go:StreamGremlin.app/Contents/MacOS/StreamGremlin=$(MAC_APP)/Contents/MacOS/StreamGremlin \
		npm:StreamGremlin.app/Contents/MacOS/StreamGremlin=$(SBOM_UI) \
		go:StreamGremlin.app/Contents/Helpers/streamgremlin=$(MAC_APP)/Contents/Helpers/streamgremlin \
		file:$(DECK_PLUGIN)=$(MAC_STAGE)/$(DECK_PLUGIN) npm:$(DECK_PLUGIN)=$(SBOM_DECK)

# Linux packages (D-114): a .deb, an .rpm and a .tar.gz for LINUX_ARCH,
# from the same files, dated by the commit so a rebuild is the same bytes.
LINUX_NAME := $(if $(filter amd64,$(LINUX_ARCH)),x64,$(LINUX_ARCH))
LINUX_STAGE := $(RELEASE)/linux-stage-$(LINUX_ARCH)
LINUX_OUT := $(RELEASE)/StreamGremlin-$(VERSION)-linux-$(LINUX_NAME)
release-linux: release-version linux $(NFPM)
	rm -rf $(LINUX_STAGE) && mkdir -p $(LINUX_STAGE)/doc
	cp $(BIN)/linux/$(LINUX_ARCH)/streamgremlin-desktop $(BIN)/linux/$(LINUX_ARCH)/streamgremlin $(LINUX_STAGE)/
	cp packaging/linux/com.streamgremlin.StreamGremlin.desktop $(LINUX_STAGE)/
	for s in 16 32 48 64 128 256 512; do \
		mkdir -p $(LINUX_STAGE)/icons/$${s}x$${s}/apps && \
		cp $(ICONS)-$$s.png $(LINUX_STAGE)/icons/$${s}x$${s}/apps/com.streamgremlin.StreamGremlin.png || exit 1; \
	done
	cp docs/BETA.md $(LINUX_STAGE)/doc/README.md
	cp LICENSE THIRD-PARTY-NOTICES.md THIRD-PARTY-LICENSES.txt $(LINUX_STAGE)/doc/
	$(GO) run ./cmd/release stamp $(LINUX_STAGE) $(SOURCE_DATE_EPOCH)
	for p in deb rpm; do \
		(cd $(LINUX_STAGE) && ARCH=$(LINUX_ARCH) VERSION=$(VERSION) SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH) \
			$(NFPM) package -f $(CURDIR)/packaging/linux/nfpm.yaml -p $$p -t $(LINUX_OUT).$$p) || exit 1; \
	done
	$(GO) run ./cmd/release tarball $(LINUX_OUT).tar.gz $(LINUX_STAGE) StreamGremlin-$(VERSION)-linux-$(LINUX_NAME) $(SOURCE_DATE_EPOCH)
	for p in deb:deb:usr/bin rpm:rpm:usr/bin linux:tar.gz:StreamGremlin-$(VERSION)-linux-$(LINUX_NAME); do \
		kind=$${p%%:*} && rest=$${p#*:} && ext=$${rest%%:*} && at=$${rest#*:} && \
		$(SBOM) $(LINUX_OUT).$$ext.cdx.json $(LINUX_OUT).$$ext $(VERSION) $$kind $(SOURCE_DATE_EPOCH) \
			go:$$at/streamgremlin-desktop=$(LINUX_STAGE)/streamgremlin-desktop npm:$$at/streamgremlin-desktop=$(SBOM_UI) \
			go:$$at/streamgremlin=$(LINUX_STAGE)/streamgremlin || exit 1; \
	done

# Installs the Linux packages in fresh containers of the oldest systems
# they're for and starts the app (packaging/linux/smoke.sh).
LINUX_SMOKE := ubuntu:24.04=deb debian:trixie=deb fedora:42=rpm
linux-smoke: ## Install the Linux packages in fresh Ubuntu, Debian and Fedora containers and start the app (VERSION=...)
	@test "$(VERSION)" != dev || { echo "Set the version of the packages in bin/release: make linux-smoke VERSION=0.1.0-beta.1"; exit 1; }
	for pair in $(LINUX_SMOKE); do \
		image=$${pair%=*} && ext=$${pair#*=} && echo "== $$image" && \
		$(CONTAINER) run --rm --platform linux/$(LINUX_ARCH) -v "$(RELEASE)":/pkgs:ro -v "$(CURDIR)/packaging/linux/smoke.sh":/smoke.sh:ro \
			docker.io/library/$$image sh /smoke.sh /pkgs/StreamGremlin-$(VERSION)-linux-$(LINUX_NAME).$$ext || exit 1; \
	done

# THIRD-PARTY-LICENSES.txt holds the licence of every Go module built into
# the app and CLI, on every platform; THIRD-PARTY-NOTICES.md must name each.
notices: ## Rewrite THIRD-PARTY-LICENSES.txt after Go modules change
	$(GO) run ./cmd/notices

notices-check:
	$(GO) run ./cmd/notices -check

verify: notices-check lint vet test test-acceptance desktop-ui desktop-test desktop build streamdeck ## Everything, and builds the app, the CLI and the Stream Deck plugin; run before every push

clean: ## Remove build output
	rm -rf $(BIN)/streamgremlin $(BIN)/app $(BIN)/windows $(BIN)/linux $(BIN)/sbom $(BIN)/streamdeck $(BIN)/release $(BIN)/mocktwitch $(BIN)/mockdiscord $(BIN)/mockobs $(FRONTEND)/dist/assets $(FRONTEND)/dist/index.html
