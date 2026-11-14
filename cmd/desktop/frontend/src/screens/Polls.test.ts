import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Polls from './Polls.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { Poll, PollsView, Prediction } from '../lib/api';

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
  comp = mount(Polls, { target, props: { refreshMs: 60000 } });
  return target;
}

const button = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === text);
const typed = (i: HTMLInputElement, v: string) => {
  i.value = v;
  i.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
};
const field = (el: ParentNode, label: string) =>
  Array.from(el.querySelectorAll('label')).find((l) => l.querySelector('span')?.textContent?.trim() === label)!.querySelector('input, select') as HTMLInputElement;

const poll = (over: Partial<Poll> = {}): Poll => ({
  id: 'p1', title: 'What next?', status: 'active', seconds: 60, channel_points_per_vote: 0, started_at: new Date().toISOString(),
  choices: [{ id: 'a', title: 'Celeste', votes: 1, channel_points_votes: 0 }, { id: 'b', title: 'Hades', votes: 3, channel_points_votes: 0 }],
  ...over,
});
const prediction = (over: Partial<Prediction> = {}): Prediction => ({
  id: 'r1', title: 'Win?', status: 'active', seconds: 120, created_at: new Date().toISOString(),
  outcomes: [{ id: 'y', title: 'Yes', color: 'blue', users: 4, channel_points: 1200 }, { id: 'n', title: 'No', color: 'pink', users: 2, channel_points: 800 }],
  ...over,
});

function engine(v: PollsView) {
  const view = { ...v };
  return mockFetch({
    '/v1/polls': (method, body) => {
      if (method === 'POST') {
        view.poll = poll({ title: (body as { title: string }).title });
        return { status: 201, body: view.poll };
      }
      return { status: 200, body: view };
    },
    '/v1/polls/end': () => {
      view.poll = poll({ status: 'terminated' });
      return { status: 200, body: view.poll };
    },
    '/v1/predictions': () => ({ status: 201, body: prediction() }),
    '/v1/predictions/end': (_m, body) => {
      const how = (body as { how: string }).how;
      view.prediction = prediction({ status: how === 'cancel' ? 'canceled' : how === 'lock' ? 'locked' : 'resolved', winning_outcome_id: how === 'resolve' ? 'n' : undefined });
      return { status: 200, body: { prediction: view.prediction, repeated: false } };
    },
  });
}

