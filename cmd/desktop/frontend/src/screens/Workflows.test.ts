import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Workflows from './Workflows.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { Job, Palette, WorkflowSummary } from '../lib/api';

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
  Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === text || b.getAttribute('aria-label') === text);

/** The control inside the label whose text starts with name. */
function field<T extends HTMLElement = HTMLInputElement>(el: ParentNode, name: string, nth = 0): T {
  const labels = Array.from(el.querySelectorAll('label')).filter((l) => l.querySelector('span')?.textContent?.trim().startsWith(name));
  const c = labels[nth]?.querySelector('input, select, textarea');
  if (!c) throw new Error(`no field ${name}`);
  return c as unknown as T;
}

const byLabel = <T extends HTMLElement>(el: ParentNode, aria: string) => el.querySelector<T>(`[aria-label="${aria}"]`)!;

function set(c: HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement, value: string) {
  c.value = value;
  c.dispatchEvent(new Event('input', { bubbles: true }));
  c.dispatchEvent(new Event('change', { bubbles: true }));
  flushSync();
}

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
        { name: 'role', label: 'Role', kind: 'choice', default: 'viewer', max: 11, choices: ['viewer', 'moderator', 'broadcaster'], path: 'chatter.role' },
      ],
    },
  ],
};

const palette: Palette = {
  triggers: [
    {
      type: 'chat.command.received',
      label: 'A viewer types a command',
      category: 'Chat',
      help: 'Someone types !something in your chat.',
      match: [
        { path: 'command', label: 'Command', kind: 'command' },
        { path: 'chatter.role', label: 'Who can use it', kind: 'who' },
      ],
      values: [
        { token: 'viewer', label: 'Their name', expr: 'event.actor.display_name' },
        { token: 'args', label: 'What they typed after the command', expr: 'event.payload.args' },
      ],
    },
    {
      type: 'broadcast.scene.changed',
      label: 'OBS switches scene',
      category: 'OBS',
      help: '',
      match: [{ path: 'scene_name', label: 'Only for scene', kind: 'scene' }],
      values: [{ token: 'scene', label: 'The new scene', expr: 'event.payload.scene_name' }],
    },
  ],
  actions: [
    {
      name: 'chat.send', label: 'Send a chat message', description: 'Posts in your chat.', group: 'Twitch', external: true,
      params: [
        { name: 'message', label: 'Message', type: 'text', required: true, editor: 'message' },
        { name: 'as', label: 'Send as', type: 'choice', choices: ['bot', 'streamer'], required: false },
      ],
    },
    {
      name: 'workflow.run', label: 'Run another workflow', description: 'Runs another workflow.', group: 'Workflows', external: false,
      params: [
        { name: 'workflow', label: 'Workflow', type: 'text', required: true, literal: true, editor: 'workflow' },
        { name: 'args', label: 'Values to pass', type: 'any', required: false },
      ],
    },
    {
      name: 'obs.screenshot', label: 'Take a screenshot', description: '', group: 'OBS', external: true, more: true,
      params: [{ name: 'source', label: 'Source or scene', type: 'text', required: true, editor: 'source' }],
    },
  ],
};

const now = () => new Date().toISOString();

