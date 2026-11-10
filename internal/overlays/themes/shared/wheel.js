// Wheel geometry for the built-in looks: the same rules as the Classic
// wheel (internal/overlays/page/wheel.js). Each wedge is sized to its
// weight, starting at the top and going clockwise, so the wheel shows the
// real odds. The wheel stops on the drawn number itself: each unit of
// weight is one slot, and the drawn number's slot is always inside the
// winner's wedge. A spin that doesn't add up is refused, never guessed.

export const SVG = 'http://www.w3.org/2000/svg';
export const HEX = /^#[0-9a-f]{6}$/i;
export const TURNS = 5;
export const MAX_CHOICES = 50;

export const clamp = (n, lo, hi, dflt) => {
  const x = Number(n);
  return Number.isFinite(x) ? Math.max(lo, Math.min(hi, Math.round(x))) : dflt;
};

export const weightsOf = (choices) => choices.map((c) => clamp(c && c.weight, 1, 1000, 1));

/** Each choice's chance, as people read it: "25%", "8.3%", "<0.1%". */
export function chances(choices) {
  const w = weightsOf(choices);
  const total = w.reduce((a, b) => a + b, 0);
  return w.map((x) => {
    const p = (x * 100) / total;
    if (p < 0.1) return '<0.1%';
    return `${p >= 10 ? Math.round(p) : Math.round(p * 10) / 10}%`;
  });
}

/** Degrees clockwise from the top to the middle of the drawn slot, or null. */
export function landing(choices, winner, draw) {
  const weights = weightsOf(choices);
  const total = weights.reduce((a, b) => a + b, 0);
  const d = Number(draw);
  const i = Number(winner);
  if (!Number.isInteger(i) || i < 0 || i >= choices.length) return null;
  if (!Number.isInteger(d) || d < 0 || d >= total) return null;
  let start = 0;
  for (let k = 0; k < i; k++) start += weights[k];
  if (d < start || d >= start + weights[i]) return null;
  return ((d + 0.5) * 360) / total;
}

/** Each wedge's start and end, in degrees clockwise from the top. */
export function spans(choices) {
  const weights = weightsOf(choices);
  const total = weights.reduce((a, b) => a + b, 0);
  let at = 0;
  return weights.map((w) => {
    const from = at;
    at += (w * 360) / total;
    return [from, at];
  });
}

/** Black or white, whichever reads better on a colour. */
export function inkFor(hex) {
  if (!HEX.test(hex || '')) return '#111111';
  const [r, g, b] = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255);
  return 0.2126 * r + 0.7152 * g + 0.0722 * b > 0.55 ? '#111111' : '#ffffff';
}

export function polar(cx, r, deg) {
  const rad = ((deg - 90) * Math.PI) / 180;
  return [cx + r * Math.cos(rad), cx + r * Math.sin(rad)];
}

export function wedgePath(cx, r, from, to) {
  if (to - from >= 359.999) return `M ${cx} ${cx - r} A ${r} ${r} 0 1 1 ${cx - 0.001} ${cx - r} Z`;
  const [x1, y1] = polar(cx, r, from);
  const [x2, y2] = polar(cx, r, to);
  return `M ${cx} ${cx} L ${x1.toFixed(2)} ${y1.toFixed(2)} A ${r} ${r} 0 ${to - from > 180 ? 1 : 0} 1 ${x2.toFixed(2)} ${y2.toFixed(2)} Z`;
}

export function el(doc, tag, attrs = {}) {
  const e = doc.createElementNS(SVG, tag);
  for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, String(v));
  return e;
}

/** A choice list from a message: labels as text, at most 50. */
export function cleanChoices(raw) {
  if (!Array.isArray(raw)) return [];
  return raw.slice(0, MAX_CHOICES).map((c) => ({
    label: String((c && c.label) || '').slice(0, 120),
    weight: clamp(c && c.weight, 1, 1000, 1),
    color: c && HEX.test(c.color || '') ? c.color : '',
  }));
}

/** A label short enough for its wedge, or "" when the wedge is too thin. */
export function shortLabel(label, span) {
  if (span < 6) return '';
  const max = span < 14 ? 10 : span < 30 ? 14 : 18;
  return label.length > max ? `${label.slice(0, max - 1)}…` : label;
}
