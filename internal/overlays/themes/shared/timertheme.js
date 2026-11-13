// A built-in clock and timer look (D-092, ADR-0045), dressed like the
// wheel look of the same name: the label in the look's title plate, and
// the time, countdown or stopwatch on its board. It shows where the timer
// is, as the overlay page says; it never starts, stops or decides
// anything. Text is set as text, never markup.
import { bridge, sayReady } from './bridge.js';

const HEX = /^#[0-9a-f]{6}$/i;

// Where the overlay is placed: the look sits there, as Classic would.
const PLACES = {
  'top-left': ['flex-start', 'flex-start'], top: ['flex-start', 'center'], 'top-right': ['flex-start', 'flex-end'],
  center: ['center', 'center'],
  'bottom-left': ['flex-end', 'flex-start'], bottom: ['flex-end', 'center'], 'bottom-right': ['flex-end', 'flex-end'],
};

/** Puts el (a flex column) where position says. */
export function place(el, position) {
  const [v, h] = PLACES[position] || PLACES.center;
  el.style.justifyContent = v;
  el.style.alignItems = h;
}

const pad = (n) => String(n).padStart(2, '0');

/** "4:05", or "1:02:03" from an hour up. */
export function duration(ms) {
  const s = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  return h > 0 ? `${h}:${pad(m)}:${pad(s % 60)}` : `${m}:${pad(s % 60)}`;
}

/**
 * What the board says at now: { text, note, done }. timer is the overlay
 * page's: mode, endText, clock options, and valueMs at atMs moving by
 * direction.
 */
export function reading(timer, now) {
  if (!timer) return { text: '', note: '', done: false };
  if (timer.mode === 'clock') {
    const d = new Date(now);
    const c = timer.clock || {};
    const text = d.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit', second: c.seconds ? '2-digit' : undefined, hour12: !c.hour24 });
    const note = c.date === 'long'
      ? d.toLocaleDateString(undefined, { weekday: 'long', year: 'numeric', month: 'long', day: 'numeric' })
      : c.date === 'short'
        ? d.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' })
        : '';
    return { text, note, done: false };
  }
  const moved = (Number(timer.direction) || 0) * Math.max(0, now - (Number(timer.atMs) || now));
  const ms = Math.max(0, (Number(timer.valueMs) || 0) + moved);
  if (timer.mode === 'countdown') {
    const left = Math.ceil(ms / 1000) * 1000;
    if (left === 0 && timer.endText) return { text: String(timer.endText), note: '', done: true };
    return { text: duration(left), note: '', done: false };
  }
  return { text: duration(ms), note: '', done: false };
}

/** Starts a timer look in a window. opts.setTimeout and opts.now may be replaced in tests. */
export function timerTheme(win, opts = {}) {
  const doc = win.document;
  const later = opts.setTimeout || ((fn, ms) => win.setTimeout(fn, ms));
  const now = opts.now || (() => Date.now());
  const app = doc.getElementById('app');
  let settings = { show_label: true };
  let timer = null;

  const title = doc.createElement('div');
  title.className = 'title';
  const plate = doc.createElement('div');
  plate.className = 'plate';
  const digits = doc.createElement('div');
  digits.className = 'digits';
  const note = doc.createElement('div');
  note.className = 'note';
  plate.append(digits, note);
  app.append(title, plate);
  app.hidden = true;

  function apply(s) {
    if (!s || typeof s !== 'object') return;
    if (typeof s.show_label === 'boolean') settings.show_label = s.show_label;
    if (typeof s.accent === 'string' && HEX.test(s.accent)) doc.documentElement.style.setProperty('--accent', s.accent);
  }

  function draw() {
    if (!timer) return;
    place(app, timer.position);
    const label = String(timer.label || '').slice(0, 60);
    title.textContent = label;
    title.hidden = !label || !settings.show_label;
    const r = reading(timer, now());
    if (digits.textContent !== r.text) digits.textContent = r.text;
    digits.classList.toggle('done', r.done);
    note.textContent = r.note;
    note.hidden = !r.note;
    plate.classList.toggle('running', timer.direction !== 0 && timer.mode !== 'clock');
  }

  function tick() {
    draw();
    later(tick, 200);
  }

  const send = bridge(win, {
    'timer.snapshot'(p) {
      apply(p.settings);
      doc.documentElement.classList.toggle('reduced', p.reducedMotion === true);
      timer = p.timer && typeof p.timer === 'object' ? p.timer : null;
      app.hidden = false;
      draw();
    },
    'timer.updated'(p) {
      timer = p.timer && typeof p.timer === 'object' ? p.timer : timer;
      draw();
    },
    'theme.settings.updated'(p) {
      apply(p.settings);
      draw();
    },
  });
  tick();
  sayReady(win, send, later);
}