function engine(initial: { wfs?: WorkflowSummary[]; defs?: Record<string, unknown>; deleted?: WorkflowSummary[] } = {}) {
  const wfs: WorkflowSummary[] = [...(initial.wfs ?? [])];
  const deleted: WorkflowSummary[] = [...(initial.deleted ?? [])];
  const defs: Record<string, unknown> = { ...(initial.defs ?? {}) };
  const jobs: Job[] = [];
  const save = (id: string) => (method: string, body: unknown) => {
    if (method === 'GET') return { status: 200, body: { workflow: wfs.find((w) => w.id === id), definition: defs[id] } };
    if (method === 'DELETE') {
      const i = wfs.findIndex((w) => w.id === id);
      deleted.push({ ...wfs[i], enabled: false, deleted_at: now() });
      wfs.splice(i, 1);
      return { status: 204, body: null };
    }
    const d = body as { id: string; name: string; steps: unknown[]; triggers: WorkflowSummary['triggers'] };
    if (!d.steps?.length) {
      return { status: 400, body: { error: { code: 'invalid_request', message: 'the workflow has problems' }, problems: [{ path: '$.steps', message: 'at least one step' }] } };
    }
    defs[id] = d;
    const prev = wfs.find((w) => w.id === id);
    const w: WorkflowSummary = { id: d.id, name: d.name, enabled: prev?.enabled ?? false, version: (prev?.version ?? 0) + 1, updated_at: now(), triggers: d.triggers };
    if (prev) wfs.splice(wfs.indexOf(prev), 1, w);
    else wfs.push(w);
    return { status: 200, body: { workflow: w, changed: true } };
  };
  const routes: Parameters<typeof mockFetch>[0] = {
    '/v1/workflows': (_m, _b, query) => ({ status: 200, body: { workflows: query.includes('deleted=true') ? deleted : wfs } }),
    '/v1/jobs': () => ({ status: 200, body: { jobs } }),
    '/v1/palette': () => ({ status: 200, body: palette }),
    '/v1/variables': () => ({
      status: 200,
      body: { variables: [{ scope: 'global', owner: '', name: 'deaths', value: 3, updated_at: now(), updated_by: 'you' }] },
    }),
    '/v1/obs/scenes': () => ({ status: 409, body: { error: { code: 'not_connected', message: 'OBS isn’t connected' } } }),
    '/v1/events/catalog': () => ({ status: 200, body: catalog }),
    '/v1/workflows/say-hello-back': save('say-hello-back'),
    '/v1/workflows/chat.hello': save('chat.hello'),
    '/v1/workflows/say-hello-back/enabled': (_m, body) => {
      const i = wfs.findIndex((w) => w.id === 'say-hello-back');
      wfs[i] = { ...wfs[i], enabled: (body as { enabled: boolean }).enabled };
      return { status: 200, body: wfs[i] };
    },
    '/v1/workflows/chat.old/restore': () => {
      const w = { ...deleted[0], deleted_at: undefined };
      deleted.splice(0, 1);
      wfs.push(w);
      return { status: 200, body: w };
    },
    '/v1/events/simulate': (_m, body) => {
      jobs.unshift({ id: 'job-1', workflow_id: 'say-hello-back', version: 1, event_id: 'e', event_seq: 1, status: 'succeeded', dry_run: true, created_at: now() });
      wfs[0] = { ...wfs[0], last_job: jobs[0] };
      return { status: 201, body: { event: { seq: 1 }, duplicate: false, sent: body } };
    },
    '/v1/jobs/job-1': () => ({
      status: 200,
      body: {
        job: jobs[0],
        steps: [{ path: 'steps[0]', kind: 'action', action: 'chat.send', status: 'skipped', reason: 'dry run: a test event never reaches Twitch', input: { message: 'Hi TestViewer!' }, output: { dry_run: true }, started_at: now(), finished_at: now() }],
      },
    }),
  };
  const calls = mockFetch(routes);
  return { calls, wfs, defs, deleted };
}

