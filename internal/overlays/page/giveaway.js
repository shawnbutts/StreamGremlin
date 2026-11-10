// The giveaway overlay (docs/adr/0029-giveaway-overlay.md): the running
// giveaway, and a roll that lands on each winner the engine already drew.
// It decides nothing and picks no one: the roll's names are for show, and
// the last is always the winner it was sent. Viewer text is only ever set
// with textContent, and pictures only come from Twitch's picture host.

const PICTURE_PREFIX = 'https://static-cdn.jtvnw.net/';
// The same times as the engine's (internal/overlays/giveaway.go): chat
// names a winner as their roll lands.
export const ROLL_MS = 3500;
export const WINNER_MS = 5000;
// An ended giveaway stays up this long with its winners.
export const ENDED_MS = 20000;

const plural = (n, one, many) => `${n} ${n === 1 ? one : many}`;

function text(doc, cls, value) {
  const el = doc.createElement('div');
  el.className = cls;
  el.textContent = String(value);
  return el;
}

function countdown(ms) {
  const s = Math.max(0, Math.ceil(ms / 1000));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
}

/**
 * The roll's timing: ticks come quickly, then slow down, landing on the
 * last name at ROLL_MS. Returns the time of each tick, from 0.
 */
export function rollTicks(names) {
  const n = Math.max(1, names);
  const ticks = [];
  let t = 0;
  // Enough ticks to go round the names a few times, slowing as it lands.
  const steps = Math.max(12, Math.min(40, n * 3));
  for (let i = 0; i < steps; i++) {
    const x = i / (steps - 1);
    ticks.push(Math.round(t));
    t += 1 + 2 * x * x * 4;
  }
  const scale = ROLL_MS / ticks[ticks.length - 1];
  return ticks.map((v) => Math.round(v * scale));
}

/**
 * Builds the giveaway display in stage. opts: setTimeout, now and
 * reducedMotion may be replaced in tests.
 */
