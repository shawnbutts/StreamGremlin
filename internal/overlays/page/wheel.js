// The wheel overlay (docs/adr/0030-wheels.md): spins to the result the
// engine already drew, and decides nothing. Each wedge is sized to its
// weight, so the wheel shows the real odds; the wheel stops on the drawn
// number itself (each unit of weight is one slot, in order from the top),
// which is always inside the winning wedge. Labels are set with
// textContent. The look comes from the overlay's look; the wedges' colours
// from the wheel, or a palette.

const SVG = 'http://www.w3.org/2000/svg';
const HEX = /^#[0-9a-f]{6}$/i;
const PALETTE = ['#f9e6bd', '#b77a41', '#2a9d8f', '#e9c46a', '#e76f51', '#457b9d', '#8ab17d', '#c77dff'];
// How long the result shows after the wheel stops, the same as the engine's.
export const RESULT_MS = 4000;
// After the wheel stops, a beat before the result shows (with the cheer
// sound), the same as the engine's wait before it announces the result.
export const LAND_MS = 500;
const TURNS = 5;

const clamp = (n, lo, hi, dflt) => {
  const x = Number(n);
  return Number.isFinite(x) ? Math.max(lo, Math.min(hi, Math.round(x))) : dflt;
};

/** Black or white, whichever reads better on a colour. */
export function inkFor(hex) {
  if (!HEX.test(hex)) return '#111111';
  const [r, g, b] = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255);
  const lum = 0.2126 * r + 0.7152 * g + 0.0722 * b;
  return lum > 0.55 ? '#111111' : '#ffffff';
}

/**
 * Where the wheel stops: degrees clockwise from the top to the middle of
 * the drawn number's slot. Returns null when the spin doesn't add up.
 */
export function landing(choices, winner, draw) {
  const weights = choices.map((c) => clamp(c && c.weight, 1, 1000, 1));
  const total = weights.reduce((a, b) => a + b, 0);
  const d = Number(draw);
  if (!Number.isInteger(d) || d < 0 || d >= total) return null;
  // The drawn number must fall in the winner's wedge.
  let start = 0;
  for (let i = 0; i < winner; i++) start += weights[i];
  if (d < start || d >= start + weights[winner]) return null;
  return ((d + 0.5) * 360) / total;
}

function polar(r, deg) {
  const rad = ((deg - 90) * Math.PI) / 180;
  return [200 + r * Math.cos(rad), 200 + r * Math.sin(rad)];
}

function wedgePath(from, to) {
  if (to - from >= 359.999) return 'M 200 10 A 190 190 0 1 1 199.999 10 Z';
  const [x1, y1] = polar(190, from);
  const [x2, y2] = polar(190, to);
  return `M 200 200 L ${x1.toFixed(2)} ${y1.toFixed(2)} A 190 190 0 ${to - from > 180 ? 1 : 0} 1 ${x2.toFixed(2)} ${y2.toFixed(2)} Z`;
}

function svg(doc, tag, attrs = {}) {
  const el = doc.createElementNS(SVG, tag);
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, String(v));
  return el;
}

/** The wheel's drawing: wedges sized by weight, starting at the top. */
export function drawWheel(doc, choices) {
  const weights = choices.map((c) => clamp(c && c.weight, 1, 1000, 1));
  const total = weights.reduce((a, b) => a + b, 0);
  const root = svg(doc, 'svg', { viewBox: '0 0 400 400', class: 'sg-wheel-svg', role: 'img' });
  const rotor = svg(doc, 'g', { class: 'sg-wheel-rotor' });
  let at = 0;
  choices.forEach((c, i) => {
    const span = (weights[i] * 360) / total;
    const color = c && HEX.test(c.color || '') ? c.color : PALETTE[i % PALETTE.length];
    rotor.appendChild(svg(doc, 'path', { d: wedgePath(at, at + span), fill: color, stroke: 'rgba(0,0,0,0.35)', 'stroke-width': 1.5 }));
    // A label along the wedge's middle, if the wedge is wide enough to read.
    if (span >= 6) {
      const mid = at + span / 2;
      const [x, y] = polar(118, mid);
      const label = String((c && c.label) || '');
      const short = label.length > 18 ? `${label.slice(0, 17)}…` : label;
      const text = svg(doc, 'text', {
        x: x.toFixed(2), y: y.toFixed(2), 'text-anchor': 'middle', 'dominant-baseline': 'middle', fill: inkFor(color),
        'font-size': span < 14 ? 10 : span < 30 ? 13 : 16, 'font-weight': 700,
        transform: `rotate(${(mid - 90).toFixed(2)} ${x.toFixed(2)} ${y.toFixed(2)})`,
      });
      text.textContent = short;
      rotor.appendChild(text);
    }
    at += span;
  });
  root.appendChild(svg(doc, 'circle', { cx: 200, cy: 200, r: 194, fill: 'none', stroke: 'var(--sg-wheel-rim, #222)', 'stroke-width': 8 }));
  root.appendChild(rotor);
  root.appendChild(svg(doc, 'circle', { cx: 200, cy: 200, r: 22, class: 'sg-wheel-hub' }));
  return { root, rotor };
}

