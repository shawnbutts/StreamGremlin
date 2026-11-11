# ADR-0039: Points for plugins: rounds

- **Status:** Accepted
- **Date:** 2026-11-11
- **Based on:** [ADR-0036](0036-packages-plugins-and-themes.md) (a plugin's rounds, refunded when it stops); [ADR-0038](0038-lua-plugins.md) (Lua plugins); [ADR-0032](0032-viewers-and-points.md) (the ledger); StreamWisp's ADR-0054 (plugin wagers) and D-081; D-074.

## Context

The feature cut gives plugins a points API: "award, charge, refund, and escrow for rounds, with odds stated by the plugin". A game in a plugin (dice, a raid, a gamble) has to take a viewer's stake, pay winners and give stakes back when the game can't finish. Plugins are untrusted, and AGENTS.md asks for:
- no double awards;
- balances that never go negative;
- an append-only ledger;
- winners chosen on the server with a sound random source, recorded for audit;
- never implying odds are fair unless verified.

StreamWisp's ADR-0054 settled this with rounds, after weighing a plain give/take permission. A plugin that could credit or debit any balance could drain or inflate them all. A round bounds both, and binds each stake to the viewer whose chat started the call. StreamGremlin keeps that model.

## Decision

### A permission with limits

`points.rounds`, declared with limits in the manifest's `lua.points`:

```json
"points": { "currency": "", "max_stake": 1000, "max_multiplier": 2, "max_round_seconds": 60 }
```

- `currency`: a currency's id, `""` for points. A round in a currency that doesn't exist is refused when it opens.
- `max_stake`: 1 to 1,000,000, per viewer per round.
- `max_multiplier`: 1 to 10, with up to two decimals: a payout is at most the stake times this, rounded down.
- `max_round_seconds`: 1 to 3,600.

The permission and the section come together, as `events.publish` and `events` do. The install check says it in words: "Hold viewers' points in its rounds and pay them out: up to 1,000 points from a viewer a round, paying back at most 2× their stake. Odds are the plugin's own: StreamGremlin can't check them." It's approved like any permission. An update that raises a limit counts as asking for more (D-072): it's asked again.

### What a plugin can do: award, charge and refund, inside rounds

| | |
|---|---|
| `sg.points.balance()` → `n` or `nil, why` | the balance of **the viewer behind the call**, in the plugin's currency |
| `sg.round.open(title, rules [, id])` → `round` or `nil, why` | opens a round. `rules` are the plugin's stated odds, recorded with it (up to 300 characters). An `id`, such as `call.key`, makes a retried step reopen the same round. At most 3 open per plugin |
| `sg.round.stake(round, amount)` → `{viewer, name, stake, balance}` or `nil, why` | **charges** the viewer behind the call `amount` (1 to `max_stake`) into the round, once per viewer per round. Not enough points is refused, saying how many they have |
| `sg.round.roll(round, label, sides)` → `n` or `nil, why` | a roll from 1 to `sides` (2 to 1,000,000), from `crypto/rand`, recorded with the round in order, up to 1,000 a round |
| `sg.round.settle(round, payouts)` → `true` or `nil, why` | **awards** each staked viewer their payout (`{[viewer] = amount}`), 0 to stake × `max_multiplier`; anyone not listed gets 0, their stake spent. The round is closed |
| `sg.round.cancel(round, reason)` → `true` or `nil, why` | **refunds** every stake and closes the round |

Like `sg.chat`, these never raise an error. Each is a host call, recorded in the plugin's activity.

**The viewer behind the call:** the actor of the event that ran the step. A plugin can stake, or read the balance of, only someone who just triggered it, never a viewer it names. A timer has no viewer, so a timer can't stake or read a balance. It can roll, settle and cancel.

**Nothing outside a round:** a plugin can't give points without a stake, or take them without a round that pays or refunds.

### Exactly once, and the ledger as the record

Each ledger operation's id is made from the plugin, the round and the viewer:
- a stake is `plugin:<id>:round:<round>:stake:<viewer>`;
- a payout is `…:pay:<viewer>`;
- a refund is an undo of the stake (ADR-0032's reversal).

A retried step, a repeated host call or a crash halfway can't take or pay twice. Refunds and payouts are worked out from the stakes the ledger actually holds, not from the plugin's word, so a crash between the ledger and the round's own table can't lose a stake. Round operations run one at a time, engine-wide.

**Settling** stores the payouts and marks the round `settling` first, then pays each, then marks it `settled`. A crash in between is finished when StreamGremlin starts: the payouts are applied again, each once.

### Refunded when a round can't finish

Every open round of a plugin is refunded, with the reason recorded:
- when its worker starts: on, restarted after a crash, or after its settings changed. Its memory is gone, so it can't finish the round;
- when it's turned off, fails, is updated, rolled back or removed;
- when the round passes `max_round_seconds`, checked every 5 seconds;
- when StreamGremlin starts, for rounds left open when it stopped.

### Recorded for audit

Migration 0026:
- `plugin_rounds`: id, plugin, version, title, the stated rules, currency, state (`open`, `settling`, `settled`, `refunded`), why it was refunded, opened, closes by, closed, and the payouts;
- `plugin_round_stakes`;
- `plugin_round_rolls`, append-only by triggers.

Rounds outlive the plugin, as its audit does. The ledger has every stake, payout and refund, with the plugin and round in its memo.

### Screens, API and CLI

- **Add-ons:** a plugin with `points.rounds` shows its limits with the permission. Its card has **Rounds**: each recent round with its title, state, the rules "as stated by the plugin", each stake, each roll "made and recorded by StreamGremlin", and each payout or refund. Nothing calls a game fair.
- **API:** `GET /v1/packages/{id}/rounds?limit=` (packages.read).
- **CLI:** `streamgremlin packages rounds ID`.

### The example

Dice tray 1.3.0 adds **Gamble points** (`!gamble 50`, or `!gamble all`). Each gamble is a round: it stakes, rolls 1 to 100, and pays twice the stake at the setting's number or more (60 by default), saying the roll and the result in chat. Its stated rules say exactly that. The `!gamble` template is in its gallery section.

## Alternatives considered

- **`points.give` and `points.take` for plugins:** a plugin could drain or inflate any balance. Rejected, as in StreamWisp.
- **Stakes the plugin can change:** "the difference debited or refunded" (StreamWisp) needs an operation id per change. One stake per viewer per round is enough for the games in view, and simpler to audit.
- **Trusting the round's own table for refunds:** a crash between the ledger and the table could lose a stake. The ledger is the record.
- **Checking a game's odds:** StreamGremlin can't know what a plugin does with its rolls. The rolls are made and recorded by StreamGremlin, the rules are the plugin's words, and the screens say so.

## Consequences

- A plugin with `points.rounds` can take viewers' points to chance, and pay back up to `max_multiplier` × the stake. That's what a betting game is. The limits, the viewer binding, the refunds and the audit are the controls, and the install check says so in plain words.
- One engine-wide lock for round operations: plugins' rounds wait for each other. They're short.
- A round's memory is the plugin's: restarting StreamGremlin mid-round refunds it.
