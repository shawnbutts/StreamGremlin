// Paper wheel: an example of an installed StreamGremlin theme. It shows
// the result StreamGremlin drew and sends, and never decides one. It talks
// only to the overlay page that frames it, with StreamGremlin's theme
// messages (protocol 1).
const PROTOCOL = 1;
const PAPERS = { Cream: '#f6edd9', Sky: '#dcebf7', Blush: '#f7e1e4' };
const INKS = ['#f2b880', '#9fd3c7', '#f7d488', '#c7b8ea', '#f4a6a6', '#b5d99c'];
const TURNS = 5;
const SVG = 'http://www.w3.org/2000/svg';

const $ = (id) => document.getElementById(id);
let settings = {};
let reduced = false;
let spinId = null;
const parent = window.parent;

function send(type, payload) {
  parent.postMessage({ protocolVersion: PROTOCOL, type, payload: payload || {} }, '*');
}

function apply() {
  const root = document.documentElement.style;
  root.setProperty('--paper', PAPERS[settings.paper] || PAPERS.Cream);
  if (/^#[0-9a-f]{6}$/i.test(settings.accent || '')) root.setProperty('--accent', settings.accent);
}

function weightOf(c) {
  const w = Number(c && c.weight);
  return Number.isInteger(w) && w > 0 ? w : 0;
}

function chance(w, total) {
  const p = (100 * w) / total;
  if (p < 0.1) return '<0.1%';
  return p >= 10 ? `${Math.round(p)}%` : `${p.toFixed(1)}%`;
}

function polar(deg, r) {
  const a = ((deg - 90) * Math.PI) / 180;
  return [r * Math.cos(a), r * Math.sin(a)];
}

function draw(choices, total) {
  const rotor = $('rotor');
  rotor.replaceChildren();
  const outline = Math.max(0, Math.min(8, Number(settings.outline) || 0));
  let at = 0;
  choices.forEach((c, i) => {
    const sweep = (360 * weightOf(c)) / total;
    const [x1, y1] = polar(at, 100);
    const [x2, y2] = polar(at + sweep, 100);
    const path = document.createElementNS(SVG, 'path');
    const whole = sweep >= 359.999;
    path.setAttribute('d', whole ? 'M 0 -100 A 100 100 0 1 1 -0.01 -100 Z' : `M 0 0 L ${x1} ${y1} A 100 100 0 ${sweep > 180 ? 1 : 0} 1 ${x2} ${y2} Z`);
    path.setAttribute('fill', INKS[i % INKS.length]);
    path.setAttribute('stroke', '#3b2f2a');
    path.setAttribute('stroke-width', String(outline / 2));
    rotor.appendChild(path);
    const [tx, ty] = polar(at + sweep / 2, 62);
    const text = document.createElementNS(SVG, 'text');
    text.setAttribute('x', String(tx));
    text.setAttribute('y', String(ty));
    text.setAttribute('text-anchor', 'middle');
    text.setAttribute('dominant-baseline', 'middle');
    const label = String(c.label || '');
    text.textContent = label.length > 16 ? `${label.slice(0, 15)}…` : label;
    rotor.appendChild(text);
    at += sweep;
  });
  const list = $('chances');
  list.replaceChildren(
    ...choices.map((c) => {
      const li = document.createElement('li');
      li.textContent = `${c.label}: ${chance(weightOf(c), total)}`;
      return li;
    }),
  );
  list.hidden = settings.show_chances === false;
}

function start(p) {
  const choices = Array.isArray(p.choices) ? p.choices : [];
  const total = choices.reduce((n, c) => n + weightOf(c), 0);
  const winner = Number(p.winner);
  const n = Number(p.draw);
  let below = 0;
  for (let i = 0; i < winner && i < choices.length; i++) below += weightOf(choices[i]);
  // Only a spin that adds up is shown: the drawn number lands in the winner's wedge.
  if (!choices[winner] || total <= 0 || !Number.isInteger(n) || n < below || n >= below + weightOf(choices[winner])) {
    send('theme.error', { spinId: p.spinId, code: 'bad_spin' });
    return;
  }
  spinId = p.spinId;
  $('title').textContent = settings.title || p.title || '';
  $('title').hidden = !$('title').textContent;
  $('result').classList.remove('on');
  draw(choices, total);
  const land = (360 * (n + 0.5)) / total;
  const rotor = $('rotor');
  const ms = reduced ? 0 : Math.max(0, Number(p.durationMs) || 0);
  rotor.style.transition = 'none';
  rotor.style.transform = 'rotate(0deg)';
  void rotor.getBoundingClientRect();
  rotor.style.transition = ms ? `transform ${ms}ms cubic-bezier(0.12, 0.7, 0.18, 1)` : 'none';
  rotor.style.transform = `rotate(${(ms ? TURNS * 360 : 0) + 360 - land}deg)`;
  $('stage').hidden = false;
  const mine = spinId;
  setTimeout(() => {
    if (spinId === mine) send('spin.animation.completed', { spinId: mine });
  }, ms);
}

window.addEventListener('message', (ev) => {
  if (ev.source !== parent || parent === window) return;
  const msg = ev.data;
  if (!msg || msg.protocolVersion !== PROTOCOL) return;
  const p = msg.payload && typeof msg.payload === 'object' ? msg.payload : {};
  switch (msg.type) {
    case 'session.snapshot':
      settings = p.settings || {};
      reduced = Boolean(p.reducedMotion);
      apply();
      break;
    case 'theme.settings.updated':
      settings = p.settings || {};
      apply();
      break;
    case 'spin.start':
      start(p);
      break;
    case 'winner.reveal':
      if (p.spinId === spinId) {
        $('result').textContent = `🎉 ${p.label}`;
        $('result').classList.add('on');
      }
      break;
    case 'spin.cancel':
      spinId = null;
      $('stage').hidden = true;
      break;
  }
});

send('theme.ready', { apiVersion: '1', obsApi: typeof window.obsstudio === 'object' });
