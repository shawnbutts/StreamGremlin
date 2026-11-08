# ADR-0005: Twitch sign-in

- **Status:** Accepted
- **Date:** 2026-11-08
- **Based on:** StreamWisp ADR-0011 (Twitch authentication), ADR-0057 (bot account) and D-131 (built-in Client ID). Twitch's docs were re-read on 2026-11-08; differences are noted below and in the adapter's manifest (`internal/twitch/manifest.go`).

## Context

Chat (slice 5), and later every Twitch feature, needs a user access token for the streamer. Streamers coming from Streamer.bot expect an optional second "bot" account that sends chat. StreamGremlin is a desktop app given to anyone, so it can't keep a client secret. The tokens are the most sensitive data the app holds.

## Decision

**Client**
- **A public Twitch app, no client secret.** It uses the device code grant flow, which Twitch documents for public clients.
- **Release builds carry StreamGremlin's own Client ID** (registered under StreamGremlinHQ), injected at build time with `make TWITCH_CLIENT_ID=…` into `twitch.BuiltInClientID`. It is never committed.
- **A pasted Client ID overrides it** (Connections screen, `PUT /v1/connections/twitch/client-id`, `streamgremlin twitch client-id`). Development builds and streamers with their own app use it. A Client ID isn't a secret and is kept in settings.
- **Changing the Client ID ends sign-ins made with the old one.**

**Sign-in** (device code flow)
- `POST /v1/connections/twitch/{streamer|bot}/sign-in` starts the flow on the engine and returns only the user code, the verification link and the expiry. The device code never leaves the engine.
- The engine polls `/oauth2/token` at Twitch's interval:
  - `authorization_pending` keeps waiting;
  - `slow_down` (undocumented, handled anyway) adds 5 seconds;
  - network errors keep trying until the code expires;
  - any other answer ends the attempt and shows Twitch's message.
- A new sign-in replaces a pending one. Cancelling is harmless.
- On success, the new token is checked with `/oauth2/validate`: it must belong to our Client ID, and the bot must not be the streamer's account (or the other way round). A refused token is revoked, never saved.
- **An existing sign-in stays in use until a new one succeeds,** and the replaced token is then revoked.
- Scopes: the streamer asks for `user:read:chat user:write:chat`; the bot for `user:write:chat user:read:moderated_channels`. Only what M1 uses; later milestones add theirs. Missing scopes are listed, with "Sign in again".

**Tokens** (ADR-0003)
- **Storage:** one secret per account, `twitch/streamer/oauth` and `twitch/bot/oauth`. Each is a JSON value holding the access and refresh tokens, expiry, scopes, user id, login and Client ID, so a refresh replaces it in one write.
- **Refresh:**
  - An access token is refreshed when less than 10 minutes remain, or when Twitch answers 401.
  - Refreshes run one at a time per account, because a public client's refresh token is single use: two at once would end the sign-in.
  - **The new pair is saved before it is used.** If saving fails, the token works until the app closes, and the state says so.
- **Validation:** at start (in the background, never delaying the engine) and hourly, as Twitch requires.
  - A 401 gets one refresh.
  - A refused refresh (400 or 401; Twitch documents both) ends the sign-in: the secret is deleted, the state becomes `needs_sign_in` with the reason, and `Service.Changes()` wakes everything using it.
  - If Twitch can't be reached, the state is `unknown`. The token is kept, never shown as valid, never dropped.
- **Sign-out** revokes the token at Twitch (best effort, so it also works offline) and deletes the secret.
- **What's visible:** no API answer, log line or error message contains a token. Errors from Twitch are reduced to their message, without the request.

**States** shown to the streamer: `not_configured` (no Client ID), `signed_out`, `signing_in` (with the code), `connected` (Twitch confirmed it recently), `needs_sign_in` (with why), and `unknown` (couldn't ask Twitch). Only `connected` counts as signed in.

**Opening Twitch in the browser**
- The app's `POST /desktop/open-url` opens a page in the system browser, never inside the app.
- It opens only allow-listed pages:
  - `https://www.twitch.tv/activate`;
  - `https://dev.twitch.tv/console…` and `/docs/…`;
  - a loopback test server's `/activate`.

**Testing without Twitch**
- `internal/mocktwitch` implements the documented endpoints, plus controls for tests: approve or deny a code, expire access tokens, disconnect a user, go down.
- `cmd/mocktwitch` runs it standalone, with an `/activate` page that stands in for twitch.tv.
- The engine uses a test server only through `-twitch-auth-url`, or `STREAMGREMLIN_TWITCH_AUTH_URL` for the desktop app. It must be a loopback `http://IP:port`, and the Connections screen then says "Test mode".
- Acceptance tests point every engine at a dead local port unless they start their own mock, so tests never reach the real Twitch.

## Alternatives considered

- **Authorization code flow with a localhost redirect:** needs a client secret, or a free local port and a browser on the same machine. A distributed app can't keep a secret.
- **Implicit flow:** no refresh token, so the streamer would sign in again every few hours.
- **App access token for the Chat Bot badge:** needs a client secret (recorded as unsupported in the manifest).
- **Requesting every scope later milestones need now:** saves a second sign-in later, but asks streamers to grant permissions nothing uses yet.

## Consequences

- Twitch's docs say a public client's refresh token expires 30 days after it's generated in one place, and after 30 days of inactivity in another. Each refresh issues a new one, so an app in regular use may never hit it. The app plans for signing in again, and shows why when it happens.
- A crash between receiving a refreshed pair and saving it loses the sign-in. That's unavoidable with single-use refresh tokens; the window is one local write.
- The bot's own messages and its moderator status matter from slice 5 (chat).

## Open questions

- The registered app's exact console settings (client type, redirect URL) are checked when the user registers it, then recorded here.