async function noViolations(label: string) {
  for (const theme of ['dark', 'light', 'midnight']) {
    document.documentElement.dataset.theme = theme;
    const r = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
    expect(r.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.html).join(', ')}`), `${label}, ${theme}`).toEqual([]);
  }
}

const lastPut = (calls: { method: string; path: string; body: unknown }[]) => calls.filter((c) => c.method === 'PUT' && !c.path.endsWith('/enabled')).at(-1)!.body;

describe('Workflows', () => {
  it('builds a workflow in plain words, then turns it on, tests it and shows the trace', async () => {
    const e = engine();
    const el = start();
    await settle(40);
    expect(el.textContent).toContain('No workflows yet');
    button(el, 'New workflow')!.click();
    await settle(40);
    expect(el.querySelector('h2')!.textContent).toBe('New workflow');

    set(field(el, 'Name'), 'Say hello back');
    set(field(el, 'Command'), '!Hello');
    set(field<HTMLSelectElement>(el, 'Who can use it'), 'mods');
    // Saving with no steps shows the problem.
    button(el, 'Save')!.click();
    await settle(40);
    expect(el.textContent).toContain('at least one step');

    set(byLabel<HTMLSelectElement>(el, 'Add a step'), 'action:chat.send');
    expect(el.textContent).toContain('Send a chat message');
    // Rare steps are hidden until asked for.
    expect(byLabel(el, 'Add a step').textContent).not.toContain('Take a screenshot');
    const msg = field<HTMLTextAreaElement>(el, 'Message');
    set(msg, 'Hi ');
    msg.setSelectionRange(3, 3);
    set(byLabel<HTMLSelectElement>(el, 'Insert a value into Message'), 'viewer');
    expect(msg.value).toBe('Hi {viewer}');
    set(msg, 'Hi {viewer}!');
    await noViolations('the editor');
    button(el, 'Save')!.click();
    await settle(60);
    expect(lastPut(e.calls)).toEqual({
      schema: 'streamgremlin.workflow/v1',
      id: 'say-hello-back',
      name: 'Say hello back',
      triggers: [{ type: 'chat.command.received', where: { command: 'hello', 'chatter.role': ['moderator', 'broadcaster'] } }],
      steps: [{ action: 'chat.send', with: { message: 'Hi ${event.actor.display_name}!' } }],
    });
    expect(el.textContent).toContain('It’s off until you turn it on.');
    expect(el.textContent).toContain('When: Chat command !hello (mods and you)');
    expect(button(el, 'Test')!.disabled).toBe(true);
    expect(el.textContent).toContain('Turn it on to test it.');

    el.querySelector<HTMLInputElement>('input[role="switch"]')!.click();
    await settle(60);
    button(el, 'Test')!.click();
    await settle(60);
    const sim = e.calls.find((c) => c.path === '/v1/events/simulate')!.body as { type: string; fields: Record<string, string> };
    expect(sim.fields).toEqual({ command: '!hello', role: 'moderator' }); // a test event that matches "mods and you"
    expect(el.textContent).toContain('nothing reaches Twitch or OBS');
    button(el, 'Trace')!.click();
    await settle(60);
    expect(el.textContent).toContain('dry run: a test event never reaches Twitch');
    await noViolations('the list');
  });

  it('edits a workflow and keeps what the editor can’t show', async () => {
    const def = {
      schema: 'streamgremlin.workflow/v1',
      id: 'chat.hello',
      name: 'Hello',
      triggers: [{ type: 'chat.command.received', where: { command: 'hi', 'chatter.badges': 'x' }, delivery_quality: ['confirmed'] }],
      queue: { policy: 'drop' },
      steps: [
        { action: 'chat.send', with: { message: 'Yo ${event.actor.display_name} ${1 + 2}' }, if: 'vars.x > 2 || true' },
        { foreach: 'event.payload.arg_list', steps: [{ action: 'note', with: { text: 'x' } }] },
      ],
    };
    const e = engine({
      wfs: [{ id: 'chat.hello', name: 'Hello', enabled: true, version: 1, updated_at: now(), triggers: def.triggers as WorkflowSummary['triggers'] }],
      defs: { 'chat.hello': def },
    });
    const el = start();
    await settle(40);
    button(el, 'Edit Hello')!.click();
    await settle(60);
    expect(el.querySelector('h2')!.textContent).toBe('Edit “Hello”');
    expect(field(el, 'Id').disabled).toBe(true);
    expect(field(el, 'Message').value).toBe('Yo {viewer} ${1 + 2}');
    expect(el.textContent).toContain('A step the editor can’t show yet');
    expect(el.textContent).toContain('Also must match');
    expect(el.textContent).toContain('Test events don’t start it.');
    expect(field(el, 'Condition (an expression)').value).toBe('vars.x > 2 || true');
    set(field(el, 'Message'), 'Yo {viewer}!');
    await noViolations('editing');
    button(el, 'Save')!.click();
    await settle(60);
    expect(lastPut(e.calls)).toEqual({
      ...def,
      steps: [{ ...def.steps[0], with: { message: 'Yo ${event.actor.display_name}!' } }, def.steps[1]],
    });
  });

  it('builds If… with a condition in words, and runs another workflow with values', async () => {
    const e = engine({
      wfs: [{ id: 'helper.shout', name: 'Shout', enabled: false, version: 1, updated_at: now(), triggers: [] }],
    });
    const el = start();
    await settle(40);
    button(el, 'New workflow')!.click();
    await settle(40);
    set(field(el, 'Name'), 'Say hello back');
    set(field(el, 'Command'), 'hi');
    set(byLabel<HTMLSelectElement>(el, 'Add a step'), 'flow:if');
    set(byLabel<HTMLSelectElement>(el, 'Value'), '{saved.global.deaths}'); // saved variables are offered too
    set(byLabel<HTMLSelectElement>(el, 'Comparison'), 'more');
    set(byLabel<HTMLInputElement>(el, 'Compared with'), 'lots');
    expect(el.textContent).toContain('Type a number to compare with.');
    set(byLabel<HTMLInputElement>(el, 'Compared with'), '3');
    set(byLabel<HTMLSelectElement>(el, 'Add a step to do when it’s true'), 'action:workflow.run');
    const pick = field<HTMLSelectElement>(el, 'Workflow');
    expect(pick.textContent).toContain('Shout (off: turn it on to run it)');
    set(pick, 'helper.shout');
    set(field(el, 'New value’s name'), 'who');
    button(el, 'Add')!.click();
    flushSync();
    set(field(el, 'args.who'), '{viewer}');
    button(el, 'Save')!.click();
    await settle(60);
    expect((lastPut(e.calls) as { steps: unknown[] }).steps).toEqual([
      {
        if: 'double(saved.global.?deaths.orValue(0)) > 3',
        do: [{ action: 'workflow.run', with: { workflow: 'helper.shout', args: { who: '${event.actor.display_name}' } } }],
      },
    ]);
  });

  it('has a JSON tab, and asks before leaving unsaved changes', async () => {
    engine();
    const el = start();
    await settle(40);
    button(el, 'New workflow')!.click();
    await settle(40);
    button(el, 'JSON')!.click();
    flushSync();
    const ta = el.querySelector<HTMLTextAreaElement>('textarea')!;
    set(ta, '{ not json');
    button(el, 'Editor')!.click();
    flushSync();
    expect(el.textContent).toContain('That isn’t valid JSON');
    set(ta, JSON.stringify({ schema: 'streamgremlin.workflow/v1', id: 'x', name: 'From JSON', triggers: [{ type: 'chat.command.received' }], steps: [] }));
    button(el, 'Editor')!.click();
    await settle(20);
    expect(field(el, 'Name').value).toBe('From JSON');
    button(el, 'Cancel')!.click();
    flushSync();
    const dialog = document.querySelector('[role="alertdialog"]')!;
    expect(dialog.textContent).toContain('Leave without saving?');
    button(dialog, 'Leave')!.click();
    await settle(20);
    expect(el.textContent).toContain('Your workflows');
  });

  it('deletes after asking, and restores', async () => {
    const e = engine({
      wfs: [{ id: 'chat.hello', name: 'Hello', enabled: true, version: 1, updated_at: now(), triggers: [{ type: 'chat.command.received', where: { command: 'hi' } }] }],
      deleted: [{ id: 'chat.old', name: 'Old one', enabled: false, version: 2, updated_at: now(), deleted_at: now(), triggers: [] }],
    });
    const el = start();
    await settle(40);
    expect(el.textContent).toContain('Deleted workflows (1)');
    button(el, 'Delete Hello')!.click();
    flushSync();
    const dialog = document.querySelector('[role="alertdialog"]')!;
    expect(dialog.textContent).toContain('you can restore it');
    await noViolations('delete dialog');
    button(dialog, 'Delete')!.click();
    await settle(60);
    expect(e.calls.some((c) => c.method === 'DELETE' && c.path === '/v1/workflows/chat.hello')).toBe(true);
    expect(el.textContent).toContain('Deleted “Hello”');
    expect(el.textContent).toContain('Deleted workflows (2)');
    button(el, 'Restore Old one')!.click();
    await settle(60);
    expect(e.calls.some((c) => c.method === 'POST' && c.path === '/v1/workflows/chat.old/restore')).toBe(true);
    expect(el.textContent).toContain('Restored “Old one”. It’s off until you turn it on.');
  });

  it('says why runs did not run', async () => {
    mockFetch({
      '/v1/workflows': () => ({ status: 200, body: { workflows: [] } }),
      '/v1/jobs': () => ({
        status: 200,
        body: { jobs: [{ id: 'j2', workflow_id: 'x', version: 1, event_id: 'e', event_seq: 2, status: 'rejected', reason: '100 runs were already waiting (max_pending)', dry_run: false, created_at: now() }] },
      }),
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
