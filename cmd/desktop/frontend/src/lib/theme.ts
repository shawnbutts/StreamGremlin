import type { Theme } from './api';

const lightQuery = '(prefers-color-scheme: light)';
let stopFollowing: (() => void) | undefined;

/** Resolve "system" to the OS preference; the brand kit has no "system" theme. */
export function resolveTheme(theme: Theme, prefersLight: boolean): Exclude<Theme, 'system'> {
  if (theme === 'system') return prefersLight ? 'light' : 'dark';
  return theme;
}

/** Apply a theme to the page, following OS changes while it's "system". */
export function applyTheme(theme: Theme): void {
  stopFollowing?.();
  stopFollowing = undefined;
  const mq = typeof matchMedia === 'function' ? matchMedia(lightQuery) : undefined;
  const set = () => {
    document.documentElement.dataset.theme = resolveTheme(theme, mq?.matches ?? false);
  };
  set();
  if (theme === 'system' && mq) {
    mq.addEventListener('change', set);
    stopFollowing = () => mq.removeEventListener('change', set);
  }
}
