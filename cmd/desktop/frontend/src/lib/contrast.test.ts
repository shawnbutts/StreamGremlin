// The brand kit's semantic colours must stay readable in every theme
// (WCAG 2.2: 4.5:1 for text, 3:1 for focus rings and large text).
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

// Vitest runs from the frontend folder; the brand kit is at the repository root.
const css = readFileSync(resolve(process.cwd(), '../../../brand-kit/streamgremlin.css'), 'utf8');

function themeTokens(theme: string): Record<string, string> {
  const selector = theme === 'dark' ? ':root[data-theme="dark"]' : `:root[data-theme="${theme}"]`;
  const start = css.indexOf(selector);
  const block = css.slice(start, css.indexOf('}', start));
  const tokens: Record<string, string> = {};
  for (const m of block.matchAll(/--sg-([a-z-]+):\s*(#[0-9a-fA-F]{6})/g)) tokens[m[1]] = m[2];
  return tokens;
}

function luminance(hex: string): number {
  const [r, g, b] = [1, 3, 5].map((i) => {
    const c = parseInt(hex.slice(i, i + 2), 16) / 255;
    return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function ratio(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

// [foreground, background, minimum] pairs the app actually uses.
const pairs: [string, string, number][] = [
  ['foreground', 'background', 4.5],
  ['foreground', 'surface', 4.5],
  ['foreground', 'surface-raised', 4.5],
  ['foreground', 'surface-hover', 4.5],
  ['muted', 'surface', 4.5],
  ['muted', 'surface-raised', 4.5],
  ['primary-text', 'primary', 4.5],
  ['danger', 'surface', 4.5],
  ['danger', 'surface-raised', 4.5],
  ['primary-text', 'primary-hover', 4.5],
  ['success', 'surface', 3],
  ['warning', 'surface', 3],
  ['focus', 'background', 3],
  ['focus', 'surface', 3],
];

describe.each(['dark', 'light', 'midnight'])('%s theme', (theme) => {
  const t = themeTokens(theme);
  it.each(pairs)('%s on %s is at least %s:1', (fg, bg, min) => {
    expect(t[fg], `--sg-${fg}`).toBeDefined();
    expect(t[bg], `--sg-${bg}`).toBeDefined();
    expect(ratio(t[fg], t[bg])).toBeGreaterThanOrEqual(min);
  });
});

describe('cursors', () => {
  it('never shows the busy cursor on a merely disabled button', () => {
    const css = readFileSync(resolve(process.cwd(), 'src/app.css'), 'utf8');
    const rule = css.match(/\.button:disabled,\s*\.sg-button:disabled\s*\{([^}]*)\}/);
    expect(rule?.[1]).toContain('not-allowed');
    expect(rule?.[1]).not.toContain('progress');
  });
});

// Live's buttons are filled with their colour (D-076), with dark text and
// icons: readable on every colour, in every theme, since the fill is the
// same in all of them.
describe('button colours', () => {
  const live = readFileSync(resolve(process.cwd(), 'src/screens/Live.svelte'), 'utf8');
  const colours = [...live.matchAll(/\[data-color='([a-z]+)'\]\s*\{\s*--live-color:\s*(#[0-9a-fA-F]{6})/g)].map((m) => [m[1], m[2]]);
  const ink = live.match(/--live-ink:\s*(#[0-9a-fA-F]{6})/)?.[1];

  it('has all eight, and one ink for them', () => {
    expect(colours.map((c) => c[0])).toEqual(['red', 'orange', 'yellow', 'green', 'teal', 'blue', 'purple', 'pink']);
    expect(ink).toBeDefined();
  });
  it.each(colours)('text on %s (%s) is at least 4.5:1', (_name, hex) => {
    expect(ratio(ink!, hex)).toBeGreaterThanOrEqual(4.5);
  });
});

