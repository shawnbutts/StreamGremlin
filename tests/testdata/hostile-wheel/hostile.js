// Hostile wheel (test): a deliberately hostile wheel look, for
// StreamGremlin's tests and live checks (docs/adr/0036-packages-plugins-and-themes.md,
// D-068). Never for a real stream. It tries what a look mustn't be able
// to do and lists, on screen while a spin shows, what was blocked and what
// worked. Nothing it tries does harm if it works: it only reads, and the
// one OBS call is getStatus (read only).
//
// It also misbehaves towards the overlay page that frames it: a flood of
// messages, an oversized one, kinds a look may not send, finishing spins
// that aren't its own, and (with "lie") a made-up winner on screen, which
// no sandbox can stop: the real result is always in chat.
const PROTOCOL = 1;

export function hostile(win, opts = {}) {
  const doc = win.document;
  const parent = win.parent;
  const later = opts.setTimeout || ((fn, ms) => win.setTimeout(fn, ms));
  const probe = opts.probe !== false;
  const $ = (id) => doc.getElementById(id);
  let settings = {};
  const tries = [];

  const send = (type, payload, extra) =>
    parent.postMessage({ protocolVersion: PROTOCOL, type, payload: payload || {}, ...(extra || {}) }, '*');

  function render() {
    const list = $('tries');
    if (!list) return;
    list.replaceChildren(
      ...tries.map(([what, outcome]) => {
        const li = doc.createElement('li');
        li.textContent = `${what}: ${outcome}`;
        if (outcome === 'worked') li.className = 'worked';
        return li;
      }),
    );
  }

  function note(what, outcome) {
    tries.push([what, outcome]);
    render();
  }

  // Runs fn; "worked" if it does, "blocked" if it throws or is refused.
  function attempt(what, fn) {
    let p;
    try {
      p = Promise.resolve(fn());
    } catch {
      note(what, 'blocked');
      return;
    }
    p.then(
      (r) => note(what, typeof r === 'string' ? r : 'worked'),
      () => note(what, 'blocked'),
    );
  }

  // Waits for a callback or an event, up to a second and a half.
  const within = (start) =>
    new Promise((resolve, reject) => {
      start(resolve, reject);
      later(() => reject(new Error('no answer')), 1500);
    });

  // The overlay page's origin, which a sandboxed page isn't meant to need.
  function engineOrigin() {
    const a = win.location.ancestorOrigins;
    return a && a.length ? a[0] : 'http://127.0.0.1:7431';
  }

  if (probe) {
    attempt('Reach the internet', () => win.fetch('http://hostile.invalid/'));
    attempt('Reach StreamGremlin’s engine', () => win.fetch(`${engineOrigin()}/v1/status`));
    attempt('Open a WebSocket to the engine', () =>
      within((ok, no) => {
        const ws = new win.WebSocket(`${engineOrigin().replace(/^http/, 'ws')}/o/x/ws`);
        ws.onopen = () => ok();
        ws.onerror = () => no(new Error('refused'));
      }),
    );
    attempt('Read the overlay page', () => String(parent.document.title).length >= 0);
    attempt('Read the overlay’s link', () => String(parent.location.href).length >= 0);
    attempt('Keep data (local storage)', () => win.localStorage.setItem('hostile', '1'));
    attempt('Read cookies', () => String(doc.cookie).length >= 0);
    attempt('Open a window', () => {
      const w = win.open('about:blank');
      if (!w) throw new Error('blocked');
      w.close();
    });
    attempt('Use OBS’s page API (read status)', () =>
      within((ok, no) => {
        const obs = win.obsstudio;
        if (!obs || typeof obs.getStatus !== 'function') return no(new Error('not offered'));
        obs.getStatus(() => ok());
      }),
    );
  }

  win.addEventListener('message', (ev) => {
    if (ev.source !== parent || parent === win) return;
    const msg = ev.data;
    if (!msg || msg.protocolVersion !== PROTOCOL) return;
    const p = msg.payload && typeof msg.payload === 'object' ? msg.payload : {};
    switch (msg.type) {
      case 'session.snapshot':
      case 'theme.settings.updated':
        settings = p.settings || {};
        break;
      case 'spin.start': {
        if (settings.hang) {
          const until = Date.now() + 3000;
          while (Date.now() < until) {
            // a look that freezes: the page's clock and Classic must carry on
          }
        }
        $('claim').textContent = '';
        // Misbehave: finish at once, finish others' spins, claim a reveal,
        // flood, and send something far too big.
        send('spin.animation.completed', { spinId: p.spinId });
        send('theme.error', { spinId: 'not-this-one', code: 'x' });
        send('winner.reveal', { spinId: p.spinId, label: 'Hostile' });
        for (let i = 0; i < 200; i++) send('spin.animation.completed', { spinId: `flood-${i}` });
        send('theme.ready', { pad: 'x'.repeat(64 << 10) });
        break;
      }
      case 'winner.reveal':
        $('claim').textContent = settings.lie === false ? `🎉 ${p.label}` : '🎉 Not the real winner (a look can draw anything: chat has the real one)';
        break;
      case 'spin.cancel':
        $('claim').textContent = '';
        break;
    }
  });

  send('theme.ready', { apiVersion: '1', obsApi: typeof win.obsstudio === 'object' });
  return { tries };
}

if (typeof window !== 'undefined' && !window.__streamgremlinThemeTest) hostile(window);
