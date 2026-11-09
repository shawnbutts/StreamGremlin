import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Rewards from './Rewards.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { Redemption, Reward, RewardList } from '../lib/api';

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
  comp = mount(Rewards, { target, props: { refreshMs: 60000 } });
  return target;
}

const button = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === text || b.getAttribute('aria-label') === text);

const reward = (r: Partial<Reward>): Reward => ({
  id: 'r1', title: 'Group hug', prompt: '', cost: 500, background_color: '#9147FF', enabled: true, paused: false, in_stock: true,
  input_required: false, skip_queue: false, max_per_stream: 0, max_per_user_per_stream: 0, cooldown_seconds: 0, managed: true, ...r,
});

function engine(state: RewardList['state'], rewards: Reward[], opts: { canManage?: boolean } = {}) {
  const list = [...rewards];
  const queue: Redemption[] = [
    { id: 'd1', reward_id: 'r1', reward: 'Group hug', cost: 500, user_login: 'fan', user_name: 'Fan', input: 'everyone', status: 'unfulfilled', redeemed_at: new Date().toISOString() },
  ];
  const routes: Parameters<typeof mockFetch>[0] = {
    '/v1/rewards': (method, body) => {
      if (method === 'POST') {
        const r = reward({ ...(body as Partial<Reward>), id: 'r9' });
        list.push(r);
        return { status: 201, body: r };
      }
      return {
        status: 200,
        body: { state, detail: state === 'unknown' ? 'couldn’t reach Twitch' : undefined, rewards: state === 'ok' ? list : [], can_manage: opts.canManage ?? true, max: 50 },
      };
    },
    '/v1/rewards/r1': (method, body) => {
      if (method === 'DELETE') {
        list.splice(0, 1);
        return { status: 200, body: { deleted: true } };
      }
      list[0] = { ...list[0], ...(body as Partial<Reward>) };
      return { status: 200, body: list[0] };
    },
    '/v1/rewards/r1/redemptions': () => ({ status: 200, body: { redemptions: queue } }),
    '/v1/rewards/r1/redemptions/d1': (_m, body) => {
      queue.splice(0, 1);
      const action = (body as { action: string }).action;
      return { status: 200, body: { redemption: { id: 'd1', user_name: 'Fan', cost: 500, status: action === 'done' ? 'fulfilled' : 'canceled' }, repeated: false } };
    },
  };
  return mockFetch(routes);
}

async function noViolations(label: string) {
  const r = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
  expect(r.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.html).join(', ')}`), label).toEqual([]);
}

describe('Rewards', () => {
  it('says plainly when the channel has no channel points, and offers nothing to change', async () => {
    engine('no_channel_points', []);
    const el = start();
    await settle();
    expect(el.textContent).toContain('Twitch gives channel points to Affiliates and Partners');
    expect(el.textContent).not.toContain('Make a reward');
    await noViolations('no channel points');
  });

  it('asks to sign in, or to sign in again for the permission, or says unknown', async () => {
    engine('not_signed_in', []);
    let el = start();
    await settle();
    expect(el.textContent).toContain('Sign in with your Twitch account');
    unmount(comp!);
    document.body.innerHTML = '';
    engine('needs_permission', []);
    el = start();
    await settle();
    expect(el.textContent).toContain('Sign in to Twitch again');
    unmount(comp!);
    document.body.innerHTML = '';
    engine('unknown', []);
    el = start();
    await settle();
    expect(el.textContent).toContain('Unknown: couldn’t reach Twitch');
  });

  it('lists rewards: StreamGremlin’s can be changed, Twitch’s are trigger only and can be copied', async () => {
    const calls = engine('ok', [reward({ cooldown_seconds: 60 }), reward({ id: 'r2', title: 'Hydrate', cost: 100, managed: false })]);
    const el = start();
    await settle();
    expect(el.textContent).toContain('500 points · On · Made in StreamGremlin');
    expect(el.textContent).toContain('cooldown 60s');
    expect(el.textContent).toContain('Made on Twitch: trigger only');
    expect(button(el, 'Edit Hydrate')).toBeUndefined();
    await noViolations('the list');
    button(el, 'Pause Group hug')!.click();
    await settle();
    expect(calls.find((c) => c.method === 'PUT')).toMatchObject({ path: '/v1/rewards/r1', body: { paused: true } });
    expect(el.textContent).toContain('Paused “Group hug”.');
    button(el, 'Make a copy of Hydrate')!.click();
    flushSync();
    expect(el.querySelector<HTMLInputElement>('input[placeholder="Hydrate"]')!.value).toBe('Hydrate');
    expect(el.textContent).toContain('Twitch needs a different title');
  });

  it('makes a reward, checking it first', async () => {
    const calls = engine('ok', []);
    const el = start();
    await settle();
    const title = el.querySelector<HTMLInputElement>('input[placeholder="Hydrate"]')!;
    const typed = (i: HTMLInputElement, v: string) => {
      i.value = v;
      i.dispatchEvent(new Event('input', { bubbles: true }));
      flushSync();
    };
    button(el, 'Make it')!.click();
    await settle();
    expect(el.textContent).toContain('A title is 1 to 45 characters.');
    typed(title, 'Song request');
    const input = Array.from(el.querySelectorAll('label')).find((l) => l.textContent?.includes('Viewers type'))!.querySelector('input')!;
    input.click();
    flushSync();
    button(el, 'Make it')!.click();
    await settle();
    expect(el.textContent).toContain('Say what viewers should type');
    typed(el.querySelector<HTMLInputElement>('input[placeholder="Remind me to drink water"]')!, 'Which song?');
    button(el, 'Make it')!.click();
    await settle();
    expect(calls.find((c) => c.method === 'POST')!.body).toMatchObject({ title: 'Song request', cost: 100, prompt: 'Which song?', input_required: true, enabled: true });
    expect(el.textContent).toContain('Made “Song request”.');
  });

  it('shows the queue, marks one done and refunds, and deletes after asking', async () => {
    const calls = engine('ok', [reward({})]);
    const el = start();
    await settle();
    button(el, 'Queue of Group hug')!.click();
    await settle();
    expect(el.textContent).toContain('Waiting: Group hug');
    expect(el.textContent).toContain('“everyone”');
    button(el, 'Refund Fan')!.click();
    await settle();
    expect(calls.find((c) => c.path === '/v1/rewards/r1/redemptions/d1')!.body).toEqual({ action: 'refund' });
    expect(el.textContent).toContain('Refunded Fan’s 500 points.');
    expect(el.textContent).toContain('Nothing waiting.');
    button(el, 'Delete Group hug')!.click();
    flushSync();
    const dialog = document.querySelector('[role="alertdialog"]')!;
    expect(dialog.textContent).toContain('marks any redemptions still waiting as done');
    button(dialog, 'Delete')!.click();
    await settle();
    expect(calls.some((c) => c.method === 'DELETE' && c.path === '/v1/rewards/r1')).toBe(true);
  });

  it('offers no changes when the sign-in lacks the permission', async () => {
    engine('ok', [reward({})], { canManage: false });
    const el = start();
    await settle();
    expect(el.textContent).toContain('To make and change rewards, sign in to Twitch again');
    expect(button(el, 'Pause Group hug')).toBeUndefined();
    expect(el.textContent).not.toContain('Make a reward');
  });
});
