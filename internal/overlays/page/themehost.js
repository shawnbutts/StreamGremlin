// A wheel overlay with a look other than Classic
// (docs/adr/0036-packages-plugins-and-themes.md). The look is a page from
// the theme server, in a sandboxed frame: no same origin, no network. This
// page stays in charge: it plays spins one after another on the engine's
// clock (as Classic does), plays the sounds, and tells the look what to
// show. The look can only say it's ready, that an animation finished, or
// that it couldn't show a spin; anything else it sends is dropped. A spin
// the look can't show (not ready, or an error) is shown in Classic, so
// OBS never shows a blank wheel.
import { LAND_MS, RESULT_MS, landing } from './wheel.js';

const PROTOCOL = 1;
const MAX_MESSAGE = 16 << 10; // characters of JSON
const MAX_PER_SECOND = 20;
const FROM_THEME = ['theme.ready', 'spin.animation.completed', 'theme.error'];

// OBS's "Page permissions" for a browser source, by the level its page
// API reports (window.obsstudio.getControlLevel): what each lets a page do.
export const OBS_LEVELS = [
  'No access to OBS',
  'Read access to OBS status information',
  'Read access to user information',
  'Basic access to OBS',
  'Advanced access to OBS',
  'Full access to OBS',
];

/** How a look's report of OBS's page API reads in the status line. */
export function obsReach(p) {
  if (p.obsApi !== true) return 'OBS page API not reachable';
  const n = p.obsLevel;
  return Number.isInteger(n) && n >= 0 && n < OBS_LEVELS.length
    ? `OBS page API reachable, at level ${n} (${OBS_LEVELS[n]})`
    : 'OBS page API reachable; OBS didn’t tell the look its level';
}

/**
 * opts: setTimeout, now, reducedMotion, cue (sounds), classic (the Classic
 * wheel view, for spins the look can't show), report(status, detail).
 * Returns { spin(v), update(theme), url, stop() }.
 */
export function themedWheelView(win, stage, theme, opts) {
  const doc = win.document;
  const later = opts.setTimeout;
  const now = opts.now || (() => Date.now());
  const reduced = opts.reducedMotion || (() => false);
  const cue = opts.cue || (() => {});
  const report = opts.report || (() => {});
  let settings = theme.settings || {};
  let ready = false;
  let stopped = false;
  let current = null; // the spin the look is showing
  let gen = 0; // bumped when a spin's scheduled steps no longer apply
  const queue = [];
  const seen = new Set();
  let busy = false;
  let window0 = 0;
  let count = 0;

  const frame = doc.createElement('iframe');
  frame.className = 'sg-theme-frame';
  frame.setAttribute('sandbox', 'allow-scripts');
  frame.setAttribute('referrerpolicy', 'no-referrer');
  frame.setAttribute('title', theme.name || 'Wheel');
  frame.setAttribute('aria-hidden', 'true');
  frame.src = theme.url;
  stage.appendChild(frame);

  function post(type, payload) {
    if (frame.contentWindow) frame.contentWindow.postMessage({ protocolVersion: PROTOCOL, type, payload }, '*');
  }

  function snapshot() {
    post('session.snapshot', { settings, reducedMotion: Boolean(reduced()) });
  }

  // Not ready in 5 seconds: say so (spins meanwhile show in Classic).
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
      snapshot();
    } else if (msg.type === 'theme.error') {
      // Only an error about the spin on screen changes anything: that spin
      // shows in Classic, at once.
      if (current && p.spinId === current.id) fallback(current, true);
    }
    // spin.animation.completed is only the look's word: the engine's
    // clock decides when a spin is over.
  }
  win.addEventListener('message', onMessage);

  function show(on) {
    frame.classList.toggle('on', on);
  }

  function done(my) {
    if (my !== gen) return;
    current = null;
    busy = false;
    next();
  }

  // Classic shows the spin: from the start, or (mid-spin) stopped at once.
  function fallback(v, midway) {
    const my = ++gen;
    current = null;
    show(false);
    post('spin.cancel', {});
    report('fallback', `${theme.name} couldn't show a spin, so it showed in Classic`);
    const ms = Math.max(2, Math.min(20, Number(v.seconds) || 6)) * 1000;
    if (midway) {
      opts.classic(true).spin({ ...v, id: `${v.id}#classic` });
      later(() => done(my), RESULT_MS + LAND_MS);
    } else {
      opts.classic(false).spin(v);
      later(() => done(my), (reduced() ? 0 : ms + LAND_MS) + RESULT_MS + LAND_MS);
    }
  }

  function next() {
    if (busy || stopped) return;
    const v = queue.shift();
    if (!v) return;
    const choices = Array.isArray(v.choices) ? v.choices.slice(0, 50) : [];
    const winner = Number(v.winner);
    if (!choices[winner] || landing(choices, winner, v.draw) === null) {
      next(); // a spin that doesn't add up isn't shown, never guessed
      return;
    }
    busy = true;
    if (!ready) {
      fallback(v, false);
      return;
    }
    const my = ++gen;
    current = v;
    const ms = Math.max(2, Math.min(20, Number(v.seconds) || 6)) * 1000;
    const label = String(choices[winner].label || '');
    post('spin.start', {
      spinId: v.id, title: String(v.title || ''), winner, draw: v.draw, durationMs: reduced() ? 0 : ms,
      choices: choices.map((c) => ({ label: String((c && c.label) || ''), weight: c && c.weight, color: (c && c.color) || '' })),
    });
    show(true);
    const reveal = () => {
      if (my !== gen) return;
      cue('cheer');
      post('winner.reveal', { spinId: v.id, label });
      later(() => {
        if (my !== gen) return;
        show(false);
        post('spin.cancel', { spinId: v.id });
        done(my);
      }, RESULT_MS);
    };
    if (reduced()) {
      reveal();
      return;
    }
    cue('start');
    later(() => {
      if (my !== gen) return;
      cue('end'); // as it stops
      later(reveal, LAND_MS);
    }, ms);
  }

  return {
    url: theme.url,
    spin(v) {
      if (!v || typeof v.id !== 'string' || seen.has(v.id)) return;
      seen.add(v.id);
      if (seen.size > 100) seen.delete(seen.values().next().value);
      queue.push(v);
      next();
    },
    update(t) {
      settings = t.settings || {};
      if (ready) post('theme.settings.updated', { settings });
    },
    stop() {
      stopped = true;
      gen++;
      win.removeEventListener('message', onMessage);
      frame.remove();
    },
  };
}
