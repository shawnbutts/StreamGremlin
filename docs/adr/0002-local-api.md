# ADR-0002: The local API and the desktop bridge

- **Status:** Accepted
- **Date:** 2026-11-07
- **Based on:** StreamWisp ADR-0008 (API auth) and ADR-0014 (desktop shell)

## Context

The desktop app and the CLI must reach the engine through the same authenticated API (AGENTS.md), with no desktop-only logic. Any web page the streamer opens can also send requests to `127.0.0.1`, so the API has to refuse those. The desktop UI is a webview, and it shouldn't hold a secret it doesn't need.

## Decision

**Listener**
- Loopback only. The default is `127.0.0.1:7430`; if that port is taken, a free loopback port is used. Any address that isn't a loopback IP is refused.
- After it starts listening, the engine writes `<data>/api-addr` (for example `http://127.0.0.1:7430`, mode 0600). It removes the file on a clean stop. After a crash, clients treat a refused connection as "not running".

**Authentication and checks**, applied to every request in this order:
1. Every response carries `StreamGremlin-API-Version: 1`, `X-Content-Type-Options: nosniff` and `Cache-Control: no-store`.
2. A request that sends a different API version gets `400 unsupported_api_version`.
3. The Host header must be `localhost`, `127.0.0.1` or `[::1]`, with any port. This blocks DNS rebinding.
4. A request with an `Origin` header is refused unless that origin is allowed. None are allowed yet: the app's bridge sends no Origin.
5. Bodies are capped at 1 MiB. JSON is decoded strictly: unknown fields and trailing data are errors.
6. `Authorization: Bearer <token>` is compared in constant time. The token is 32 random bytes, base64url-encoded, in `<data>/api-token` (mode 0600), created on first start. Deleting the file and restarting rotates it.
7. Every route declares a permission (`health.read`, `settings.read`, `settings.write`, and more as slices add them). One function checks it. The token holder is the `admin` principal, with every permission. Later roles add principals without touching routes.

`GET /v1/health` is the only public route. It answers `{"status":"ok"}` and nothing else.

**Errors** always look like `{"error":{"code":"…","message":"…"}}`. The stable codes are `unauthorized`, `forbidden`, `not_found`, `method_not_allowed` (with an `Allow` header), `invalid_request`, `payload_too_large`, `unsupported_api_version`, `conflict` and `internal`. Internal errors are logged with detail and answered generically.

**Logging.** The API logs method, path, status and duration at debug level, and never headers or bodies. An acceptance test checks that the token appears in neither the log file nor stderr.

**Every route has a screen.** `internal/api/coverage_test.go` fails when a route isn't called anywhere in the desktop app's source, unless the route has a `CLIOnly` reason.

**Desktop bridge** (`internal/desktop`, no Wails code)
- The webview loads the UI from Wails' in-process asset server. That's a URL scheme, not a socket, so only the webview can reach it.
- Requests to `/v1/*` on that origin are passed in-process to the same API handler. On the way, the bridge:
  - sets Host to `127.0.0.1`;
  - removes the webview's `Origin`, `Referer`, `Cookie` and any `Authorization`;
  - adds the session's bearer token and the version header.

  The UI never sees the token.
- Until the engine is ready, `/v1/*` answers `503` with `engine_starting` or `engine_failed` and a message for the streamer.
- `GET /desktop/info` reports `starting`, `ready` or `failed`, with the reason.
- UI responses carry a strict CSP: `default-src 'none'`, with scripts, styles, images, fonts and connections allowed only from the app itself, and no frames, forms or base changes. The build puts no inline scripts or styles in the page.
- No Wails bindings or Wails events are used, so there is no desktop-only path.

**Separate Go module.** `cmd/desktop` has its own `go.mod`, with a `replace` for the main module. The engine, CLI, tests and lint of the main module never compile Wails or need cgo.

## Consequences

- The CLI and any future client follow exactly the same rules as the app.
- Remote access (for a phone or a moderator) isn't possible until it gets its own design with TLS and roles.
- A live-update stream, needed later for events, can't go through the asset server. It will use a loopback WebSocket with a single-use ticket fetched through the bridge, designed with the event bus slice.

## Alternatives considered

- **Wails bindings for the UI:** that would create a desktop-only path that the CLI can't exercise. Rejected by AGENTS.md.
- **Giving the UI the token:** anything in the webview, such as a compromised dependency, could then read it. The in-process bridge avoids that.
