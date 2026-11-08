# ADR-0013: Timers

- **Status:** Accepted
- **Date:** 2026-11-08
- **Based on:** StreamWisp ADR-0060 (timers), ADR-0061 (cron schedules and `timers.set_state`) and ADR-0070 (timers that work as Streamer.bot's, D-096 there). Adapted as noted below.

## Context

M2 starts with F1: chat commands, timers, counters and quotes (D-004, D-006). Streamers coming from Streamer.bot use its Timed Actions for reminders: socials every 15 minutes, a hydration nudge, a nightly message.

**Streamer.bot's docs** (checked 2026-11-08: Guide → Timers, the Timed Actions trigger, Set Timer State, the Cron trigger):
- **Settings:** a timer has Enabled, Repeat, Name, Interval (seconds), and Random, which "fires randomly between x and y seconds".
- **Lines:** "If lines is greater than 0, then x many chat messages need to be posted before the Timed Action fires … If one requirement is fulfilled, it will wait for the other." The docs add that Lines doesn't work with several platforms connected.
- **Repeat:** unchecked, the timer "will just run once and disable itself afterwards".
- **Trigger variables:** `%counter%` ("how many times the timer has fired"), `%timerId%` and `%timerName%`.
- **Set Timer State:** Enabled, Disabled or Toggle.
- **The Cron trigger** takes a cron expression (example `0 0 * * *`).
- **Not documented:** what happens on restart, or while offline.

StreamWisp built all of this and then brought it in line with Streamer.bot (ADR-0070).

## Decision

### A timer (`internal/timers`, migration `0008_timers.sql`)

| Field | Meaning |
|---|---|
| `id` | lowercase letters, digits, `-` and `_`, up to 64; workflows find the timer by it |
| `name` | up to 100 characters |
| `enabled` | on or off |
| `every_seconds` | 10 seconds to 24 hours |
| `up_to_seconds` | optional: each turn picks a time at random between `every_seconds` and this |
| `cron` | instead of an interval: five fields in the computer's local time |
| `chat_lines` | 0 to 1000 |
| `live_only` | skip turns unless OBS says it's streaming |
| `once` | turn off after going off on schedule |
| `counter` | how many times it has gone off |

- **Cron:**
  - `*`, numbers, `a-b`, `/n`, lists, month and weekday names, and `@hourly`, `@daily`, `@weekly`, `@monthly` and `@yearly`.
  - 0 and 7 are both Sunday. When both day fields are restricted, either matches (classic cron).
  - A schedule that matches no date within five years never fires, and the screen says so.
  - The parser is about 150 lines with tests; no dependency.
- **Up to 200 timers.**

### Going off

- **The schedule lives in memory.** A timer starts counting when the engine starts, when it's saved with a change, or when it's turned on. A restart never fires missed turns in a burst. Saving it unchanged keeps its place.
- **Chat lines wait** (as Streamer.bot): when its time comes and chat is short of lines, the timer stays due and goes off on the first tick (once a second) after they arrive. Its next turn counts from then.
- **"Only while live" skips:** a turn that comes while OBS isn't streaming passes. Waiting would fire every live-only timer at once when the stream started. "Live" is the newest real `broadcast.stream.started` or `stopped` event. When OBS has never said, the timer counts as not live: unknown is never treated as confirmed.
- **Each fire, Run now included,** adds one to the saved count, then records `timers.timer.fired`:
  - **source:** `timers`, confirmed;
  - **payload:** `timer_id`, `name`, `counter`, `manual`, `chat_lines`, and `every_seconds` or `cron`;
  - **its source event id** is the timer's id, when it was made, and the count. A timer deleted and made again under the same id starts a new series.
- **Workflows match** `where: {timer_id: …}`. The editor offers "A timer goes off", with a timer picker and the values `{timer}` and `{counter}`.
- **Run once:** after going off on schedule, the timer turns itself off. Run now doesn't turn it off, since Run now is for trying the timer out.
- **Run now** fires at once, even when the timer is off. Its workflows run for real (it's a real fire, not a test event), and its schedule is unchanged. Each request is another fire: nothing makes repeating one safe, and the button is a deliberate action.
- **Test events** of `timers.timer.fired` (Events screen, Test on a workflow) run workflows as tests, as for every event.

### Turning a timer on or off from a workflow

- **The step** `timers.set_state {timer, state: on | off | toggle}` ("Turn a timer on or off"), in a new Timers group.
- **The timer must be written out** (literal), so chat can never choose which timer.
- **Turning a timer on** starts its count again.
- **For a test event,** the step only says what it would do.
- **Toggle isn't safe to repeat,** and says so.

### API, CLI and screen

- **API**, with new permissions `timers.read` and `timers.write`:
  - `GET /v1/timers`: every timer with `next_at`; `waiting` (`off`, `chat` with `lines` seen so far, or `never`); and `live`.
  - `PUT /v1/timers/{id}`: create or replace. The count is kept; only a new timer takes `counter`, so an import can carry Streamer.bot's count over.
  - `DELETE /v1/timers/{id}`.
  - `POST /v1/timers/{id}/fire`: Run now.
- **CLI:** `streamgremlin timers [-json]`, `timers set ID -name … -every 15m [-up-to] [-cron] [-lines] [-live] [-once] [-off]`, and `timers run | on | off | delete ID`.
- **The Timers screen** (Ctrl/⌘+5):
  - **The list:** each timer in words ("Every 10 min to 15 min, at random, after 5 chat lines from viewers, only while you're live"), with when it goes off next, how many times it has gone off, and which workflows it starts.
  - **On each timer:** an On switch, Run now, Edit and Delete. Delete uses the app's dialog and names the workflows that are kept.
  - **The form:** "Every so often" (a number and a unit, and an optional "At random, up to") or "On a schedule" (with examples to click), plus chat lines, only while live, run once, and on.
  - **"Make a workflow"** opens the editor on a new workflow for a timer that has none, with the timer chosen and its chat message left to fill in.

## Differences from StreamWisp and Streamer.bot

- **Chat lines count viewers' messages only.** Not counted:
  - test events;
  - the streamer's own account's messages, which include what StreamGremlin sends as the streamer when no bot is signed in;
  - the bot's messages (never stored).

  Without this, a timer's own reminder counted towards its next turn. The acceptance test found this. Streamer.bot's docs only say "chat messages", and the point of the setting is not talking into a quiet chat.
- **No random extra time on cron schedules** (StreamWisp had "plus up to"). Streamer.bot's Cron trigger has none either; it can be added if asked.
- **"Live" comes from OBS, and since ADR-0015 from Twitch too:** the newest of OBS starting or stopping the stream and Twitch saying the channel is online or offline.
- **No settings-file export yet:** there's no configuration export in StreamGremlin. Backups include timers, since they're in the database.

## Alternatives considered

- **Keeping each timer's next turn across restarts:** after downtime it would either fire at once or need catch-up rules. Starting the count again is what streamers expect.
- **Waking timers on each chat message instead of a one-second tick:** under a second faster, at the cost of tying timers to the event bus.
- **A "wait or skip" choice for chat lines:** Streamer.bot waits, and nobody asked for skipping.
- **`repeat` (default on) instead of `once`:** a request without the field would decode as a one-off timer. `once` makes the safe default the zero value.
- **Timers as a workflow trigger setting with no timer object:** Streamer.bot users think in timers they can turn on and off, and several actions can share one, so a timer is its own thing.

## Consequences

- **A timer is saved on every fire:** one UPDATE.
- **Daylight saving:** cron schedules use the computer's time zone, so a change can skip or repeat an hour's run, as in classic cron.
- **Deleting a timer keeps the workflows that use it;** they just never start from it again. The screen says which they are before you delete.
- **The Streamer.bot import (M4)** maps Timed Actions to timers: Repeat off becomes `once`, and the counter carries over.
