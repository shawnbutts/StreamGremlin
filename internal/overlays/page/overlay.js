// StreamGremlin overlay page (docs/adr/0017-alert-overlays.md).
import { giveawayView } from './giveaway.js';
import { wheelView } from './wheel.js';

// It shows what the engine sends and says how each alert went: started,
// done, or why not. It decides nothing. Viewer text is only ever set with
// textContent, and pictures only come from Twitch's picture host.

const PICTURE_PREFIX = 'https://static-cdn.jtvnw.net/';
const POSITIONS = ['top-left', 'top', 'top-right', 'center', 'bottom-left', 'bottom', 'bottom-right'];
// How long leaving takes: unfold folds the words away, then the picture.
const OUT_MS = { slide: 450, pop: 450, unfold: 750 };

const HEX = /^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i;
const FONT_NAME = /^[A-Za-z0-9][A-Za-z0-9 _.&'-]{0,63}$/;

// Font lists most computers have, so a choice looks alike on macOS and
// Windows. "custom" uses a font installed on this computer, by name.
const FONTS = {
  system: 'system-ui, -apple-system, "Segoe UI", Roboto, sans-serif',
  rounded: 'ui-rounded, "SF Pro Rounded", "Arial Rounded MT Bold", "Nunito", "Varela Round", system-ui, sans-serif',
  serif: 'Georgia, "Times New Roman", serif',
  mono: 'ui-monospace, "SF Mono", Menlo, Consolas, monospace',
  condensed: 'Impact, Haettenschweiler, "Arial Narrow Bold", "Bahnschrift Condensed", sans-serif',
  handwritten: '"Comic Sans MS", "Chalkboard SE", "Marker Felt", "Segoe Print", cursive',
};

const clamp = (n, lo, hi, dflt) => {
  const x = Number(n);
  return Number.isFinite(x) ? Math.max(lo, Math.min(hi, Math.round(x))) : dflt;
};

function hexToRgba(hex, opacity) {
  const m = HEX.exec(hex || '');
  if (!m) return null;
  const a = clamp(opacity, 0, 100, 100) / 100;
  return `rgba(${parseInt(m[1], 16)}, ${parseInt(m[2], 16)}, ${parseInt(m[3], 16)}, ${a})`;
}

/** The CSS custom properties for a look, from checked values only. */
export function lookStyle(look) {
  const out = {};
  if (!look) return out;
  const color = (c) => (HEX.test(c || '') ? c : null);
  if (color(look.text_color)) out['--sg-text'] = look.text_color;
  if (color(look.title_color)) out['--sg-title'] = look.title_color;
  const bg = hexToRgba(look.background, look.opacity);
  if (bg) {
    const bg2 = hexToRgba(look.background2, look.opacity);
    out['--sg-bg'] = bg2 ? `linear-gradient(${clamp(look.gradient_angle, 0, 359, 135)}deg, ${bg}, ${bg2})` : bg;
  }
  out['--sg-radius'] = `${clamp(look.radius, 0, 48, 16)}px`;
  const bw = clamp(look.border_width, 0, 12, 0);
  out['--sg-border'] = bw > 0 && color(look.border_color) ? `${bw}px solid ${look.border_color}` : 'none';
  const shadowColor = color(look.shadow_color) || '#000000';
  const shadow = clamp(look.shadow, 0, 100, 35);
  out['--sg-shadow'] = shadow > 0
    ? `0 ${Math.round(shadow / 4.4)}px ${Math.round(shadow * 0.86)}px ${hexToRgba(shadowColor, shadow)}`
    : 'none';
  // Letters: an outline drawn as shadows all around (works in every
  // browser OBS has used), then a drop shadow.
  const parts = [];
  const ow = clamp(look.text_outline, 0, 6, 0);
  if (ow > 0) {
    const oc = color(look.outline_color) || '#000000';
    const steps = ow > 2 ? 16 : 8;
    for (let i = 0; i < steps; i++) {
      const a = (i / steps) * 2 * Math.PI;
      parts.push(`${(Math.cos(a) * ow).toFixed(2)}px ${(Math.sin(a) * ow).toFixed(2)}px 0 ${oc}`);
    }
  }
  const ts = clamp(look.text_shadow, 0, 100, 0);
  if (ts > 0) {
    const off = (1 + ts / 25).toFixed(1);
    parts.push(`${off}px ${off}px ${(ts / 10).toFixed(1)}px ${hexToRgba(shadowColor, Math.min(100, 30 + ts * 0.7))}`);
  }
  out['--sg-text-shadow'] = parts.length ? parts.join(', ') : 'none';
  let font = FONTS[look.font] || FONTS.system;
  if (look.font === 'custom' && FONT_NAME.test(look.font_name || '')) font = `"${look.font_name}", ${FONTS.system}`;
  out['--sg-font'] = font;
  out['--sg-scale'] = String(clamp(look.text_size, 50, 200, 100) / 100);
  out['--sg-pace'] = String(clamp(look.pace, 50, 300, 100) / 100);
  return out;
}

/**
 * Starts the overlay in a window. opts.WebSocket and opts.setTimeout may
 * be replaced in tests. Returns { stop }.
 */
export function start(win, opts = {}) {
  const doc = win.document;
  const WS = opts.WebSocket || win.WebSocket;
  const later = opts.setTimeout || ((fn, ms) => win.setTimeout(fn, ms));
  const makeAudio = opts.Audio || ((src) => new win.Audio(src));
  const params = new URLSearchParams(win.location.search);
  const preview = params.get('preview') === '1';
  const showStatus = preview || params.get('status') === '1';
  const stage = doc.getElementById('stage');
  const status = doc.getElementById('status');
  const base = win.location.pathname.replace(/[^/]*$/, '');
  const url = `${win.location.protocol === 'https:' ? 'wss' : 'ws'}://${win.location.host}${base}ws${preview ? '?preview=1' : ''}`;

  const seen = new Set();
  const queue = [];
  let playing = false;
  let ws = null;
  let stopped = false;
  let retry = 1000;
  let animation = 'slide';
  let pageVersion = ''; // the engine's page this one is, from the first hello
  const reload = opts.reload || (() => win.location.reload());
  // A giveaway overlay (docs/adr/0029-giveaway-overlay.md) shows the
  // running giveaway; made when the first hello says this is one.
  let gw = null;
  // A wheel overlay (docs/adr/0030-wheels.md) spins to each result sent.
  let wheel = null;
  const reducedMotion =
    opts.reducedMotion || (() => Boolean(win.matchMedia && win.matchMedia('(prefers-reduced-motion: reduce)').matches));
  let pace = 1; // how long animations take, times the usual

  function setStatus(text) {
    if (!status) return;
    status.textContent = text;
    status.hidden = !showStatus || !text;
  }

  function send(msg) {
    if (ws && ws.readyState === 1) ws.send(JSON.stringify(msg));
  }

  function applyLook(look) {
    if (!look) return;
    const root = doc.documentElement;
    for (const [k, v] of Object.entries(lookStyle(look))) root.style.setProperty(k, v);
    pace = clamp(look.pace, 50, 300, 100) / 100;
    const pos = POSITIONS.includes(look.position) ? look.position : 'top';
    animation = ['pop', 'unfold'].includes(look.animation) ? look.animation : 'slide';
    // Unfold opens toward the middle: from the right edge on the right.
    const side = animation === 'unfold' && pos.endsWith('right') ? ' side-right' : '';
    stage.className = `pos-${pos} anim-${animation}${side}`;
  }

  // A circle with the viewer's initial (or a star), where a picture
  // would go: unfold keeps its shape without one.
  function initial(alert) {
    const badge = doc.createElement('div');
    badge.className = 'sg-avatar';
    const name = typeof alert.name === 'string' ? alert.name.trim() : '';
    badge.textContent = name ? Array.from(name)[0].toUpperCase() : '★';
    return badge;
  }

  function build(alert) {
    const box = doc.createElement('div');
    box.className = 'sg-alert';
    const hasPicture = typeof alert.picture === 'string' && alert.picture.startsWith(PICTURE_PREFIX);
    if (hasPicture) {
      const img = doc.createElement('img');
      img.className = 'sg-picture';
      img.alt = '';
      img.referrerPolicy = 'no-referrer';
      img.addEventListener('error', () => {
        if (animation === 'unfold') img.replaceWith(initial(alert));
        else img.remove();
      });
      img.src = alert.picture;
      box.appendChild(img);
    } else if (animation === 'unfold') {
      box.appendChild(initial(alert));
    }
    const words = doc.createElement('div');
    words.className = 'sg-words';
    if (alert.title) {
      const t = doc.createElement('div');
      t.className = 'sg-title';
      t.textContent = String(alert.title);
      words.appendChild(t);
    }
    const x = doc.createElement('div');
    x.className = 'sg-text';
    x.textContent = String(alert.text || '');
    words.appendChild(x);
    box.appendChild(words);
    return box;
  }

  // The alert's sound plays here, in the page, so OBS hears it through the
  // browser source (tick "Control audio via OBS" to give it a mixer bar).
  // The app's preview stays silent.
  function playSound(alert) {
    const snd = alert.sound;
    if (!snd || preview || typeof snd.name !== 'string' || !/^[a-z0-9][a-z0-9_-]{0,63}$/.test(snd.name)) return;
    let audio;
    try {
      audio = makeAudio(`${base}sound/${encodeURIComponent(snd.name)}`);
      audio.volume = clamp(snd.volume, 0, 100, 80) / 100;
    } catch (err) {
      send({ type: 'ack', id: alert.id, status: 'sound_error', detail: String((err && err.message) || err) });
      return;
    }
    Promise.resolve(audio.play())
      .then(() => send({ type: 'ack', id: alert.id, status: 'sound_started' }))
      .catch((err) => send({ type: 'ack', id: alert.id, status: 'sound_error', detail: String((err && (err.name || err.message)) || err) }));
  }

  function next() {
    if (playing || stopped) return;
    const item = queue.shift();
    if (!item) return;
    playing = true;
    const { alert, msLeft } = item;
    let box;
    try {
      box = build(alert);
      stage.appendChild(box);
      // Read layout so the starting state is drawn before the change.
      void box.offsetWidth;
      box.classList.add('in');
    } catch (err) {
      send({ type: 'ack', id: alert.id, status: 'error', detail: String(err && err.message ? err.message : err) });
      playing = false;
      next();
      return;
    }
    send({ type: 'ack', id: alert.id, status: 'started' });
    playSound(alert);
    const outMs = Math.round((OUT_MS[animation] || 450) * pace);
    later(() => {
      box.classList.add('out');
      later(() => {
        box.remove();
        send({ type: 'ack', id: alert.id, status: 'done' });
        playing = false;
        next();
      }, outMs);
    }, Math.max(500, msLeft - outMs));
  }

  function onMessage(ev) {
    let msg;
    try {
      msg = JSON.parse(ev.data);
    } catch {
      return;
    }
    if (msg.type === 'hello') {
      // The engine was updated since this page loaded: load the new page.
      const version = msg.body && msg.body.page_version;
      if (version && pageVersion && version !== pageVersion) {
        stopped = true;
        if (ws) ws.close();
        reload();
        return;
      }
      if (version) pageVersion = version;
      applyLook(msg.body && msg.body.overlay && msg.body.overlay.look);
      if (msg.body && msg.body.overlay && msg.body.overlay.kind === 'giveaway' && !gw) {
        gw = giveawayView(doc, stage, { setTimeout: later, now: opts.now, reducedMotion });
      }
      if (msg.body && msg.body.overlay && msg.body.overlay.kind === 'wheel' && !wheel) {
        wheel = wheelView(doc, stage, { setTimeout: later, reducedMotion });
      }
    } else if (wheel && msg.type === 'spin') {
      wheel.spin(msg.body);
    } else if (gw && msg.type === 'giveaway') {
      gw.state(msg.body);
    } else if (gw && msg.type === 'draw') {
      gw.draw(msg.body);
    } else if (gw && msg.type === 'picture') {
      gw.picture(msg.body);
    } else if (msg.type === 'alert') {
      const alert = msg.body && msg.body.alert;
      if (!alert || !alert.id || seen.has(alert.id)) return;
      seen.add(alert.id);
      if (seen.size > 200) seen.delete(seen.values().next().value);
      queue.push({ alert, msLeft: Number(msg.body.ms_left) || (Number(alert.seconds) || 6) * 1000 });
      next();
    }
  }

  function connect() {
    if (stopped) return;
    setStatus(preview ? 'Connecting…' : '');
    try {
      ws = new WS(url, 'streamgremlin.overlay.v1');
    } catch {
      later(connect, retry);
      return;
    }
    ws.addEventListener('open', () => {
      retry = 1000;
      setStatus('');
    });
    ws.addEventListener('message', onMessage);
    ws.addEventListener('close', () => {
      ws = null;
      if (stopped) return;
      setStatus('Not connected to StreamGremlin');
      later(connect, retry);
      retry = Math.min(retry * 2, 10000);
    });
  }

  connect();
  return {
    stop() {
      stopped = true;
      if (ws) ws.close();
    },
  };
}

if (typeof window !== 'undefined' && !window.__streamgremlinOverlayTest) {
  start(window);
}
