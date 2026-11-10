# ADR-0030: Wheels

- **Status:** Accepted
- **Date:** 2026-11-10
- **Based on:** StreamWisp ADR-0021 (wheels and audited spins), with D-034 there, and D-048 here. Changed:
  - no cost or points prizes yet (the points ledger is F4);
  - a wheel is named, not numbered;
  - spins are shown one after another, and settle on a fixed time rather than waiting for the page;
  - the look comes from the wheel concept the user kept in `tmp/streamgremlin-theme-starter`, rebuilt in StreamGremlin's code.

## Context

The user asked for a dedicated wheel feature (D-048): a wheel of choices (the next game, a challenge, a mystery pick) spun on stream. AGENTS.md:
- results are drawn on the server with a cryptographically sound random source and recorded for audit;
- odds are never implied to be fair unless they are;
- the overlay never decides.

The concept's wheels gave every wedge the same size whatever its weight, which its own docs flag as misleading.

## Decision

### Wheels

- **A wheel has:**
  - a name (like `next-game`, which workflows and the CLI use);
  - a title;
  - a spin time (2 to 20 seconds, default 6);
  - 2 to 50 choices, each with a label (1 to 60 characters), a weight (1 to 1000; 2 is twice as likely as 1) and an optional `#rrggbb` colour.
- **A change makes a new version.** An unchanged save doesn't.
- **Up to 100 wheels.** Deleting a wheel keeps its spins.
- **Odds are exact** (weight over total), shown by the screen as you type, by `GET /v1/wheels`, by the CLI, and by the "Say a wheel's odds" step as text for chat.

### Spins (migration 0019)

- **Drawn and recorded at once:**
  - `draw = crypto/rand` in `[0, total)`;
  - each choice holds its weight's worth of numbers, in order; a test checks every number;
  - `wheel_spins` keeps the wheel's title, version and choices as they were, the total, the number drawn, the winner and its label, who started it, and when.
- **Triggers:** the record never changes; a spin settles once; nothing is deleted.
- **A request key makes a spin safe to repeat:** the API's `request_id`, or the step's idempotency key. The same key returns the first spin, and the same key for another wheel is refused.
- **Shown, then settled:**
  - spins queue, at most 20 waiting, and play one after another;
  - each is sent to every wheel overlay (a third kind of overlay, 0019 rebuilds the overlays table) and to the app's previews;
  - with a wheel overlay open in OBS, the spin settles after its spin time plus half a second, as the wheel stops ("shown"); with none, at once ("no_audience");
  - a fixed time, not the page's answer, so a stuck page can't hold it.
- **Recovered:** a spin a stop left unsettled settles when StreamGremlin starts again ("recovered"). The result stands.
- **Events:**
  - `wheels.spin.started` carries no result, so a workflow can't spoil it;
  - `wheels.spin.settled` carries the label, the choice, the number drawn and how it was shown, with the viewer who started it as actor;
  - triggers "A wheel stops" (by wheel name) and, under More, "A wheel starts spinning".
- **`Verify` and "Check the spin"** (the app checks it itself) recompute the winner from the number and the recorded choices. The randomness can't be replayed.

### The wheel overlay

- **Wedges are sized to their weights,** starting at the top, so the wheel shows the real odds.
- **The wheel stops on the drawn number itself:** the middle of that number's slot, which is always inside the winner's wedge. A spin whose number isn't in its winner's wedge isn't shown.
- **Labels are text** (`textContent`), shortened in narrow wedges, and left off wedges too thin to read.
- **The look** comes from the overlay's look: font, title colour, panels. The wedges' colours come from the wheel, or a palette, with black or white ink for contrast.
- **Timing:** it turns five times and slows to a stop over the spin time, then shows "🎉 result" for 4 seconds and clears. Reduced motion shows it stopped at once.

### Steps, screen, API, CLI

- **Steps:**
  - "Spin a wheel" (External: a test event never spins) waits until the wheel stops and gives `label`, `choice`, `spin_id` and `display`, so a chat command or channel point reward can spin and reply with the result;
  - "Say a wheel's odds" (under More) gives `text`.
- **A template, "Spin a wheel from chat":** `!spin` (mods and you) spins the wheel named when it's added, and says where it landed. A test event skips the spin, so the reply says it was a test.
- **The Wheels screen** (under More… by default):
  - wheels with their odds table;
  - Spin, Edit, Delete (asks first);
  - a form with choices, weights, optional colours and the chance worked out as you type;
  - recent spins, the result hidden until the wheel stops, and Check the spin;
  - a note when no wheel overlay is open.
- **API** (`wheels.read`, `wheels.write`): `GET/POST /v1/wheels`, `PUT/DELETE /v1/wheels/{name}`, `POST /v1/wheels/{name}/spin` (202, or 200 for a repeat), `GET /v1/spins/{id}`.
- **CLI:**
  - `wheels`;
  - `wheels add|set NAME -title T -choice LABEL[:WEIGHT[:#COLOR]] … [-seconds N]`;
  - `wheels delete`;
  - `wheels spin` (waits);
  - `wheels verify SPIN`;
  - `overlays add -wheel`.

## Alternatives considered

- **Equal wedges (the concept's):** they misstate weighted odds.
- **Waiting for the page's answer before settling:** a hung page would hold the result. A fixed time is enough.
- **Imported, scripted wheel themes:** they would run third-party code in OBS. That's a decision of its own, alongside content packs.
- **Costs and points prizes:** these come with the points ledger (F4), which can charge, award and refund once.

## Consequences

- **A wheel step waits** up to its spin time (and the spins queued before it) when a wheel overlay is open. Workflows allow 5 minutes.
- **Spins outlive their wheels,** and keep the version they were spun from.
