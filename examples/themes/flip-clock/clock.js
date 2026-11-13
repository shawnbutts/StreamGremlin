// Flip clock: an example of an installed StreamGremlin clock and timer
// look (D-092). It shows the time, or where the countdown or stopwatch
// StreamGremlin keeps is, and never decides anything. It talks only to
// the overlay page that frames it, with StreamGremlin's theme messages
// (protocol 1): it's told timer.snapshot and timer.updated, and says
// theme.ready (or theme.error).
const PROTOCOL = 1;
const CARDS = {
  Charcoal: ['#26282c', '#f4f1ea'],
  Cream: ['#f3ecd9', '#2b2620'],
  Midnight: ['#101a33', '#cfe3ff'],
};

const pad = (n) => String(n).padStart(2, '0');

/** "4:05", or "1:02:03" from an hour up. */
export function duration(ms) {
  const s = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  return h > 0 ? `${h}:${pad(m)}:${pad(s % 60)}` : `${m}:${pad(s % 60)}`;
}

/**
 * What the cards say at time now: { text, note }. timer is StreamGremlin's
 * timer message: mode, endText, clock options, and valueMs at atMs, moving
 * in direction (-1 down, 1 up, 0 still).
 */
export function reading(timer, now) {
  if (!timer) return { text: '', note: '' };
  if (timer.mode === 'clock') {
    const d = new Date(now);
    const c = timer.clock || {};
    let h = d.getHours();
    let suffix = '';
    if (!c.hour24) {
      suffix = h < 12 ? ' AM' : ' PM';
      h = h % 12 || 12;
    }
    const text = `${c.hour24 ? pad(h) : h}:${pad(d.getMinutes())}${c.seconds ? `:${pad(d.getSeconds())}` : ''}${suffix}`;
    const note = c.date === 'long'
      ? d.toLocaleDateString(undefined, { weekday: 'long', year: 'numeric', month: 'long', day: 'numeric' })
      : c.date === 'short'
        ? d.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' })
        : '';
    return { text, note };
  }
  const moved = (Number(timer.direction) || 0) * Math.max(0, now - (Number(timer.atMs) || now));
  const ms = Math.max(0, (Number(timer.valueMs) || 0) + moved);
  if (timer.mode === 'countdown') {
    const left = Math.ceil(ms / 1000) * 1000;
    if (left === 0 && timer.endText) return { text: '', note: String(timer.endText) };
    return { text: duration(left), note: '' };
  }
  return { text: duration(ms), note: '' };
}

// flipClock runs the look in win, its page's window. Tests can replace
// the timer functions.
export function flipClock(win, opts = {}) {
  const doc = win.document;
  const later = opts.setTimeout || ((fn, ms) => setTimeout(fn, ms));
  const now = opts.now || (() => Date.now());
  const $ = (id) => doc.getElementById(id);
  const parent = win.parent;
  let settings = {};
  let timer = null;
  let reduced = false;
  let shown = '';

  function send(type, payload) {
    parent.postMessage({ protocolVersion: PROTOCOL, type, payload: payload || {} }, '*');
  }

  function apply() {
    const root = doc.documentElement.style;
    const [card, ink] = CARDS[settings.cards] || CARDS.Charcoal;
    root.setProperty('--card', card);
    root.setProperty('--ink', ink);
    if (/^#[0-9a-f]{6}$/i.test(settings.accent || '')) root.setProperty('--accent', settings.accent);
    const size = Number(settings.size);
    root.setProperty('--size', String(Number.isFinite(size) ? Math.max(50, Math.min(200, size)) / 100 : 1));
  }

  // Each character is a card, and a colon a separator; a card flips when
  // its character changes.
  function draw() {
    const r = reading(timer, now());
    const label = String((timer && timer.label) || '');
    $('label').textContent = label;
    $('label').hidden = !label || settings.show_label === false;
    $('note').textContent = r.note;
    $('note').hidden = !r.note;
    if (r.text === shown) return;
    const cards = $('cards');
    const old = Array.from(cards.children);
    const chars = Array.from(r.text);
    const same = old.length === chars.length && old.every((el, i) => (el.dataset.ch === ':') === (chars[i] === ':'));
    if (!same) cards.replaceChildren();
    chars.forEach((ch, i) => {
      let el = same ? old[i] : null;
      if (!el) {
        el = doc.createElement('span');
        el.className = ch === ':' ? 'sep' : ch === ' ' ? 'sep' : /[A-Z]/.test(ch) ? 'card word' : 'card';
        cards.appendChild(el);
      }
      if (el.dataset.ch !== ch) {
        el.dataset.ch = ch;
        el.textContent = ch === ' ' ? ' ' : ch;
        if (same && !reduced && el.classList.contains('card')) {
          el.classList.remove('flip');
          void el.offsetWidth;
          el.classList.add('flip');
        }
      }
    });
    shown = r.text;
  }

  function tick() {
    if (timer) draw();
    later(tick, 200);
  }

  win.addEventListener('message', (ev) => {
    if (ev.source !== parent || parent === win) return;
    const m = ev.data;
    if (!m || m.protocolVersion !== PROTOCOL || typeof m.type !== 'string') return;
    const p = m.payload && typeof m.payload === 'object' ? m.payload : {};
    try {
      switch (m.type) {
        case 'timer.snapshot':
          settings = p.settings || {};
          reduced = p.reducedMotion === true;
          timer = p.timer || null;
          apply();
          shown = '';
          $('stage').hidden = false;
          draw();
          break;
        case 'timer.updated':
          timer = p.timer || null;
          draw();
          break;
        case 'theme.settings.updated':
          settings = p.settings || {};
          apply();
          draw();
          break;
      }
    } catch (err) {
      send('theme.error', { message: String((err && err.message) || err) });
    }
  });

  tick();
  send('theme.ready', { apiVersion: '1', obsApi: typeof win.obsstudio === 'object' });
}

if (typeof window !== 'undefined' && !window.__streamgremlinThemeTest) flipClock(window);
