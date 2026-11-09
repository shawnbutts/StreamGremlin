// Appearance shared by the sidebar and Settings (docs/adr/0019-app-comfort.md):
// text size and spacing on the page, and which screens sit under "More…".
import type { Density, TextSize, UISettings } from './api';

export const TEXT_SIZES: TextSize[] = ['small', 'standard', 'large', 'larger'];

/** Screens that can go under "More…", in sidebar order. */
export const MOVABLE = ['connections', 'timers', 'quotes', 'sounds', 'overlays', 'rewards', 'variables'];

export const appearance = $state<{ textSize: TextSize; density: Density; more: string[] }>({
  textSize: 'standard',
  density: 'comfortable',
  more: ['timers', 'quotes', 'rewards'],
});

/** Apply saved settings to the page and the sidebar. */
export function applyAppearance(ui: Partial<UISettings>) {
  if (ui.text_size) appearance.textSize = ui.text_size;
  if (ui.density) appearance.density = ui.density;
  if (ui.more) appearance.more = [...ui.more];
  const root = document.documentElement;
  root.dataset.textSize = appearance.textSize;
  root.dataset.density = appearance.density;
}

/** The next text size up (+1) or down (-1), or the same at either end. */
export function stepTextSize(by: 1 | -1): TextSize {
  const i = TEXT_SIZES.indexOf(appearance.textSize);
  return TEXT_SIZES[Math.min(TEXT_SIZES.length - 1, Math.max(0, i + by))];
}
