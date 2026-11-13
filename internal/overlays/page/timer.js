// The clock and timer overlay (D-092), in Classic: a clock (this
// computer's time, and the date if chosen), a countdown or a stopwatch,
// in the overlay's look. The engine keeps a countdown's and a stopwatch's
// state; this page only counts the seconds from it. Text is only ever set
// with textContent.

// How often the display is redrawn: often enough that a second never
// shows late.
export const TICK_MS = 250;
const DEFAULT_SECONDS = 5 * 60;

const pad = (n) => String(n).padStart(2, '0');

/** "4:05", or "1:02:03" from an hour up. */
export function duration(ms) {
  const s = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  return h > 0 ? `${h}:${pad(m)}:${pad(s % 60)}` : `${m}:${pad(s % 60)}`;
}

/** What a timer's look asks for, with defaults: also what a themed look is told. */
export function timerSettings(look) {
  const l = look || {};
  const mode = ['clock', 'countdown', 'stopwatch'].includes(l.timer_mode) ? l.timer_mode : 'clock';
  const seconds = Number(l.timer_length) > 0 ? Math.min(86400, Math.floor(Number(l.timer_length))) : DEFAULT_SECONDS;
  return {
    mode,
    label: typeof l.timer_label === 'string' ? l.timer_label.slice(0, 60) : '',
    endText: typeof l.timer_end_text === 'string' ? l.timer_end_text.slice(0, 60) : '',
    lengthMs: seconds * 1000,
    clock: { hour24: l.clock_24h === true, seconds: l.clock_seconds === true, date: ['short', 'long'].includes(l.clock_date) ? l.clock_date : '' },
  };
}

/**
 * Where a countdown or stopwatch is now, from the engine's state (null:
 * at its start). offset is the engine's clock less this page's. Returns
 * { ms, running, direction } (direction -1 counting down, 1 up, 0 still).
 */
export function timerValue(settings, state, now, offset) {
  if (settings.mode === 'clock') return { ms: 0, running: false, direction: 0 };
  if (!state) return { ms: settings.mode === 'countdown' ? settings.lengthMs : 0, running: false, direction: 0 };
  const base = Number(state.base_ms) || 0;
  if (!state.running) return { ms: base, running: false, direction: 0 };
  const passed = now + offset - (Number(state.since_ms) || 0);
  if (settings.mode === 'countdown') {
    const ms = Math.max(0, base - passed);
    return { ms, running: ms > 0, direction: ms > 0 ? -1 : 0 };
  }
  return { ms: base + passed, running: true, direction: 1 };
}

/** What a clock says: the time, and the date if chosen. */
export function clockText(settings, now) {
  const d = new Date(now);
  const c = settings.clock;
  const time = d.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit', second: c.seconds ? '2-digit' : undefined, hour12: !c.hour24 });
  const date = c.date === 'long'
    ? d.toLocaleDateString(undefined, { weekday: 'long', year: 'numeric', month: 'long', day: 'numeric' })
    : c.date === 'short'
      ? d.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' })
      : '';
  return { time, date };
}

/**
 * Builds the Classic display in stage. opts: setTimeout and now may be
 * replaced in tests. Returns { look(l), state(v), shown(on), stop() }.
 */
export function timerView(doc, stage, opts = {}) {
  const later = opts.setTimeout || ((fn, ms) => setTimeout(fn, ms));
  const now = opts.now || (() => Date.now());
  let settings = timerSettings({});
  let state = null;
  let offset = 0;
  let stopped = false;
  let visible = true;

  const box = doc.createElement('div');
  box.className = 'sg-alert sg-timer';
  const label = doc.createElement('div');
  label.className = 'sg-title sg-timer-label';
  const digits = doc.createElement('div');
  digits.className = 'sg-text sg-timer-digits';
  const date = doc.createElement('div');
  date.className = 'sg-timer-date';
  const words = doc.createElement('div');
  words.className = 'sg-words';
  words.append(label, digits, date);
  box.appendChild(words);
  stage.appendChild(box);
  void box.offsetWidth;
  box.classList.add('in');

  function draw() {
    label.textContent = settings.label;
    label.hidden = !settings.label;
    if (settings.mode === 'clock') {
      const c = clockText(settings, now());
      digits.textContent = c.time;
      date.textContent = c.date;
      date.hidden = !c.date;
    } else {
      const v = timerValue(settings, state, now(), offset);
      const ms = settings.mode === 'countdown' ? Math.ceil(v.ms / 1000) * 1000 : v.ms;
      const done = settings.mode === 'countdown' && state && v.ms === 0 && settings.endText;
      digits.textContent = done ? settings.endText : duration(ms);
      digits.classList.toggle('sg-timer-done', Boolean(done));
      date.hidden = true;
    }
    box.hidden = !visible;
  }

  function tick() {
    if (stopped) return;
    draw();
    later(tick, TICK_MS);
  }
  tick();

  return {
    look(l) {
      settings = timerSettings(l);
      draw();
    },
    state(v) {
      state = v && typeof v === 'object' ? v : null;
      offset = state && Number.isFinite(Number(state.now_ms)) ? Number(state.now_ms) - now() : 0;
      draw();
    },
    /** A themed look shows instead: Classic hides, but keeps counting. */
    shown(on) {
      visible = on;
      draw();
    },
    /** Where it is now, for a themed look. */
    value: () => ({ settings, ...timerValue(settings, state, now(), offset) }),
    stop() {
      stopped = true;
      box.remove();
    },
  };
}
