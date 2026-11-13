# Flip clock

An example of an installed StreamGremlin clock and timer look (D-092,
docs/adr/0036-packages-plugins-and-themes.md). To try it, compress this
folder into a ZIP (Finder: Compress; Explorer: Send to, Compressed folder)
and install it on the **Add-ons** screen, or install `flip-clock.zip`
beside it. Then add a **Clock and timers** overlay on the Overlays screen
and pick **Flip clock** under Look.

It shows a clock, a countdown or a stopwatch as split-flap cards that flip
when a digit changes. StreamGremlin keeps the countdown or stopwatch; the
look only draws where it is. It runs in a sandbox with no network, and if
it fails, the overlay shows Classic instead.

What a look is told, and may say back (protocol 1):

- `timer.snapshot` `{settings, reducedMotion, timer}` once it's said it's ready,
  and `timer.updated` `{timer}` on each change, where `timer` is
  `{mode, label, endText, clock: {hour24, seconds, date}, lengthMs, valueMs,
  running, direction, atMs}`: the value was `valueMs` at `atMs` (this
  computer's clock, in milliseconds) and moves by `direction` (-1 down, 1
  up, 0 still) from there.
- `theme.settings.updated` `{settings}` when the streamer changes them.
- It says `theme.ready`, and `theme.error` if it can't draw.
