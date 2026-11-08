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

# Prefer a Homebrew Node 24 or 22 when the default node is another version.
NODE_BIN ?= $(firstword $(wildcard /opt/homebrew/opt/node@24/bin /opt/homebrew/opt/node@22/bin /usr/local/opt/node@24/bin /usr/local/opt/node@22/bin))
ifneq ($(NODE_BIN),)
NPM_ENV := PATH="$(NODE_BIN):$$PATH"
endif
NPM := $(NPM_ENV) npm --prefix $(FRONTEND)

# The desktop app needs cgo on macOS; macOS 13 is the minimum (D-008).
ifeq ($(shell uname -s),Darwin)
DESKTOP_ENV := MACOSX_DEPLOYMENT_TARGET=13.0 CGO_CFLAGS="-mmacosx-version-min=13.0" CGO_LDFLAGS="-mmacosx-version-min=13.0"
endif

.PHONY: mocktwitch help bootstrap fmt lint vet test test-acceptance build node-check desktop-ui desktop-test desktop verify clean

help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "}; {printf "  %-16s %s\n", $$1, $$2}'

bootstrap: $(GOLANGCI_LINT) ## Install pinned tools into ./bin/tools
	$(GO) mod download
	cd $(DESKTOP) && $(GO) mod download

$(GOLANGCI_LINT):
	GOBIN=$(TOOLS) $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

fmt: $(GOLANGCI_LINT) ## Format Go code
	$(GOLANGCI_LINT) fmt ./...

lint: $(GOLANGCI_LINT) ## Lint Go code (includes the format check)
	$(GOLANGCI_LINT) run ./...
	cd $(DESKTOP) && $(GOLANGCI_LINT) run --config ../../.golangci.yml ./...

vet: ## go vet, with the acceptance tests included
	$(GO) vet ./...
	$(GO) vet -tags acceptance ./tests/acceptance

test: ## Unit tests with the race detector
	$(GO) test -race ./...

test-acceptance: ## Acceptance tests through the real binary
	$(GO) test -tags acceptance -count=1 ./tests/acceptance

mocktwitch: ## Build the stand-in for Twitch's sign-in server into ./bin
	CGO_ENABLED=0 $(GO) build -trimpath -o $(BIN)/mocktwitch ./cmd/mocktwitch

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

verify: lint vet test test-acceptance desktop-ui desktop-test desktop build ## Everything, and builds the app and the CLI; run before every push

clean: ## Remove build output
	rm -rf $(BIN)/streamgremlin $(BIN)/app $(BIN)/mocktwitch $(FRONTEND)/dist/assets $(FRONTEND)/dist/index.html
