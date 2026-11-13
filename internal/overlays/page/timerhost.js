// A clock and timer overlay with a look other than Classic (D-092), in a
// sandboxed frame from the theme server, as wheel looks are
// (docs/adr/0036-packages-plugins-and-themes.md): no same origin, no
// network. This page stays in charge: Classic keeps counting underneath,
// and shows whenever the look isn't ready or reports an error, so OBS
// never shows a blank timer. The look is told what to show and when it
// changes; it can only say it's ready, or that something went wrong.
import { obsReach } from './themehost.js';

const PROTOCOL = 1;
const MAX_MESSAGE = 16 << 10;
const MAX_PER_SECOND = 20;
const FROM_THEME = ['theme.ready', 'theme.error'];

/**
 * opts: setTimeout, now, reducedMotion, classic (the Classic view, which
 * keeps counting), report(status, detail). Returns { changed(), update(theme),
 * url, id, stop() }: changed() says the look or state changed.
 */
export function themedTimerView(win, stage, theme, opts) {
  const doc = win.document;
  const later = opts.setTimeout;
  const now = opts.now || (() => Date.now());
  const reduced = opts.reducedMotion || (() => false);
  const report = opts.report || (() => {});
  const classic = opts.classic;
  let settings = theme.settings || {};
  let ready = false;
  let failed = false;
  let stopped = false;
  let window0 = 0;
  let count = 0;

  const frame = doc.createElement('iframe');
  frame.className = 'sg-theme-frame';
  frame.setAttribute('sandbox', 'allow-scripts');
  frame.setAttribute('referrerpolicy', 'no-referrer');
  frame.setAttribute('title', theme.name || 'Timer');
  frame.setAttribute('aria-hidden', 'true');
  frame.src = theme.url;
  stage.appendChild(frame);

  function post(type, payload) {
    if (frame.contentWindow) frame.contentWindow.postMessage({ protocolVersion: PROTOCOL, type, payload }, '*');
  }

  // What the look is told: the timer's settings and where it is, at
  // atMs on this computer's clock (the look counts on from there).
  function timer() {
    const v = classic.value();
    return {
      mode: v.settings.mode, label: v.settings.label, endText: v.settings.endText, clock: v.settings.clock, lengthMs: v.settings.lengthMs,
      valueMs: v.ms, running: v.running, direction: v.direction, atMs: now(),
    };
  }

  function showing(on) {
    frame.classList.toggle('on', on);
    classic.shown(!on);
  }
  showing(false);

  later(() => {
    if (!ready && !stopped) report('not_ready', `${theme.name} didn't start`);
  }, 5000);

  function onMessage(ev) {
    if (stopped || ev.source !== frame.contentWindow) return;
    const t = now();
    if (t - window0 >= 1000) {
      window0 = t;
      count = 0;
    }
    if (++count > MAX_PER_SECOND) return;
    const msg = ev.data;
    let size = 0;
    try {
      size = JSON.stringify(msg).length;
    } catch {
      return;
    }
    if (size > MAX_MESSAGE || !msg || typeof msg !== 'object' || msg.protocolVersion !== PROTOCOL || !FROM_THEME.includes(msg.type)) return;
    const p = msg.payload && typeof msg.payload === 'object' ? msg.payload : {};
    if (msg.type === 'theme.ready') {
      if (!ready) report('ready', `${theme.name} ready; ${obsReach(p)}`);
      ready = true;
      if (!failed) {
        post('timer.snapshot', { settings, reducedMotion: Boolean(reduced()), timer: timer() });
        showing(true);
      }
    } else if (msg.type === 'theme.error') {
      // Classic shows from now on; the look is told to stop.
      failed = true;
      showing(false);
      report('fallback', `${theme.name} couldn't show the timer, so it shows in Classic`);
    }
  }
  win.addEventListener('message', onMessage);

  return {
    url: theme.url,
    id: theme.id,
    changed() {
      if (ready && !failed) post('timer.updated', { timer: timer() });
    },
    update(t) {
      settings = t.settings || {};
      if (ready && !failed) post('theme.settings.updated', { settings });
    },
    stop() {
      stopped = true;
      win.removeEventListener('message', onMessage);
      frame.remove();
      classic.shown(true);
    },
  };
}
