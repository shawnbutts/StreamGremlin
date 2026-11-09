import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Giveaways from './Giveaways.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { GiveawayDetail, GiveawayEntry } from '../lib/api';
import { fingerprint } from '../lib/giveaways';

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
  comp = mount(Giveaways, { target, props: { refreshMs: 60000 } });
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
const entry = (seq: number, name: string): GiveawayEntry => ({
  seq, platform: 'twitch', user_id: `u${seq}`, login: name.toLowerCase(), display_name: name, entered_at: new Date().toISOString(),
});

async function detail(over: Partial<GiveawayDetail> = {}): Promise<GiveawayDetail> {
  const entries = [entry(1, 'Ann'), entry(2, 'Bob'), entry(3, 'Cat')];
  return {
    id: 'g1', title: 'a Steam key', command: 'join', winners: 1, claim_seconds: 0, subscribers_only: false, practice: false, state: 'open',
    opened_by: 'the app', opened_at: new Date().toISOString(), entry_count: 3, entries, draws: [], refusals: [],
    messages: [
      { id: 1, text: '🎉 Giveaway: a Steam key! Type !join in chat to enter.', state: 'sent', created_at: '' },
      { id: 2, text: '@ann, @bob: you’re in', state: 'failed', detail: 'sign in with your Twitch account', created_at: '' },
    ],
    ...over,
    fingerprint: over.state && over.state !== 'open' ? await fingerprint(entries) : '',
  };
}

describe('Giveaways', () => {
  it('opens a giveaway from the form', async () => {
    const calls = mockFetch({
      '/v1/giveaways': (m) => (m === 'POST' ? { status: 201, body: {} } : { status: 200, body: { current: null, recent: [] } }),
    });
    const el = start();
    await settle(30);
    typed(field(el, 'What’s being given away'), 'a Steam key');
    typed(field(el, 'Winners'), '2');
    const claim = Array.from(el.querySelectorAll<HTMLInputElement>('input[type=checkbox]'))[0];
    claim.click();
    flushSync();
    typed(field(el, 'Seconds to claim'), '45');
    (el.querySelector('form') as HTMLFormElement).requestSubmit();
    await settle(30);
    const sent = calls.find((c) => c.method === 'POST')!.body as Record<string, unknown>;
    expect(sent).toMatchObject({ title: 'a Steam key', command: 'join', winners: 2, claim_seconds: 45, practice: false });
    expect(el.textContent).toContain('chat has been told how to enter');
    const r = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
    expect(r.violations.map((v) => v.id)).toEqual([]);
  });

  it('shows entries and what chat heard, draws, and checks the draw', async () => {
    let d = await detail();
    const calls = mockFetch({
      '/v1/giveaways': () => ({ status: 200, body: { current: d, recent: [] } }),
      '/v1/giveaways/g1/draw': () => {
        d = { ...d, state: 'closed' };
        return { status: 200, body: { draws: [] } };
      },
    });
    const el = start();
    await settle(30);
    expect(el.textContent).toContain('Taking entries');
    expect(el.textContent).toContain('3 entries');
    expect(el.textContent).toContain('Not sent: sign in with your Twitch account');
    button(el, 'Draw a winner')!.click();
    await settle(30);
    expect(calls.some((c) => c.path === '/v1/giveaways/g1/draw')).toBe(true);

    d = await detail({ state: 'closed' });
    d.draws = [{ id: 'd1', n: 1, entry_seq: 2, pick_index: 1, candidates: 3, outcome: 'won', drawn_at: '', winner: d.entries[1] }];
    comp && unmount(comp);
    document.body.innerHTML = '';
    const el2 = start();
    await settle(30);
    expect(el2.textContent).toContain('Bob: Won');
    expect(el2.textContent).toContain('Entry 2, number 2 of the 3 not yet drawn');
    button(el2, 'Check the draws')!.click();
    await settle(30);
    expect(el2.querySelector('[role=status].state-ok')?.textContent).toContain('matches its fingerprint');
    // A record that doesn't add up is said plainly.
    d.draws[0] = { ...d.draws[0], pick_index: 0 };
    comp && unmount(comp);
    document.body.innerHTML = '';
    const el3 = start();
    await settle(30);
    button(el3, 'Check the draws')!.click();
    await settle(30);
    expect(el3.querySelector('[role=alert]')?.textContent).toContain('Draw 1');
    const r = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
    expect(r.violations.map((v) => v.id)).toEqual([]);
  });

  it('asks before setting a winner aside or cancelling', async () => {
    const d = await detail({ state: 'closed' });
    d.draws = [{ id: 'd1', n: 1, entry_seq: 1, pick_index: 0, candidates: 3, outcome: 'pending_claim', claim_until: new Date(Date.now() + 30000).toISOString(), drawn_at: '', winner: d.entries[0] }];
    const calls = mockFetch({
      '/v1/giveaways': () => ({ status: 200, body: { current: d, recent: [] } }),
      '/v1/giveaways/g1/set-aside': () => ({ status: 200, body: { draws: [] } }),
      '/v1/giveaways/g1/cancel': () => ({ status: 200, body: {} }),
    });
    const el = start();
    await settle(30);
    expect(el.textContent).toMatch(/Can claim: (29|30) seconds left/);
    expect(button(el, 'End the giveaway')!.disabled).toBe(true);
    button(el, 'Set aside and draw again')!.click();
    flushSync();
    expect(calls.some((c) => c.path.endsWith('set-aside'))).toBe(false);
    button(document.querySelector('[role=alertdialog]')!, 'Set aside and draw again')!.click();
    await settle(30);
    expect(calls.find((c) => c.path.endsWith('set-aside'))!.body).toEqual({ draw_id: 'd1' });
    button(el, 'Cancel')!.click();
    flushSync();
    button(document.querySelector('[role=alertdialog]')!, 'Cancel the giveaway')!.click();
    await settle(30);
    expect(calls.some((c) => c.path.endsWith('/cancel'))).toBe(true);
  });

  it('shows practice plainly, and earlier giveaways with their winners', async () => {
    const past = await detail({ id: 'g0', state: 'ended' });
    past.draws = [{ id: 'd', n: 1, entry_seq: 3, pick_index: 2, candidates: 3, outcome: 'claimed', drawn_at: '', winner: past.entries[2] }];
    const cur = { ...(await detail({ practice: true })), messages: [{ id: 1, text: 'hi', state: 'practice' as const, created_at: '' }] };
    mockFetch({
      '/v1/giveaways': () => ({ status: 200, body: { current: cur, recent: [past] } }),
      '/v1/giveaways/g0': () => ({ status: 200, body: past }),
    });
    const el = start();
    await settle(40);
    expect(el.textContent).toContain('Practice: only test chat commands from the Events screen enter');
    button(el, 'Show')!.click();
    await settle(30);
    expect(el.textContent).toContain('Cat: Claimed');
  });
});
