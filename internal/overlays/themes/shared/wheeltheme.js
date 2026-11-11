// A built-in wheel look (docs/adr/0036-packages-plugins-and-themes.md):
// draws the wheel the overlay page sends, spins to the drawn number, and
// shows the result when told. It never picks, and never decides when a
// spin is over: the engine does. Labels are text, never markup.
import { bridge, sayReady } from './bridge.js';
import { HEX, TURNS, cleanChoices, el, inkFor, landing, polar, shortLabel, spans, wedgePath } from './wheel.js';

/**
 * Starts a wheel look in a window. style: { palette: [colours],
 * rim(svg, doc, cx), studs: colour, edge: colour, labelStroke: colour or ''}.
 * opts.setTimeout may be replaced in tests.
 */
export function wheelTheme(win, style, opts = {}) {
  const doc = win.document;
  const later = opts.setTimeout || ((fn, ms) => win.setTimeout(fn, ms));
  const app = doc.getElementById('app');
  let settings = { title: '', show_title: true, accent: '' };
  let reduced = false;
  let spinId = '';
  let gen = 0;

  const header = doc.createElement('div');
  header.className = 'title';
  const stageEl = doc.createElement('div');
  stageEl.className = 'stage';
  const result = doc.createElement('div');
  result.className = 'result';
  result.setAttribute('role', 'status');
  result.hidden = true;
  app.append(header, stageEl, result);

  function apply(s) {
    if (!s || typeof s !== 'object') return;
    if (typeof s.title === 'string') settings.title = s.title.slice(0, 70);
    if (typeof s.show_title === 'boolean') settings.show_title = s.show_title;
    if (typeof s.accent === 'string' && HEX.test(s.accent)) {
      settings.accent = s.accent;
      doc.documentElement.style.setProperty('--accent', s.accent);
    }
  }

  function draw(choices) {
    const cx = 250;
    const svg = el(doc, 'svg', { viewBox: '0 0 500 500', class: 'wheel', role: 'img' });
    style.rim(svg, doc, cx);
    const rotor = el(doc, 'g', { class: 'rotor' });
    spans(choices).forEach(([from, to], i) => {
      const c = choices[i];
      const fill = c.color || style.palette[i % style.palette.length] || '#888888';
      // An odd count would put the first and last wedge in the same colour.
      const odd = !c.color && choices.length % style.palette.length === 1 && i === choices.length - 1 && i > 0;
      const colour = odd ? style.palette[1 % style.palette.length] : fill;
      rotor.appendChild(el(doc, 'path', { d: wedgePath(cx, 214, from, to), fill: colour, stroke: style.edge, 'stroke-width': 2, class: 'wedge' }));
      const text = shortLabel(c.label, to - from);
      if (text) {
        const mid = (from + to) / 2;
        const [x, y] = polar(cx, 140, mid);
        const t = el(doc, 'text', {
          x: x.toFixed(2), y: y.toFixed(2), 'text-anchor': 'middle', 'dominant-baseline': 'middle', fill: inkFor(colour),
          'font-size': to - from < 14 ? 12 : to - from < 30 ? 16 : 20, 'font-weight': 800,
          transform: `rotate(${(mid - 90).toFixed(2)} ${x.toFixed(2)} ${y.toFixed(2)})`,
        });
        if (style.labelStroke) {
          t.setAttribute('stroke', style.labelStroke);
          t.setAttribute('stroke-width', '3');
          t.setAttribute('paint-order', 'stroke');
        }
        t.textContent = text;
        rotor.appendChild(t);
      }
    });
    svg.appendChild(rotor);
    for (let i = 0; i < 24; i++) {
      const [x, y] = polar(cx, 232, i * 15);
      svg.appendChild(el(doc, 'circle', { cx: x.toFixed(2), cy: y.toFixed(2), r: 4.5, class: 'stud' }));
    }
    svg.appendChild(el(doc, 'circle', { cx, cy: cx, r: 40, class: 'hub' }));
    return { svg, rotor };
  }

  const send = bridge(win, {
    'session.snapshot'(p) {
      apply(p.settings);
      reduced = p.reducedMotion === true;
    },
    'theme.settings.updated'(p) {
      apply(p.settings);
    },
    'spin.start'(p) {
      const id = typeof p.spinId === 'string' ? p.spinId : '';
      const choices = cleanChoices(p.choices);
      const land = landing(choices, p.winner, p.draw);
      if (!id || land === null) {
        send('theme.error', { spinId: id, code: 'bad_spin' });
        return;
      }
      const my = ++gen;
      spinId = id;
      result.hidden = true;
      header.textContent = settings.title || String(p.title || '').slice(0, 70);
      header.hidden = !settings.show_title || !header.textContent;
      const { svg, rotor } = draw(choices);
      svg.setAttribute('aria-label', header.textContent || 'Wheel');
      const wrap = doc.createElement('div');
      wrap.className = 'wrap';
      const pointer = doc.createElement('div');
      pointer.className = 'pointer';
      wrap.append(svg, pointer);
      stageEl.replaceChildren(wrap);
      const end = TURNS * 360 + (360 - land);
      const ms = Math.max(1000, Math.min(30000, Number(p.durationMs) || 6000));
      if (reduced) {
        rotor.style.transform = `rotate(${end}deg)`;
        send('spin.animation.completed', { spinId: id });
        return;
      }
      rotor.style.transform = 'rotate(0deg)';
      void svg.getBoundingClientRect();
      rotor.style.transition = `transform ${ms}ms cubic-bezier(0.12, 0.7, 0.12, 1)`;
      rotor.style.transform = `rotate(${end}deg)`;
      later(() => {
        if (my === gen) send('spin.animation.completed', { spinId: id });
      }, ms);
    },
    'winner.reveal'(p) {
      if (p.spinId !== spinId) return;
      result.textContent = `🎉 ${String(p.label || '').slice(0, 120)}`;
      result.hidden = false;
    },
    'spin.cancel'() {
      gen++;
      spinId = '';
      stageEl.replaceChildren();
      header.textContent = '';
      result.hidden = true;
    },
  });
  sayReady(win, send, later);
  return { send };
}