async function noViolations(label: string) {
  const r = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
  expect(r.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.html).join(', ')}`), label).toEqual([]);
}

describe('Polls and predictions', () => {
  it('says plainly when the channel can’t run them, and offers nothing to start', async () => {
    engine({ state: 'no_channel_points', poll: null, prediction: null });
    const el = start();
    await settle();
    expect(el.textContent).toContain('Twitch gives polls and predictions to Affiliates and Partners');
    expect(el.querySelector('form[aria-label="Start a poll"]')).toBeNull();
    await noViolations('not affiliate');
  });

  it('shows the running poll with its votes, and ends it', async () => {
    const calls = engine({ state: 'ok', poll: poll(), prediction: null });
    const el = start();
    await settle();
    expect(el.textContent).toContain('What next?');
    expect(el.textContent).toContain('3 votes (75%)');
    expect(el.querySelector('form[aria-label="Start a poll"]')).toBeNull(); // one at a time
    await noViolations('running');
    button(el, 'End and show results')!.click();
    await settle();
    expect(calls.find((c) => c.path === '/v1/polls/end')!.body).toEqual({ archive: false });
    expect(el.textContent).toContain('Ended early');
    expect(el.querySelector('form[aria-label="Start a poll"]')).not.toBeNull();
  });

  it('starts a poll, checking it first', async () => {
    const calls = engine({ state: 'ok', poll: null, prediction: null });
    const el = start();
    await settle();
    button(el, 'Start the poll')!.click();
    await settle();
    expect(el.textContent).toContain('A poll’s question is 1 to 60 characters.');
    typed(field(el, 'Question'), 'What next?');
    typed(field(el, 'Choice 1'), 'Celeste');
    button(el, 'Start the poll')!.click();
    await settle();
    expect(el.textContent).toContain('A poll needs at least 2 choices.');
    typed(field(el, 'Choice 2'), 'Hades');
    button(el, '+ Another choice')!.click();
    flushSync();
    typed(field(el, 'Choice 3'), 'Tunic');
    button(el, 'Start the poll')!.click();
    await settle();
    expect(calls.find((c) => c.method === 'POST' && c.path === '/v1/polls')!.body).toEqual({
      title: 'What next?', choices: ['Celeste', 'Hades', 'Tunic'], seconds: 60, channel_points_per_vote: 0,
    });
    expect(el.textContent).toContain('The poll started in your chat.');
  });

  it('locks, then resolves a prediction after asking', async () => {
    const calls = engine({ state: 'ok', poll: null, prediction: prediction() });
    const el = start();
    await settle();
    expect(el.textContent).toContain('4 viewers, 1,200 points');
    button(el, 'Lock bets')!.click();
    await settle();
    expect(el.textContent).toContain('Locked: no more bets');
    const winner = field(el, 'Winner') as unknown as HTMLSelectElement;
    winner.value = 'No';
    winner.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();
    button(el, 'Resolve')!.click();
    flushSync();
    const dialog = document.querySelector('[role="alertdialog"]')!;
    expect(dialog.textContent).toContain('paid out');
    button(dialog, 'Resolve')!.click();
    await settle();
    expect(calls.filter((c) => c.path === '/v1/predictions/end').map((c) => c.body)).toEqual([{ how: 'lock', winner: '' }, { how: 'resolve', winner: 'No' }]);
    expect(el.textContent).toContain('✓ won');
  });

  it('cancels a prediction after asking', async () => {
    const calls = engine({ state: 'ok', poll: null, prediction: prediction() });
    const el = start();
    await settle();
    button(el, 'Cancel and refund')!.click();
    flushSync();
    const dialog = document.querySelector('[role="alertdialog"]')!;
    expect(dialog.textContent).toContain('Everyone gets their channel points back');
    button(dialog, 'Cancel and refund')!.click();
    await settle();
    expect(calls.find((c) => c.path === '/v1/predictions/end')!.body).toEqual({ how: 'cancel', winner: '' });
    expect(el.textContent).toContain('Canceled and refunded');
  });

  it('runs a YouTube poll in the chat being read, with its votes, and ends it', async () => {
    let ytPoll: Record<string, unknown> | undefined;
    const calls = mockFetch({
      '/v1/polls': () => ({ status: 200, body: { state: 'ok', poll: null, prediction: null } }),
      '/v1/connections/youtube': () => ({
        status: 200,
        body: { chat: { state: 'connected', since: new Date().toISOString(), broadcast: { id: 'b', title: 'Stream' }, poll: ytPoll } },
      }),
      '/v1/youtube/polls': (_m, body) => {
        const b = body as { question: string; options: string[] };
        ytPoll = { id: 'yp', question: b.question, status: 'active', choices: b.options.map((t, i) => ({ title: t, votes: i * 2 })) };
        return { status: 201, body: ytPoll };
      },
      '/v1/youtube/polls/end': () => {
        ytPoll = { ...ytPoll, status: 'closed' };
        return { status: 200, body: ytPoll };
      },
    });
    const el = start();
    await settle();
    const form = el.querySelector('form[aria-label="Start a YouTube poll"]')!;
    expect(form).not.toBeNull();
    expect(el.textContent).toContain('20 of today’s YouTube quota units');
    form.querySelector<HTMLButtonElement>('button[type="submit"]')!.click();
    await settle();
    expect(el.textContent).toContain('A question is 1 to 100 characters.');
    typed(field(form, 'Question'), 'Snack?');
    typed(field(form, 'Choice 1'), 'Chips');
    typed(field(form, 'Choice 2'), 'Fruit');
    form.querySelector<HTMLButtonElement>('button[type="submit"]')!.click();
    await settle();
    expect(calls.find((c) => c.path === '/v1/youtube/polls')?.body).toEqual({ question: 'Snack?', options: ['Chips', 'Fruit'] });
    expect(el.textContent).toContain('The poll started in your YouTube chat.');
    expect(el.textContent).toContain('2 votes (100%)');
    expect(el.querySelector('form[aria-label="Start a YouTube poll"]')).toBeNull(); // one at a time
    await noViolations('youtube poll');
    button(el, 'End the poll')!.click();
    await settle();
    expect(calls.some((c) => c.path === '/v1/youtube/polls/end' && c.method === 'POST')).toBe(true);
    expect(el.textContent).toContain('Ended');
  });

  it('hides YouTube polls until YouTube is set up', async () => {
    engine({ state: 'ok', poll: null, prediction: null });
    const el = start();
    await settle();
    expect(el.querySelector('#yt-poll-heading')).toBeNull();
  });
});
