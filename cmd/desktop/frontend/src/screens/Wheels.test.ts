import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Wheels from './Wheels.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { Wheel, WheelSpin } from '../lib/api';

let comp: ReturnType<typeof mount> | undefined;

afterEach(() => {
  if (comp) unmount(comp);
  comp = undefined;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
});

function start() {
  const target = document.createElement('main');
  document.body.appendChild(target);
  comp = mount(Wheels, { target, props: { refreshMs: 60000 } });
  return target;
}

const button = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === text);
const field = (el: ParentNode, label: string) =>
  Array.from(el.querySelectorAll('label')).find((l) => l.querySelector('span')?.textContent?.trim() === label)!.querySelector('input') as HTMLInputElement;
const typed = (i: HTMLInputElement, v: string) => {
  i.value = v;
  i.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
};

const now = new Date().toISOString();
const games: Wheel = {
  name: 'next-game', title: 'Next game', spin_seconds: 6, version: 1, created_at: now, updated_at: now,
  choices: [{ label: 'Mario', weight: 2 }, { label: 'Zelda', weight: 1 }, { label: 'Tetris', weight: 1 }],
  odds: [
    { label: 'Mario', weight: 2, total: 4, percent: 50 },
    { label: 'Zelda', weight: 1, total: 4, percent: 25 },
    { label: 'Tetris', weight: 1, total: 4, percent: 25 },
  ],
};
const spun = (over: Partial<WheelSpin> = {}): WheelSpin => ({
  id: 's1', wheel: 'next-game', title: 'Next game', wheel_version: 1, choices: games.choices, total: 4, draw: 2, winner: 1, label: 'Zelda',
  started_by: 'the app', spun_at: now, seconds: 6, ...over,
});

describe('Wheels', () => {
  it('lists wheels with their exact odds, and adds one with the chance worked out as you type', async () => {
    const calls = mockFetch({
      '/v1/wheels': (m) => (m === 'POST' ? { status: 201, body: games } : { status: 200, body: { wheels: [games], spins: [], pages: 0 } }),
    });
    const el = start();
    await settle(30);
    expect(el.textContent).toContain('Next game');
    expect(el.textContent).toContain('50%');
    expect(el.textContent).toContain('No wheel overlay is open in OBS');
    button(el, 'Add a wheel')!.click();
    flushSync();
    typed(field(el, 'Name'), 'coin');
    typed(field(el, 'Title'), 'Coin');
    typed(field(el, 'Choice 1'), 'Heads');
    typed(field(el, 'Choice 2'), 'Tails');
    const weights = Array.from(el.querySelectorAll<HTMLInputElement>('.weight-field input'));
    typed(weights[1], '3');
    expect(el.querySelector('[aria-label="Choice 2\'s chance"]')?.textContent).toBe('75%');
    (el.querySelector('form') as HTMLFormElement).requestSubmit();
    await settle(30);
    expect(calls.find((c) => c.method === 'POST')!.body).toMatchObject({
      name: 'coin', title: 'Coin', choices: [{ label: 'Heads', weight: 1 }, { label: 'Tails', weight: 3 }], spin_seconds: 6,
    });
    const r = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
    expect(r.violations.map((v) => v.id)).toEqual([]);
  });

  it('spins, hides the result until the wheel stops, then says it, and checks the spin', async () => {
    let spins: WheelSpin[] = [];
    const calls = mockFetch({
      '/v1/wheels': () => ({ status: 200, body: { wheels: [games], spins, pages: 1 } }),
      '/v1/wheels/next-game/spin': () => {
        spins = [spun()];
        return { status: 202, body: spins[0] };
      },
      '/v1/spins/s1': () => ({ status: 200, body: spins[0] }),
    });
    const el = start();
    await settle(30);
    button(el, 'Spin')!.click();
    await settle(30);
    expect(calls.find((c) => c.path === '/v1/wheels/next-game/spin')!.body).toHaveProperty('request_id');
    expect(el.textContent).toContain('spinning on your wheel overlay');
    expect(el.textContent).toContain('spinning…');
    expect(el.textContent).not.toContain('Zelda as recorded');
    expect(button(el, 'Check the spin')).toBeUndefined();
    spins = [spun({ settled_at: now, display: 'shown' })];
    comp && unmount(comp);
    document.body.innerHTML = '';
    const el2 = start();
    await settle(30);
    expect(el2.textContent).toContain('Next game: Zelda');
    expect(el2.textContent).toContain('shown on the wheel overlay');
    button(el2, 'Check the spin')!.click();
    flushSync();
    expect(el2.querySelector('.state-ok')?.textContent).toContain('Number 3 of 4 lands on Zelda, as recorded.');
    const r = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
    expect(r.violations.map((v) => v.id)).toEqual([]);
  });

  it('asks before deleting', async () => {
    const calls = mockFetch({
      '/v1/wheels': () => ({ status: 200, body: { wheels: [games], spins: [], pages: 0 } }),
      '/v1/wheels/next-game': () => ({ status: 200, body: { deleted: true } }),
    });
    const el = start();
    await settle(30);
    button(el, 'Delete')!.click();
    flushSync();
    expect(document.body.textContent).toContain('Its spins are kept.');
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);
    const confirm = Array.from(document.body.querySelectorAll<HTMLButtonElement>('button')).filter((b) => b.textContent?.trim() === 'Delete');
    confirm[confirm.length - 1].click();
    await settle(30);
    expect(calls.some((c) => c.method === 'DELETE' && c.path === '/v1/wheels/next-game')).toBe(true);
  });
});
