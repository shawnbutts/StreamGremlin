import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import PretzelCard from './PretzelCard.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { PretzelSong } from '../lib/api';

let comp: ReturnType<typeof mount> | undefined;

afterEach(() => {
  if (comp) unmount(comp);
  comp = undefined;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
});

const button = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === text)!;

function start() {
  const target = document.createElement('main');
  document.body.appendChild(target);
  comp = mount(PretzelCard, { target });
  return target;
}

describe('Pretzel on Connections', () => {
  it('says it has not checked, then checks and shows the song, or why it cannot', async () => {
    let next: PretzelSong = { state: 'playing', title: 'Caffeine Part 2', text: 'Caffeine Part 2', checked_at: new Date().toISOString() };
    const calls = mockFetch({
      '/v1/pretzel': () => ({ status: 200, body: { song: { state: 'unknown', why: 'not checked yet', checked_at: '0001-01-01T00:00:00Z' } } }),
      '/v1/pretzel/check': () => ({ status: 200, body: next }),
    });
    const el = start();
    await settle(30);
    expect(el.textContent).toContain('Not checked yet.');
    expect(el.textContent).toContain('Premium or Pro');
    const r = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
    expect(r.violations.map((v) => v.id)).toEqual([]);

    button(el, "Check what's playing").click();
    await settle(30);
    expect(el.textContent).toContain('Playing Caffeine Part 2.');
    expect(el.querySelectorAll('.state strong')).toHaveLength(1); // no artist named
    expect(calls.some((c) => c.method === 'POST' && c.path === '/v1/pretzel/check')).toBe(true);

    next = { state: 'not_using', why: "Pretzel doesn't know your Twitch account: sign in to Pretzel with it", checked_at: new Date().toISOString() };
    button(el, "Check what's playing").click();
    await settle(30);
    flushSync();
    expect(el.textContent).toContain("Unknown: Pretzel doesn't know your Twitch account");
    expect(el.textContent).not.toContain('Playing Caffeine');
  });
});
