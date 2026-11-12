# ADR-0042: Paired clients, and how the Stream Deck plugin pairs

- **Status:** Accepted
- **Date:** 2026-11-12
- **Based on:** D-075 (our own Stream Deck plugin); [ADR-0002](0002-local-api.md) (the local API, one admin token); [ADR-0040](0040-buttons.md) (buttons, and `buttons.press` kept apart for the Stream Deck). StreamWisp's ADR-0008 planned principals beyond admin without building them.

## Context

The Stream Deck plugin (M7 slice 5) is a separate program: a Node.js process that the Stream Deck app runs. It has to reach the engine's API on `127.0.0.1` to list and press buttons. Until now the API knew one caller, the admin token in `<data>/api-token`, which can do everything.

What Elgato's current SDK (3.x, docs read 2026-11-12) means for this design:
- **The plugin is an ordinary local Node process** with no documented network limits. Its built-in `fetch` sends no `Origin` header, so the API's Origin check (ADR-0002) doesn't get in its way.
- **Its settings page** (the "property inspector") is a Chromium web view, whose origin isn't documented. It talks to the plugin with `sendToPlugin` and `sendToPropertyInspector`.
- **Storing a token:** Elgato says to keep a user's token in the plugin's global settings, "stored securely on the user's local machine. However, as these are stored locally, users can access them." It doesn't say they're encrypted, and SDK 3 removed the old secrets API. So a token the plugin keeps should be treated as readable by anything running as the user.

Handing the plugin the admin token would give a file anyone can read the power to:
- change workflows;
- moderate;
- read every record;
- send chat.

## Decision

### A paired client has its own token, limited to its kind

`internal/clients` and migration `0030_api_clients.sql` add an `api_clients` table:
- an id (`cl_` and 16 hex digits);
- a kind (only `stream_deck`);
- the name the plugin gave;
- the **SHA-256 of its token** (the token itself is never stored);
- when it was paired, and when it was last used.

There are up to 10 paired clients.

**The API's principals.** A bearer token is checked as the admin token first, in constant time. If it isn't that, its hash is looked up among paired clients (kept in memory and changed as clients are paired or forgotten). A paired client's principal holds only its kind's permissions:

| Kind | Permissions |
|---|---|
| `stream_deck` | `self.read`, `buttons.read`, `buttons.press` |

A Stream Deck can't:
- change buttons;
- read workflows, events, viewers, settings, secrets or the engine's status (which names the data folder);
- moderate, send chat, or pair anything.

**A press from a Stream Deck token is always `from: stream_deck`,** whatever the body says. Any other caller asking for `stream_deck` is refused, as before (ADR-0040).

`GET /v1/whoami` (`self.read`) tells a token what it is: `admin`, or the client's kind, name, id and permissions. The plugin uses it to show "Connected as …".

### Pairing: a code shown in the app, typed into the plugin

1. **Starting:** the streamer clicks **Pair a Stream Deck** on Connections (or runs `streamgremlin streamdeck pair`). The engine makes a one-time code: 8 characters from `23456789ABCDEFGHJKLMNPQRSTUVWXYZ`, shown as `K7QX-M3PA`. That's 40 bits from `crypto/rand`, with no 0, 1, I or O to confuse.
2. **The code's limits:**
   - it works **once**, for **5 minutes**;
   - **5 wrong codes end the pairing**;
   - starting again replaces it, and **Stop pairing** ends it;
   - it lives only in memory, so a restart ends it.
3. **Typing it in:** the streamer types the code into the plugin's settings. The plugin sends `POST /v1/pair {code, name, request_id}`. It's the only route besides `/v1/health` that needs no token, and it answers the new client and its token **once**.
4. **Retries:** a retry with the same `request_id` gets the same answer until the code would have run out, so a dropped answer doesn't strand the plugin. Any other second use finds the code spent.
5. **Typing slack:** the code is accepted in any case, with or without the dash or spaces.

Pairing goes from the app to the plugin, never the other way: there's no request a program can make to start a pairing. An unprompted program would have to guess a 40-bit code in 5 tries, while the streamer is pairing.

`GET /v1/clients/pairing` reports the state:
- `none`;
- `waiting`, with the code;
- `paired`, with the client;
- `expired`;
- `locked`.

It also gives the address the engine listens on, which the app shows next to the code.

### Seeing and forgetting

- **Connections → Stream Deck** lists paired decks with when each was paired and last used. Last used is written at most once a minute.
- **Forget…** asks first, then deletes the client. Its token is refused from the next request.
- **CLI:** `streamgremlin streamdeck`, `streamdeck pair [-wait=false] [-json]` and `streamdeck forget ID`.
- **API:** `GET /v1/clients`, `DELETE /v1/clients/{id}`, `GET`/`POST`/`DELETE /v1/clients/pairing` (`clients.read`, `clients.write`; admin only).

### Logs

- **Logged:** pairing started; a client paired (id, kind, name); a wrong code (count only); a client forgotten.
- **Never logged:** the code or a token. An acceptance test checks both against the engine's log and stderr.

### For the plugin (slice 5)

- **Who calls the API:** the plugin's Node process does, never its settings page. The page sends the typed code to the plugin with `sendToPlugin`.
- **Where to keep the token:** in the plugin's global settings, never action settings. Elgato documents action settings as plain text and included in profile exports.
- **Finding the engine:** first at `http://127.0.0.1:7430`, then at the address in `<data>/api-addr` for the default data folder. A field holds the address shown on Connections, when neither works.
- **A refused token:** a 401 means "Pair again", never a silent failure.

## Alternatives considered

- **Give the plugin the admin token:** it would sit in a file anyone can read, and could do everything. Rejected.
- **Let the plugin start a pairing, with the app approving it:** any local program could pop up pairing requests, and a streamer might approve one without comparing codes. Typing a code the app shows needs no approve button to get wrong.
- **Store tokens in the clear in the database:** a backup or a copied database would carry working tokens. A hash checks just as well.
- **The OS keyring in the plugin:** Elgato offers no API for it. It would need a native Node module in the plugin, which Stream Deck 6.4 refuses (`--no-addons`). Narrow permissions and Forget limit the harm of a readable token instead.
- **Remote access (a phone, another computer):** still loopback only (ADR-0002). Remote needs TLS and its own design.

## Consequences

- The API now has principals other than admin, as ADR-0002 foresaw: one function checks permissions for both.
- **New permissions:** `clients.read`, `clients.write` and `self.read`.
- **Public routes:** exactly `GET /v1/health` and `POST /v1/pair`, checked by a test.
- **A future kind** (a phone remote, say) adds a row to the permission table and a value to the kind check; no route changes.
- **What a stolen Stream Deck token can do:** press your buttons until you forget it. Connections shows when each deck was last used, so you can spot that.