export function giveawayView(doc, stage, opts = {}) {
  const later = opts.setTimeout || ((fn, ms) => setTimeout(fn, ms));
  const now = opts.now || (() => Date.now());
  const reduced = opts.reducedMotion || (() => false);

  let state = null;
  const draws = [];
  const pictures = new Map(); // draw id -> picture
  let showing = null; // the draw on screen, if any
  let box = null;
  let ticking = false;

  function mount(el) {
    if (box) box.remove();
    box = el;
    if (el) {
      stage.appendChild(el);
      el.classList.add('in');
    }
  }

  function panel() {
    if (!state || state.state === 'cancelled') return null;
    if (state.state === 'ended' && state.ended_at && now() - Date.parse(state.ended_at) > ENDED_MS) return null;
    const el = doc.createElement('div');
    el.className = 'sg-alert sg-giveaway';
    const words = doc.createElement('div');
    words.className = 'sg-words';
    words.appendChild(text(doc, 'sg-title', `🎉 ${state.title || 'Giveaway'}`));
    const winners = Array.isArray(state.winners) ? state.winners : [];
    if (state.state === 'ended') {
      const names = winners.map((w) => w.name).join(', ');
      words.appendChild(text(doc, 'sg-text', names ? `Congratulations, ${names}!` : 'It’s over.'));
    } else {
      words.appendChild(text(doc, 'sg-text', state.state === 'open' ? state.how_to_enter || '' : 'Entries are closed'));
      const facts = [];
      facts.push(plural(Number(state.entries) || 0, 'entry', 'entries'));
      if (state.state === 'open' && state.closes_at) facts.push(`closes in ${countdown(Date.parse(state.closes_at) - now())}`);
      words.appendChild(text(doc, 'sg-line', facts.join(' · ')));
      if (state.who) words.appendChild(text(doc, 'sg-line', `Open to ${state.who}`));
      if (state.chances) words.appendChild(text(doc, 'sg-line', state.chances.charAt(0).toUpperCase() + state.chances.slice(1)));
      for (const w of winners) {
        const how = w.outcome === 'pending_claim' ? ' (can claim)' : w.outcome === 'claimed' ? ' (claimed)' : '';
        words.appendChild(text(doc, 'sg-line sg-winner-line', `🏆 ${w.name}${how}`));
      }
    }
    el.appendChild(words);
    return el;
  }

  // The panel redraws each second while a countdown runs, or an ended
  // giveaway is waiting to go.
  function tick() {
    if (ticking) return;
    ticking = true;
    later(() => {
      ticking = false;
      if (!showing) mount(panel());
      if (state && ((state.state === 'open' && state.closes_at) || state.state === 'ended')) tick();
    }, 1000);
  }

  function initial(name) {
    const badge = doc.createElement('div');
    badge.className = 'sg-avatar';
    const n = typeof name === 'string' ? name.trim() : '';
    badge.textContent = n ? Array.from(n)[0].toUpperCase() : '★';
    return badge;
  }

  function winnerBox(d) {
    const el = doc.createElement('div');
    el.className = 'sg-alert sg-giveaway sg-giveaway-winner';
    const pic = pictures.get(d.id);
    if (typeof pic === 'string' && pic.startsWith(PICTURE_PREFIX)) {
      const img = doc.createElement('img');
      img.className = 'sg-picture';
      img.alt = '';
      img.referrerPolicy = 'no-referrer';
      img.addEventListener('error', () => img.replaceWith(initial(d.name)));
      img.src = pic;
      el.appendChild(img);
    } else {
      el.appendChild(initial(d.name));
    }
    const words = doc.createElement('div');
    words.className = 'sg-words';
    words.appendChild(text(doc, 'sg-title', d.title ? `Winner of ${d.title}` : 'Winner'));
    words.appendChild(text(doc, 'sg-text', `🎉 ${d.name}`));
    if (d.claim) words.appendChild(text(doc, 'sg-line', d.claim));
    el.appendChild(words);
    return el;
  }

  function showWinner(d) {
    mount(winnerBox(d));
    later(() => {
      showing = null;
      mount(panel());
      next();
    }, WINNER_MS);
  }

  function next() {
    if (showing) return;
    const d = draws.shift();
    if (!d) return;
    showing = d;
    const names = Array.isArray(d.roll) && d.roll.length ? d.roll.map(String) : [String(d.name)];
    // The roll always lands on the winner the engine sent.
    names[names.length - 1] = String(d.name);
    if (reduced()) {
      showWinner(d);
      return;
    }
    const el = doc.createElement('div');
    el.className = 'sg-alert sg-giveaway sg-roll';
    const words = doc.createElement('div');
    words.className = 'sg-words';
    words.appendChild(text(doc, 'sg-title', d.title ? `Drawing ${d.title}…` : 'Drawing…'));
    const name = text(doc, 'sg-text sg-roll-name', names[0]);
    words.appendChild(name);
    el.appendChild(words);
    mount(el);
    const ticks = rollTicks(names.length);
    ticks.forEach((at, i) => {
      const last = i === ticks.length - 1;
      later(() => {
        if (showing !== d) return;
        // Counted back from the end, so the last tick is the winner.
        const n = names.length;
        name.textContent = names[(((n - 1 - (ticks.length - 1 - i)) % n) + n) % n];
        if (last) showWinner(d);
      }, at);
    });
  }

  return {
    state(v) {
      state = v && typeof v === 'object' ? v : null;
      if (!state) {
        draws.length = 0;
      }
      if (!showing) mount(panel());
      tick();
    },
    draw(d) {
      if (!d || typeof d.id !== 'string' || typeof d.name !== 'string') return;
      draws.push(d);
      next();
    },
    picture(p) {
      if (!p || typeof p.id !== 'string' || typeof p.picture !== 'string' || !p.picture.startsWith(PICTURE_PREFIX)) return;
      pictures.set(p.id, p.picture);
      if (pictures.size > 50) pictures.delete(pictures.keys().next().value);
    },
    clear() {
      state = null;
      draws.length = 0;
      showing = null;
      mount(null);
    },
  };
}
