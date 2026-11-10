// Wheels in the app (docs/adr/0030-wheels.md): the same check as
// `streamgremlin wheels verify`, so the streamer needn't take the engine's
// word for a spin, and the odds as the engine computes them.
import type { WheelChoice, WheelSpin } from './api';

const weightOf = (c: WheelChoice) => Math.max(1, Math.round(Number(c.weight) || 1));

export const total = (cs: WheelChoice[]) => cs.reduce((n, c) => n + weightOf(c), 0);

/** The choice a drawn number lands on: each holds its weight's worth, in order. -1 when out of range. */
export function winnerOf(cs: WheelChoice[], draw: number): number {
  if (!Number.isInteger(draw) || draw < 0) return -1;
  let d = draw;
  for (let i = 0; i < cs.length; i++) {
    d -= weightOf(cs[i]);
    if (d < 0) return i;
  }
  return -1;
}

/** Each choice's chance, in percent, for the form as it's typed. */
export function percents(cs: WheelChoice[]): number[] {
  const t = total(cs);
  return cs.map((c) => (t ? (100 * weightOf(c)) / t : 0));
}

export const percentWords = (p: number) => `${p.toFixed(1).replace(/\.0$/, '')}%`;

/** What doesn't hold in a spin's record, or nothing. */
export function verifySpin(sp: WheelSpin): string[] {
  const t = total(sp.choices);
  if (t !== sp.total) return [`The wheel’s weights add up to ${t}, not the ${sp.total} recorded.`];
  if (sp.draw < 0 || sp.draw >= t) return [`The number drawn, ${sp.draw}, isn’t below ${t}.`];
  const w = winnerOf(sp.choices, sp.draw);
  if (w !== sp.winner || sp.choices[w]?.label !== sp.label) {
    return [`Number ${sp.draw} lands on ${sp.choices[w]?.label ?? '?'}, not ${sp.label}.`];
  }
  return [];
}

/** A spin in words: the number drawn and the wheel's total. */
export const howSpun = (sp: WheelSpin) => `number ${sp.draw + 1} of ${sp.total}, wheel version ${sp.wheel_version}`;
