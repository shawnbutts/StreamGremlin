// StreamGremlin overlay page (docs/adr/0017-alert-overlays.md).
import { giveawayView } from './giveaway.js';
import { songView } from './song.js';
import { OBS_LEVELS, themedWheelView } from './themehost.js';
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
// A wheel look's sound: a .wav file from the theme server (D-065).
const THEME_SOUND = /^http:\/\/127\.0\.0\.1:\d+\/t\/[A-Za-z0-9\/._-]+\.wav$/;

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
  // A wheel overlay (docs/adr/0030-wheels.md) spins to each result sent:
  // in Classic, drawn here, or in a look from the theme server, in a
  // sandboxed frame (docs/adr/0036-packages-plugins-and-themes.md), which
  // falls back to Classic for a spin it can't show.
  let wheel = null;
  let themed = null; // the look's view, when it isn't Classic
  let classic = null;
  let classicStill = null; // Classic, stopped at once: for a look that fails mid-spin
  // A Now playing overlay (D-090) shows the song the engine sends.
  let sv = null;
  const reducedMotion =
    opts.reducedMotion || (() => Boolean(win.matchMedia && win.matchMedia('(prefers-reduced-motion: reduce)').matches));
  let pace = 1; // how long animations take, times the usual
  let look = null; // the overlay's look, for giveaway and wheel sounds
  let lookSounds = null; // a wheel look's own sounds, by cue

  function setStatus(text) {
    if (!status) return;
    status.textContent = text;
    status.hidden = !showStatus || !text;
  }

  function send(msg) {
    if (ws && ws.readyState === 1) ws.send(JSON.stringify(msg));
  }

  // A giveaway or wheel overlay's sound (docs/adr/0031-overlay-sounds.md):
  // "start" as a roll or spin starts, "end" as it lands, "cheer" as the
  // result shows. Played here so OBS
  // hears it; the app's preview stays silent. A sound that won't play is
  // skipped: chat already says everything.
  //
  // A wheel look may have its own sounds (D-065): one plays for a cue the
  // streamer picked no sound for, at that cue's volume.
  function cue(which) {
    if (preview || !look) return;
    const name = look[`${which}_sound`];
    const volume = look[`${which}_volume`];
    if (!['start', 'end', 'cheer'].includes(which)) return;
    let url = '';
    if (typeof name === 'string' && name !== '') {
      if (!/^[a-z0-9][a-z0-9_-]{0,63}$/.test(name)) return;
      url = `${base}sound/${encodeURIComponent(name)}`;
    } else if (lookSounds && typeof lookSounds[which] === 'string' && THEME_SOUND.test(lookSounds[which])) {
      url = lookSounds[which];
    }
    if (!url) return;
    try {
      const audio = makeAudio(url);
      audio.volume = clamp(volume, 0, 100, 80) / 100;
      Promise.resolve(audio.play()).catch(() => {});
    } catch {
      // skipped, as above
    }
  }

  function applyLook(l) {
    if (!l) return;
    look = l;
    const root = doc.documentElement;
    for (const [k, v] of Object.entries(lookStyle(l))) root.style.setProperty(k, v);
    pace = clamp(l.pace, 50, 300, 100) / 100;
    const pos = POSITIONS.includes(l.position) ? l.position : 'top';
    animation = ['pop', 'unfold'].includes(l.animation) ? l.animation : 'slide';
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

  // What OBS lets this browser source's pages do: its "Page permissions"
  // (D-067). They reach a look's sandboxed frame too, so an add-on look,
  // someone else's code, is only framed at level 0, "No access to OBS".
  // null: not in OBS (a browser, or the app's preview); 'asking' until
  // OBS answers; 'unknown' if it can't say or doesn't in time; else 0-5.
  let obsLevel = null;
  let lastTheme;
  let pendingTheme; // a new version of the look on screen, waiting for its spin to finish
  let refused = ''; // the add-on look and level last refused, said once
  const obs = (() => {
    try {
      return win.obsstudio;
    } catch {
      return undefined;
    }
  })();
  if (obs !== undefined && obs !== null) {
    obsLevel = 'unknown';
    if (typeof obs.getControlLevel === 'function') {
      obsLevel = 'asking';
      const answer = (level) => {
        if (obsLevel !== 'asking') return;
        obsLevel = Number.isInteger(level) && level >= 0 && level < OBS_LEVELS.length ? level : 'unknown';
        if (lastTheme !== undefined) setWheel(lastTheme);
      };
      try {
        obs.getControlLevel(answer);
      } catch {
        obsLevel = 'unknown';
      }
      later(() => answer(undefined), 2000);
    }
  }

  // Why an add-on look can't be framed now, or '' when it can.
  function addOnBlocked() {
    if (obsLevel === null || obsLevel === 0) return '';
    if (obsLevel === 'asking') return 'asking';
    if (obsLevel === 'unknown') return 'OBS didn’t say what this browser source’s Page permissions allow';
    return `this browser source’s Page permissions are “${OBS_LEVELS[obsLevel]}”`;
  }

  function classicView(still) {
    if (still) return (classicStill ??= wheelView(doc, stage, { setTimeout: later, reducedMotion: () => true, cue }));
    return (classic ??= wheelView(doc, stage, { setTimeout: later, reducedMotion, cue }));
  }

  // What a look says about itself: in the status line (the app's preview,
  // or ?status=1 in OBS) and to the engine's log.
  function report(state, detail) {
    // Shown only in previews, or in OBS with ?status=1 (where whether OBS's
    // page API reaches the look is worth knowing; in the app it never does).
    setStatus(`Look: ${preview ? detail.replace(/;.*/, '') : detail}`);
    send({ type: 'theme', status: state, detail });
  }

  // The wheel's look, from the hello: Classic, or a theme by its link.
  function setWheel(theme) {
    lastTheme = theme;
    const valid = theme && typeof theme.url === 'string' && /^http:\/\/127\.0\.0\.1:\d+\/t\//.test(theme.url);
    // An add-on, unless the theme server's built-in path says otherwise.
    const addOn = valid && (theme.add_on === true || !/^http:\/\/127\.0\.0\.1:\d+\/t\/builtin\//.test(theme.url));
    const blocked = addOn ? addOnBlocked() : '';
    lookSounds = valid && !blocked && theme.sounds && typeof theme.sounds === 'object' ? theme.sounds : null;
    if (blocked) {
      pendingTheme = undefined;
      if (themed) themed.stop();
      themed = null;
      wheel = classicView(false);
      const key = `${theme.url} ${obsLevel}`;
      if (blocked !== 'asking' && refused !== key) {
        refused = key;
        const name = String(theme.name || 'The add-on look');
        report('refused', `${name} not loaded: ${blocked}. Add-on looks only load at “No access to OBS”; Classic shows instead`);
      }
      return;
    }
    if (!valid) {
      pendingTheme = undefined;
      if (themed) themed.stop();
      themed = null;
      wheel = classicView(false);
      return;
    }
    if (themed && themed.url === theme.url) {
      pendingTheme = undefined;
      themed.update(theme);
      return;
    }
    // A new version of the look mid-spin (an update or roll back): that
    // spin finishes first; the next is in the new version.
    if (themed && themed.id === theme.id && themed.busy()) {
      pendingTheme = theme;
      return;
    }
    pendingTheme = undefined;
    const waiting = themed ? themed.stop() : [];
    themed = themedWheelView(win, stage, theme, {
      setTimeout: later,
      now: opts.now,
      reducedMotion,
      cue,
      classic: classicView,
      report,
      between: () => {
        if (pendingTheme !== undefined) setWheel(pendingTheme);
      },
    });
    wheel = themed;
    for (const v of waiting) themed.spin(v);
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
        gw = giveawayView(doc, stage, { setTimeout: later, now: opts.now, reducedMotion, cue });
      }
      if (msg.body && msg.body.overlay && msg.body.overlay.kind === 'wheel') setWheel(msg.body.theme);
      if (msg.body && msg.body.overlay && msg.body.overlay.kind === 'song') {
        if (sv) sv.relook();
        else sv = songView(doc, stage, { setTimeout: later, look: () => look, animation: () => animation, pace: () => pace });
      }
    } else if (sv && msg.type === 'song') {
      sv.song(msg.body);
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