/**
 * Builds the wheel display in stage. opts: setTimeout and reducedMotion
 * may be replaced in tests.
 */
export function wheelView(doc, stage, opts = {}) {
  const later = opts.setTimeout || ((fn, ms) => setTimeout(fn, ms));
  const reduced = opts.reducedMotion || (() => false);
  // Plays the overlay's sounds: "start" as a spin starts, "end" as it stops, "cheer" as the result shows.
  const cue = opts.cue || (() => {});
  const queue = [];
  const seen = new Set();
  let busy = false;
  let box = null;

  function clear() {
    if (box) box.remove();
    box = null;
  }

  function next() {
    if (busy) return;
    const v = queue.shift();
    if (!v) return;
    const choices = Array.isArray(v.choices) ? v.choices.slice(0, 50) : [];
    const winner = Number(v.winner);
    const land = choices[winner] ? landing(choices, winner, v.draw) : null;
    if (land === null) {
      next(); // a spin that doesn't add up isn't shown, never guessed
      return;
    }
    busy = true;
    clear();
    box = doc.createElement('div');
    box.className = 'sg-wheel';
    const title = doc.createElement('div');
    title.className = 'sg-alert sg-wheel-title in';
    const t = doc.createElement('div');
    t.className = 'sg-title';
    t.textContent = String(v.title || 'Wheel');
    title.appendChild(t);
    const wrap = doc.createElement('div');
    wrap.className = 'sg-wheel-wrap';
    const pointer = doc.createElement('div');
    pointer.className = 'sg-wheel-pointer';
    const { root, rotor } = drawWheel(doc, choices);
    root.setAttribute('aria-label', String(v.title || 'Wheel'));
    wrap.appendChild(root);
    wrap.appendChild(pointer);
    box.appendChild(title);
    box.appendChild(wrap);
    stage.appendChild(box);
    const ms = clamp(v.seconds, 2, 20, 6) * 1000;
    // Clockwise turns, then back by the landing so it sits under the pointer.
    const end = TURNS * 360 + (360 - land);
    if (reduced()) {
      rotor.style.transform = `rotate(${end}deg)`;
      result(choices[winner].label);
      return;
    }
    cue('start');
    rotor.style.transform = 'rotate(0deg)';
    void root.getBoundingClientRect();
    rotor.style.transition = `transform ${ms}ms cubic-bezier(0.12, 0.7, 0.12, 1)`;
    rotor.style.transform = `rotate(${end}deg)`;
    later(() => {
      cue('end'); // as it stops
      later(() => result(choices[winner].label), LAND_MS);
    }, ms);
  }

  function result(label) {
    if (!box) return;
    cue('cheer');
    const r = doc.createElement('div');
    r.className = 'sg-alert sg-wheel-result in';
    const x = doc.createElement('div');
    x.className = 'sg-text';
    x.textContent = `🎉 ${label}`;
    r.appendChild(x);
    box.appendChild(r);
    later(() => {
      clear();
      busy = false;
      next();
    }, RESULT_MS);
  }

  return {
    spin(v) {
      if (!v || typeof v.id !== 'string' || seen.has(v.id)) return;
      seen.add(v.id);
      if (seen.size > 100) seen.delete(seen.values().next().value);
      queue.push(v);
      next();
    },
  };
}
