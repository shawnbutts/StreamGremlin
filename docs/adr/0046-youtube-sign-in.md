# ADR-0046: YouTube sign-in and quota

- **Status:** Accepted
- **Date:** 2026-11-13
- **Based on:** D-095 (M6, YouTube; each streamer's own Google project first); D-096 (slice 1); ADR-0005 (Twitch sign-in), whose states and storage this follows; ADR-0003's secret store.

## Context

YouTube's Data API is reached with Google's OAuth 2.0. The scope chat and moderating need, `youtube.force-ssl` (or the broader `youtube`), is sensitive: a Google project asking for it from people other than its owners needs Google's verification, and the Data API's quota (10,000 units a day by default) belongs to the project, shared by everyone signing in through it. So until StreamGremlin's own project is verified and given more quota, each streamer signs in through a Google project of their own (D-095).

Twitch's sign-in (ADR-0005) uses the device code flow. Google's device flow only allows `youtube` and `youtube.readonly`, and its client type ("TVs and Limited Input devices") isn't what Google recommends for desktop apps.

## Decision

### Protocol

- **Google's flow for desktop apps** (Desktop app client type): the app opens Google's authorization page in the system browser; Google returns to a one-time loopback address, `http://127.0.0.1:<random port>/`, with a code.
- **PKCE (S256) and a random state** on every sign-in. The loopback address answers only a GET to `/` with the right state (compared in constant time), handles the code once, and closes when the sign-in finishes, is cancelled, is replaced or times out (10 minutes). Its page is static text with `Content-Security-Policy: default-src 'none'`.
- **Scope:** `https://www.googleapis.com/auth/youtube.force-ssl` for both accounts. `access_type=offline` and `prompt=select_account consent`, so each sign-in can pick an account or brand account and gets a refresh token.
- **The client secret** Google gives Desktop app clients is optional in Google's token requests and isn't truly secret (an installed app can't keep one), but it's kept like one: sent when given, never shown.
- **Which channel:** after the code is swapped, `channels.list?part=snippet&mine=true` (1 unit) names the channel. A Google account without a channel is refused, and the two accounts can't be the same channel.
- **Revoking ends Google's whole grant** for that account and project. So a refused sign-in of a channel already signed in here isn't revoked (it would sign the other account out), and re-signing the same channel doesn't revoke the old token.
- **Checking:** access tokens last about an hour. A sign-in is refreshed at start, every 50 minutes, and 10 minutes before use when it's about to expire; a refresh is the check that the sign-in still works, and costs no quota. `invalid_grant` ends it (needs signing in again): access was removed, or a project in Testing reached Google's 7 days. `invalid_client` keeps it as unknown, since fixing the client ID or secret brings it back.
- Only `accounts.google.com/o/oauth2/v2/auth`, `oauth2.googleapis.com/token` and `/revoke`, and `www.googleapis.com/youtube/v3`. `mockyoutube` stands in for all of them under one loopback base (`-youtube-url`, `STREAMGREMLIN_YOUTUBE_URL`), and the app says it's a test server.

### Storage

- Settings `youtube`: the streamer's client ID (not secret). Secret store `youtube/client-secret`: its client secret.
- Secret store `youtube/streamer/oauth` and `youtube/bot/oauth`: one JSON value each (access and refresh token, expiry, scopes, channel, client ID), replaced in one write.
- Settings `youtube.quota`: the Pacific day, the units counted, and whether Google refused for quota.
- A build may carry StreamGremlin's own project (`BuiltInClientID` and `BuiltInClientSecret` by `-ldflags`, from `YOUTUBE_CLIENT_ID` and `YOUTUBE_CLIENT_SECRET`), never committed. The streamer's own wins.

### Quota

- Every Data API call counts its documented cost before it's sent (Google counts failed calls too). The count resets at midnight Pacific time.
- Google doesn't tell an app what its project has used, so the count is StreamGremlin's estimate, and says so.
- `quotaExceeded` or `dailyLimitExceeded` from Google marks the day spent: no Data API call is sent until the reset, and the app says why.

## Consequences

- Streamers set up a Google project once (the in-app guide), until StreamGremlin's is verified. Their quota is their own.
- A project left in Testing signs out every 7 days; the guide says to publish it, and the message when it happens says so.
- The engine opens a listening port for a sign-in only while one is pending, on loopback only.
- The same states as Twitch's mean the app, CLI and later slices treat both platforms alike.
