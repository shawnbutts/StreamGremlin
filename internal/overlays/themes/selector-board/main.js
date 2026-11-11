// The Selector board look: not a wheel, a list that rolls to the result.
// Each choice shows its chance, so the board never misstates the odds
// (docs/adr/0036-packages-plugins-and-themes.md). It never picks: it
// stops on the winner it's sent, and shows the result when told.
import { bridge, sayReady } from '../shared/bridge.js';
import { HEX, chances, cleanChoices, landing } from '../shared/wheel.js';

export const ROW = 90; // px per row, margin included
const LOOPS = 4;

export function selectorTheme(win, opts = {}) {
  const doc = win.document;
  const later = opts.setTimeout || ((fn, ms) => win.setTimeout(fn, ms));
  const app = doc.getElementById('app');
  const settings = { title: '', show_title: true };
  let reduced = false;
  let spinId = '';
  let gen = 0;

  const header = doc.createElement('div');
  header.className = 'title';
  const board = doc.createElement('div');
  board.className = 'board';
  const result = doc.createElement('div');
  result.className = 'result';
  result.setAttribute('role', 'status');
  result.hidden = true;
  app.append(header, board, result);

  function apply(s) {
    if (!s || typeof s !== 'object') return;
    if (typeof s.title === 'string') settings.title = s.title.slice(0, 70);
    if (typeof s.show_title === 'boolean') settings.show_title = s.show_title;
    if (typeof s.accent === 'string' && HEX.test(s.accent)) doc.documentElement.style.setProperty('--accent', s.accent);
  }

  function row(c, chance, current) {
    const r = doc.createElement('div');
    r.className = current ? 'row current' : 'row';
    const name = doc.createElement('span');
    name.className = 'name';
    name.textContent = c.label;
    const odds = doc.createElement('span');
    odds.className = 'chance';
    odds.textContent = chance;
    r.append(name, odds);
    return r;
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
      const winner = Number(p.winner);
      if (!id || landing(choices, winner, p.draw) === null) {
        send('theme.error', { spinId: id, code: 'bad_spin' });
        return;
      }
      const my = ++gen;
      spinId = id;
      result.hidden = true;
      header.textContent = settings.title || String(p.title || '').slice(0, 70);
      header.hidden = !settings.show_title || !header.textContent;
      const odds = chances(choices);
      // The list a few times over, ending on the winner in the last pass.
      const track = doc.createElement('div');
      track.className = 'track';
      const stop = LOOPS * choices.length + winner;
      for (let k = 0; k <= LOOPS; k++) choices.forEach((c, i) => track.appendChild(row(c, odds[i], k === LOOPS && i === winner)));
      const band = doc.createElement('div');
      band.className = 'band';
      const view = doc.createElement('div');
      view.className = 'view';
      view.appendChild(track);
      board.replaceChildren(view, band);
      const at = (n) => `translateY(${-(n * ROW + ROW / 2)}px)`;
      const ms = Math.max(1000, Math.min(30000, Number(p.durationMs) || 6000));
      if (reduced) {
        track.style.transform = at(stop);
        send('spin.animation.completed', { spinId: id });
        return;
      }
      track.style.transform = at(winner);
      void track.getBoundingClientRect();
      track.style.transition = `transform ${ms}ms cubic-bezier(0.1, 0.75, 0.12, 1)`;
      track.style.transform = at(stop);
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
      board.replaceChildren();
      header.textContent = '';
      result.hidden = true;
    },
  });
  sayReady(win, send, later);
  return { send };
}

if (typeof window !== 'undefined' && !window.__streamgremlinThemeTest) selectorTheme(window);
