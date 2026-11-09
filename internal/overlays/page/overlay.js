// StreamGremlin overlay page (docs/adr/0017-alert-overlays.md).
// It shows what the engine sends and says how each alert went: started,
// done, or why not. It decides nothing. Viewer text is only ever set with
// textContent, and pictures only come from Twitch's picture host.

const PICTURE_PREFIX = 'https://static-cdn.jtvnw.net/';
const POSITIONS = ['top-left', 'top', 'top-right', 'center', 'bottom-left', 'bottom', 'bottom-right'];
const OUT_MS = 450;

function hexToRgba(hex, opacity) {
  const m = /^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i.exec(hex || '');
  if (!m) return null;
  const a = Math.max(0, Math.min(100, Number(opacity) || 0)) / 100;
  return `rgba(${parseInt(m[1], 16)}, ${parseInt(m[2], 16)}, ${parseInt(m[3], 16)}, ${a})`;
}

/**
 * Starts the overlay in a window. opts.WebSocket and opts.setTimeout may
 * be replaced in tests. Returns { stop }.
 */
export function start(win, opts = {}) {
  const doc = win.document;
  const WS = opts.WebSocket || win.WebSocket;
  const later = opts.setTimeout || ((fn, ms) => win.setTimeout(fn, ms));
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
    if (/^#[0-9a-f]{6}$/i.test(look.text_color || '')) root.style.setProperty('--sg-text', look.text_color);
    const bg = hexToRgba(look.background, look.opacity);
    if (bg) root.style.setProperty('--sg-bg', bg);
    const pos = POSITIONS.includes(look.position) ? look.position : 'top';
    const anim = look.animation === 'pop' ? 'pop' : 'slide';
    stage.className = `pos-${pos} anim-${anim}`;
  }

  function build(alert) {
    const box = doc.createElement('div');
    box.className = 'sg-alert';
    if (typeof alert.picture === 'string' && alert.picture.startsWith(PICTURE_PREFIX)) {
      const img = doc.createElement('img');
      img.className = 'sg-picture';
      img.alt = '';
      img.referrerPolicy = 'no-referrer';
      img.addEventListener('error', () => img.remove());
      img.src = alert.picture;
      box.appendChild(img);
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
    later(() => {
      box.classList.add('out');
      later(() => {
        box.remove();
        send({ type: 'ack', id: alert.id, status: 'done' });
        playing = false;
        next();
      }, OUT_MS);
    }, Math.max(500, msLeft - OUT_MS));
  }

  function onMessage(ev) {
    let msg;
    try {
      msg = JSON.parse(ev.data);
    } catch {
      return;
    }
    if (msg.type === 'hello') {
      applyLook(msg.body && msg.body.overlay && msg.body.overlay.look);
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
