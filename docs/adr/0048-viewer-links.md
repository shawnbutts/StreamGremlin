# ADR-0048: Linked viewers sharing one points balance

- **Status:** Accepted
- **Date:** 2026-11-14
- **Based on:** D-095 (M6: "linked account share points"), D-101 (slice 6); [ADR-0032](0032-viewers-and-points.md) (viewers and the append-only ledger).

## Context

From M6 slice 6, YouTube chatters are viewers with their own record and points, like Twitch's. Someone who watches on both platforms then has two viewers. The user decided that linked accounts share points. Two questions follow: how a viewer proves two accounts are theirs without a website or a sign-in, and how two balances become one in a ledger that is never changed or deleted.

## Decision

### Proving it in chat

- **`!link NAME`** on one platform names the viewer's account on the other: a Twitch login, or a YouTube @handle. StreamGremlin answers with a 6-digit code from `crypto/rand`.
- **`!link CODE`** must then be typed on the other platform, by the account named, within 10 minutes.
- **The code is said in public chat but is useless to anyone else.** It's bound to the account named, by platform and login (a YouTube login is the handle, lower case, without the @). Anyone reading chat who types it is refused.
- **A YouTube name without a handle** can't type a code, since it has no login to match. It can still start a link, naming its Twitch account.
- **Limits:**
  - asking again replaces the code;
  - a code works once;
  - five wrong codes from one account in 10 minutes stop it trying for a while;
  - at most 1,000 codes wait at once.
- **Codes live in memory only.** A restart forgets them, and the viewer asks again.
- **The streamer** can link any Twitch viewer to any YouTube viewer, and unlink them, from the Viewers screen or `streamgremlin viewers link|unlink`.

### Storage

Migration `0034_viewer_links.sql` adds `viewer_links`, one row per link:

- `id`, `twitch_viewer` and `youtube_viewer`;
- `linked_at`, and `linked_by` (`chat` or who, in the app);
- `unlinked_at` and `unlinked_by`.

Rules on the table:

- **One link per viewer at a time:** partial unique indexes on each side where `unlinked_at IS NULL`.
- **Never deleted:** a trigger refuses deletes.
- **Never changed, except to end:** a trigger refuses updates to anything else, so the record says who shared points when.

### One balance

- **The Twitch viewer holds the shared points.**
  - The ledger resolves whose account a viewer's entries go to: a linked YouTube viewer's go to the Twitch viewer's account; everyone else's to their own.
  - Each transaction still names the viewer it was for, so the history says which account earned or spent.
  - `Balance` and the "not enough points" check use the shared account. `History` lists both accounts' transactions.
- **Linking moves the YouTube viewer's balance across,** in the same database transaction that stores the link: for each currency, an adjustment taking it from the YouTube viewer and one giving it to the Twitch viewer, under operation ids made from the link's id. Nothing is edited. The audit still holds: every transaction sums to zero, and every balance is the sum of its entries.
- **Unlinking only ends the link.** The YouTube viewer's own account was emptied when they linked, so it starts again from 0, and the shared points stay with the Twitch viewer. No ledger entry is needed.
- **Paid once per person:**
  - time in chat pays one operation per holder per interval;
  - gives to groups count holders;
  - the wait between chat awards is per holder.

### Names

- `@name` matches a Twitch login or a YouTube handle.
- When it matches both a Twitch viewer and a different YouTube viewer, and they aren't linked to each other, lookups answer "ambiguous" instead of guessing: `!give`, the CLI and workflow steps say so.
- When the two are linked, the Twitch viewer is used.

### Giveaways

- **Linked accounts enter once:** an entry is refused while the linked account has one, checked again when it's recorded.
- **Unlinked accounts can't be recognised as one person.** The giveaway form says so.

## Alternatives considered

- **Moving each new transaction across as it happens:** twice the entries, and a window where a balance looks wrong. Rejected for resolving the holder.
- **Merging the two viewer rows into one:** loses which platform account did what, can't be undone cleanly, and breaks the `(platform, user_id)` key every record uses.
- **A code with no account named:** anyone reading chat could use it to join their account to someone else's balance. Rejected.
- **Naming each other on both platforms, with no code:** about as safe and as simple. The code keeps what the user approved, and makes the second step clearly deliberate.
- **Splitting the balance on unlinking:** it would need a rule nobody asked for. Recommended to the user and approved: the points stay on Twitch.

## Consequences

- A person's points are one number however they watch. Leaderboards list the Twitch viewer.
- A link can only be undone by the streamer. A viewer who was tricked into typing a code asks the streamer, who unlinks them.
- Forgetting a viewer unlinks them first. The shared points stay on Twitch.
