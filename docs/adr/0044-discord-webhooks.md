# ADR-0044: Discord webhooks

- **Status:** Accepted
- **Date:** 2026-11-13
- **Based on:** D-089 (Discord, Tier 1); D-001 (switching streamers first); ADR-0003's secret store; ADR-0041's "sent once, unknown when unanswered" rule.

## Context

Streamers coming from Streamer.bot post to Discord through webhooks: a link Discord makes for one channel, which needs no bot account, no Discord developer application and no live connection. The link's last part is a token: anyone who has it can post to that channel as the webhook.

## Decision

### Storage

- A webhook is saved under the streamer's name for it (`announcements`): 1 to 32 lower-case letters, digits and dashes, up to 20 webhooks.
- Its id and token go in the secret store under `discord/webhook/<name>`, as `id/token`. Settings (`discord`) keep only the name, Discord's id for the webhook, its name in Discord, the channel and server ids, and when it was added.
- The link is taken in once (the app's password field, or the CLI's standard input, never its arguments) and never answered, logged or exported. Backups leave secrets out, as they do for every secret.
- Adding asks Discord first (Get Webhook with Token), so a typo is caught before a stream.

### Protocol

- Only Discord's documented HTTP API v10 webhook endpoints: Get Webhook with Token, and Execute Webhook with `wait=true`, for the message's id.
- Accepted links: `https://` on `discord.com`, `discordapp.com`, `canary.discord.com` or `ptb.discord.com`, path `/api[/vN]/webhooks/{id}/{token}`. Every request goes to `discord.com` (or the test server), whatever host was pasted.
- **Mentions:** `allowed_mentions` is always sent. It's `parse: []` unless the step chooses @everyone or @here, then `parse: ["everyone"]`. Discord's default for webhooks notifies mentioned users, and posts often carry a viewer's name or message.
- **Sent once.** Discord has no idempotency key. A 429 posted nothing, so it's waited for (Discord's `retry_after`, up to 10 seconds) and tried again twice. No answer, or a 5xx, is unknown and never sent again. 404 (10015) and 401 (50027) say the webhook is gone; StreamGremlin doesn't try it again by itself.
- **Limits:** text is cut to Discord's limits with "…", never refused, since a title or message can come from chat.
- `mockdiscord` stands in for both endpoints in tests and standalone (`make mockdiscord`). The engine's `-discord-api-url` points at it, loopback only.

## Consequences

- A leaked data folder doesn't leak webhook tokens beyond what the secret store protects.
- A webhook deleted in Discord makes its workflows fail with "add it again", never silently.
- Editing a posted message, a bot account and Discord-side triggers are not built (Tier 2 in D-089).
