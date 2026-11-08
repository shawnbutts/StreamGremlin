import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Workflows from './Workflows.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { Job, WorkflowSummary } from '../lib/api';

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
  comp = mount(Workflows, { target, props: { refreshMs: 20 } });
  return target;
}

const button = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === text);

const catalog = {
  kinds: [
    {
      type: 'chat.command.received',
      label: 'Chat command',
      description: '',
      sources: ['twitch'],
      sensitive: true,
      fields: [
        { name: 'user', label: 'Viewer', kind: 'text', default: 'TestViewer', max: 25 },
        { name: 'command', label: 'Command', kind: 'text', default: '!hello', max: 50 },
      ],
    },
  ],
};

function engine() {
  const wfs: WorkflowSummary[] = [];
  const jobs: Job[] = [];
  const calls = mockFetch({
    '/v1/workflows': () => ({ status: 200, body: { workflows: wfs } }),
    '/v1/jobs': () => ({ status: 200, body: { jobs } }),
    '/v1/actions': () => ({
      status: 200,
      body: { actions: [{ name: 'chat.send', label: 'Send a chat message', description: '', group: 'Twitch', external: true, params: [{ name: 'message', label: 'Message', type: 'text', required: true }] }] },
    }),
    '/v1/events/catalog': () => ({ status: 200, body: catalog }),
    '/v1/workflows/chat.hello': (_m, body) => {
      const d = body as { id: string; name: string; steps: { action: string }[] };
      if (d.steps?.some((s) => s.action === 'chat.shout')) {
        return { status: 400, body: { error: { code: 'invalid_request', message: 'the workflow has problems' }, problems: [{ path: '$.steps[0].action', message: 'there\'s no action "chat.shout"' }] } };
      }
      const w: WorkflowSummary = { id: d.id, name: d.name, enabled: false, version: 1, updated_at: new Date().toISOString(), triggers: [{ type: 'chat.command.received', where: { command: 'hello' } }] };
      wfs.splice(0, wfs.length, w);
      return { status: 200, body: { workflow: w, changed: true } };
    },
    '/v1/workflows/chat.hello/enabled': (_m, body) => {
      wfs[0] = { ...wfs[0], enabled: (body as { enabled: boolean }).enabled };
      return { status: 200, body: wfs[0] };
    },
    '/v1/events/simulate': (_m, body) => {
      jobs.unshift({ id: 'job-1', workflow_id: 'chat.hello', version: 1, event_id: 'e', event_seq: 1, status: 'succeeded', dry_run: true, created_at: new Date().toISOString() });
      wfs[0] = { ...wfs[0], last_job: jobs[0] };
      return { status: 201, body: { event: { seq: 1 }, duplicate: false, sent: body } };
    },
    '/v1/jobs/job-1': () => ({
      status: 200,
      body: {
        job: jobs[0],
        steps: [{ path: 'steps[0]', kind: 'action', action: 'chat.send', status: 'skipped', reason: 'dry run: a test event never reaches Twitch', input: { message: 'Hi TestViewer!' }, output: { dry_run: true }, started_at: new Date().toISOString(), finished_at: new Date().toISOString() }],
      },
    }),
  });
  return { calls, wfs };
}

async function type(el: HTMLElement, text: string) {
  const ta = el.querySelector<HTMLTextAreaElement>('textarea')!;
  ta.value = text;
  ta.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
}

describe('Workflows', () => {
  it('adds a workflow, shows problems, then turns it on, tests it and shows the trace', async () => {
    const e = engine();
    const el = start();
    await settle(40);
    expect(el.textContent).toContain('No workflows yet');
    button(el, 'Add a workflow')!.click();
    flushSync();
    expect(el.querySelector('textarea')!.value).toContain('"chat.send"'); // starts from the example
    await type(el, el.querySelector('textarea')!.value.replace('"chat.send"', '"chat.shout"'));
    button(el, 'Save')!.click();
    await settle(40);
    expect(el.textContent).toContain('$.steps[0].action');
    expect(el.textContent).toContain('no action "chat.shout"');
    await type(el, el.querySelector('textarea')!.value.replace('"chat.shout"', '"chat.send"'));
    button(el, 'Save')!.click();
    await settle(60);
    expect(el.textContent).toContain('It’s off until you turn it on.');
    expect(el.textContent).toContain('When: Chat command !hello');
    expect(button(el, 'Test')!.disabled).toBe(true); // off: can't test yet
    expect(el.textContent).toContain('Turn it on to test it.');

    el.querySelector<HTMLInputElement>('input[role="switch"]')!.click();
    await settle(60);
    expect(e.wfs[0].enabled).toBe(true);
    expect(el.textContent).not.toContain('Turn it on to test it.');
    button(el, 'Test')!.click();
    await settle(60);
    const sim = e.calls.find((c) => c.path === '/v1/events/simulate')!.body as { type: string; fields: Record<string, string> };
    expect(sim.type).toBe('chat.command.received');
    expect(sim.fields).toEqual({ command: '!hello' });
    expect(el.textContent).toContain('nothing reaches Twitch or OBS');
    expect(el.textContent).toContain('Last run: Done (test)');

    button(el, 'Trace')!.click();
    await settle(60);
    expect(el.textContent).toContain('A test run');
    expect(el.textContent).toContain('dry run: a test event never reaches Twitch');
    expect(el.textContent).toContain('Hi TestViewer!');
    for (const theme of ['dark', 'light', 'midnight']) {
      document.documentElement.dataset.theme = theme;
      const r = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
      expect(r.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.html).join(', ')}`), theme).toEqual([]);
    }
  });

  it('says why runs did not run', async () => {
    mockFetch({
      '/v1/workflows': () => ({ status: 200, body: { workflows: [] } }),
      '/v1/jobs': () => ({
        status: 200,
        body: { jobs: [{ id: 'j2', workflow_id: 'x', version: 1, event_id: 'e', event_seq: 2, status: 'rejected', reason: '100 runs were already waiting (max_pending)', dry_run: false, created_at: new Date().toISOString() }] },
      }),
      '/v1/actions': () => ({ status: 200, body: { actions: [] } }),
      '/v1/events/catalog': () => ({ status: 200, body: { kinds: [] } }),
    });
    const el = start();
    await settle(40);
    expect(el.textContent).toContain('Not run: queue full');
    expect(el.textContent).toContain('100 runs were already waiting');
  });

  it('shows Unknown when the engine stops answering', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('down'); }));
    const el = start();
    await settle(40);
    expect(el.textContent).toContain('Unknown');
  });
});
