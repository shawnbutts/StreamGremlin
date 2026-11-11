import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Workflows from './Workflows.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { CommandEntry, Job, Palette, WorkflowSummary } from '../lib/api';

let comp: ReturnType<typeof mount> | undefined;

afterEach(() => {
  localStorage.clear();
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
    {
      type: 'plugin.dice_tray.round_ended',
      label: 'A dice round ends',
      category: 'Dice tray',
      help: 'A round closed. From Dice tray, published by Someone (not verified).',
      match: [],
      values: [{ token: 'winner', label: 'The winner', expr: 'event.payload.winner' }],
      add_on: 'dice-tray',
    },
  ],
  actions: [
    // An add-on's step, listed first by the engine, shown after StreamGremlin's own.
    {
      name: 'dice-tray.roll', label: 'Roll dice', description: 'Rolls dice. From Dice tray, published by Someone (not verified).', group: 'Dice tray',
      external: true, add_on: 'dice-tray',
      params: [{ name: 'dice', label: 'How many dice', type: 'text', required: false, default_hint: 'A number from 1 to 10' }],
    },
    {
      name: 'chat.send', label: 'Send a chat message', description: 'Posts in your chat.', group: 'Twitch', external: true,
      params: [
        { name: 'message', label: 'Message', type: 'text', required: true, editor: 'message' },
        { name: 'as', label: 'Send as', type: 'choice', choices: ['bot', 'streamer'], required: false, default_hint: 'The bot if one is signed in, otherwise you' },
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
    {
      name: 'sounds.play', label: 'Play a sound', description: '', group: 'Sounds', external: true,
      params: [{ name: 'sound', label: 'Sound', type: 'text', required: true, editor: 'sound' }],
    },
  ],
};

const templates = {
  categories: ['Chat commands', 'Scenes', 'Stream', 'Dice tray'],
  templates: [
    {
      id: 'dice-tray/roll', title: '!roll: roll dice in chat', category: 'Dice tray', description: 'Rolls dice.', starter: false, blanks: [],
      add_on: { id: 'dice-tray', name: 'Dice tray', publisher: 'Someone', enabled: false },
      workflow: { schema: 'streamgremlin.workflow/v1', id: 'chat.dice-tray-roll', name: 'Roll dice (Dice tray)', triggers: [{ type: 'chat.command.received', where: { command: 'roll' } }], steps: [{ id: 'roll', action: 'dice-tray.roll' }] },
    },
    {
      id: 'brb', title: 'Be right back', category: 'Scenes', description: '!brb switches to your break scene.', starter: true,
      blanks: [{ path: '$.steps[0].with.message', hint: 'What to say in chat' }],
      workflow: {
        schema: 'streamgremlin.workflow/v1', id: 'chat.brb', name: 'Be right back',
        triggers: [{ type: 'chat.command.received', where: { command: 'brb', 'chatter.role': ['moderator', 'broadcaster'] } }],
        steps: [{ action: 'chat.send', with: { message: '', as: 'streamer' } }],
      },
    },
    {
      id: 'hello', title: 'Say hello back', category: 'Chat commands', description: '!hello answers by name.', starter: true, blanks: [],
      workflow: { schema: 'streamgremlin.workflow/v1', id: 'chat.hello', name: 'Say hello back', triggers: [{ type: 'chat.command.received', where: { command: 'hello' } }], steps: [{ action: 'chat.send', with: { message: 'Hi ${event.actor.display_name}!' } }] },
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
    '/v1/sounds': () => ({
      status: 200,
      body: { sounds: ['airhorn', 'chime', 'boing'].map((name) => ({ name, format: 'wav', size: 1, sha256: '', volume: 80, created_at: '', updated_at: '' })), player: true },
    }),
    '/v1/commands': () => ({ status: 200, body: { commands: [] } }),
    '/v1/templates': () => ({ status: 200, body: templates }),
    '/v1/workflows/chat.brb': save('chat.brb'),
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

function engineWithRefusal() {
  const wfs: WorkflowSummary[] = [
    { id: 'chat.hello', name: 'Hello', enabled: true, version: 1, updated_at: now(), triggers: [{ type: 'chat.command.received', where: { command: 'hello' } }] },
    { id: 'chat.hi', name: 'Hi there', enabled: false, version: 1, updated_at: now(), triggers: [{ type: 'chat.command.received', where: { command: 'hello' } }] },
  ];
  const commands: CommandEntry[] = [
    {
      command: 'hello',
      conflict: false,
      owners: [
        { owner: 'chat.hello', name: 'Hello', enabled: true, roles: ['moderator', 'broadcaster'] },
        { owner: 'chat.hi', name: 'Hi there', enabled: false },
      ],
    },
    {
      command: 'so',
      conflict: true,
      owners: [
        { owner: 'a', name: 'Shout A', enabled: true, roles: ['moderator', 'broadcaster'] },
        { owner: 'b', name: 'Shout B', enabled: true },
      ],
    },
  ];
  return mockFetch({
    '/v1/workflows': (_m, _b, q) => ({ status: 200, body: { workflows: q.includes('deleted') ? [] : wfs } }),
    '/v1/jobs': () => ({ status: 200, body: { jobs: [] } }),
    '/v1/commands': () => ({ status: 200, body: { commands } }),
    '/v1/palette': () => ({ status: 200, body: palette }),
    '/v1/templates': () => ({ status: 200, body: templates }),
    '/v1/variables': () => ({ status: 200, body: { variables: [] } }),
    '/v1/obs/scenes': () => ({ status: 409, body: { error: { code: 'not_connected', message: 'no' } } }),
    '/v1/events/catalog': () => ({ status: 200, body: catalog }),
    '/v1/workflows/chat.hi/enabled': () => ({
      status: 409,
      body: {
        error: { code: 'command_taken', message: '!hello is already answered by “Hello”. Turn that off first, or limit who can use each so they never overlap: one answer per command.' },
        conflicts: [{ command: 'hello', owner: 'chat.hello', name: 'Hello' }],
      },
    }),
  });
}

// A workflow that shows alerts, and an engine whose preview of "alerts"
// connects after a few asks (or never).
function alertEngine(connectAfter: number) {
  const w: WorkflowSummary = {
    id: 'twitch.alert-follow', name: 'Follower alerts', enabled: true, version: 1, updated_at: now(),
    triggers: [{ type: 'chat.command.received', where: { command: 'follow' } }],
  };
  let asks = 0;
  const calls = mockFetch({
    '/v1/workflows': (_m, _b, q) => ({ status: 200, body: { workflows: q.includes('deleted') ? [] : [w] } }),
    '/v1/jobs': () => ({ status: 200, body: { jobs: [] } }),
    '/v1/commands': () => ({ status: 200, body: { commands: [] } }),
    '/v1/events/catalog': () => ({ status: 200, body: catalog }),
    '/v1/workflows/twitch.alert-follow': () => ({
      status: 200,
      body: {
        workflow: w,
        definition: {
          ...w,
          steps: [{ if: 'true', do: [{ action: 'overlays.alert', with: { overlay: 'alerts', text: 'hi' } }] }, { action: 'overlays.alert', with: { overlay: '${vars.which}', text: 'x' } }],
        },
      },
    }),
    '/v1/overlays': () => {
      asks++;
      const previews = asks > connectAfter ? 1 : 0;
      return { status: 200, body: { overlays: [{ name: 'alerts', kind: 'alert', look: {}, pages: 0, previews }], server: 'http://127.0.0.1:7431' } };
    },
    '/v1/overlays/alerts/link': () => ({ status: 200, body: { name: 'alerts', url: 'http://127.0.0.1:7431/o/tok/' } }),
    '/v1/events/simulate': () => ({ status: 201, body: { event: { seq: 1 }, duplicate: false } }),
  });
  return calls;
}

describe('Workflow groups', () => {
  function groupEngine() {
    const mk = (id: string, name: string, group: string, enabled: boolean, groupOn: boolean): WorkflowSummary => ({
      id, name, enabled, group, group_enabled: groupOn, running: enabled && groupOn, version: 1, updated_at: now(),
      triggers: [{ type: 'chat.command.received', where: { command: id.split('.')[1] } }],
    });
    let alertsOn = true;
    const list = () => [
      mk('chat.follow', 'Follow alert', 'Alerts', true, alertsOn),
      mk('chat.sub', 'Sub alert', 'Alerts', false, alertsOn),
      mk('chat.hi', 'Hi', '', true, true),
    ];
    const groups = () => [{ name: 'Alerts', enabled: alertsOn, workflows: 2, on: 1 }];
    const calls = mockFetch({
      '/v1/workflows': (_m, _b, q) => ({ status: 200, body: { workflows: q.includes('deleted') ? [] : list() } }),
      '/v1/workflow-groups': () => ({ status: 200, body: { groups: groups() } }),
      '/v1/workflow-groups/Alerts/enabled': (_m, body) => {
        const on = (body as { enabled: boolean }).enabled;
        if (on && refuseOn) {
          return { status: 409, body: { error: { code: 'command_taken', message: '!follow is already answered by “Other”.' }, conflicts: [] } };
        }
        alertsOn = on;
        return { status: 200, body: { groups: groups() } };
      },
      '/v1/workflow-groups/Alerts': () => ({ status: 200, body: { name: 'On screen' } }),
      '/v1/jobs': () => ({ status: 200, body: { jobs: [] } }),
      '/v1/commands': () => ({ status: 200, body: { commands: [] } }),
      '/v1/events/catalog': () => ({ status: 200, body: catalog }),
    });
    let refuseOn = false;
    return { calls, refuse: () => (refuseOn = true) };
  }

  it('lists workflows by group, with each group switch, accessibly', async () => {
    const { calls, refuse } = groupEngine();
    const el = start();
    await settle(60);
    const heads = Array.from(el.querySelectorAll('.wf-group h3')).map((h) => h.textContent?.replace(/[▸▾]/g, '').trim());
    expect(heads).toEqual(['Alerts', 'No group']);
    expect(el.textContent).toContain('2 workflows, 1 switched on');
    // Groups start folded; "No group" is open.
    const toggle = () => el.querySelector<HTMLButtonElement>('.group-toggle')!;
    expect(toggle().getAttribute('aria-expanded')).toBe('false');
    expect(el.textContent).not.toContain('Follow alert');
    expect(el.textContent).toContain('Hi');
    await noViolations('folded');
    toggle().click();
    flushSync();
    expect(toggle().getAttribute('aria-expanded')).toBe('true');
    expect(el.textContent).toContain('Follow alert');
    expect(localStorage.getItem('streamgremlin.workflows.open-groups')).toBe('["Alerts"]');
    await noViolations('groups');
    const groupSwitch = el.querySelector<HTMLInputElement>('input[aria-label="Group Alerts on"]')!;
    groupSwitch.click();
    await settle(60);
    expect(calls.find((c) => c.path === '/v1/workflow-groups/Alerts/enabled')!.body).toEqual({ enabled: false });
    expect(el.textContent).toContain('This group is off: none of its workflows run. Each keeps its own switch.');
    expect(el.textContent).toContain('It’s on, but its group (Alerts) is off, so it doesn’t run.');
    // Its workflows can't be tested while the group is off.
    const followRow = Array.from(el.querySelectorAll('li')).find((li) => li.querySelector('strong')?.textContent === 'Follow alert')!;
    expect(button(followRow, 'Test')!.disabled).toBe(true);
    // Coming back on can be refused: one answer per command.
    refuse();
    el.querySelector<HTMLInputElement>('input[aria-label="Group Alerts on"]')!.click();
    await settle(60);
    expect(el.querySelector('[role="alert"]')?.textContent).toContain('already answered');
    expect(el.querySelector<HTMLInputElement>('input[aria-label="Group Alerts on"]')!.checked).toBe(false);
  });

  it('remembers open groups, opens all and folds all, and opens groups with search matches', async () => {
    groupEngine();
    localStorage.setItem('streamgremlin.workflows.open-groups', '["alerts"]'); // any case
    let el = start();
    await settle(60);
    expect(el.textContent).toContain('Sub alert');
    button(el, 'Fold all')!.click();
    flushSync();
    expect(el.textContent).not.toContain('Sub alert');
    set(el.querySelector<HTMLInputElement>('input[type="search"]')!, 'sub');
    flushSync();
    expect(el.textContent).toContain('Sub alert');
    expect(button(el, 'Fold all')).toBeUndefined();
    set(el.querySelector<HTMLInputElement>('input[type="search"]')!, '');
    flushSync();
    expect(el.textContent).not.toContain('Sub alert');
    button(el, 'Open all')!.click();
    flushSync();
    unmount(comp!);
    document.body.innerHTML = '';
    el = start();
    await settle(60);
    expect(el.textContent).toContain('Sub alert');
    localStorage.clear();
  });

  it('renames a group', async () => {
    const { calls } = groupEngine();
    const el = start();
    await settle(60);
    button(el, 'Rename Alerts')!.click();
    flushSync();
    set(field(el, 'New name for'), 'On screen');
    flushSync();
    button(el, 'Save')!.click();
    await settle(60);
    expect(calls.find((c) => c.path === '/v1/workflow-groups/Alerts' && c.method === 'PUT')!.body).toEqual({ name: 'On screen' });
    expect(el.textContent).toContain('Renamed “Alerts” to “On screen”.');
  });
});

describe('Workflows', () => {
  it('searches workflows by name, command or trigger, and says when nothing matches', async () => {
    const mk = (id: string, name: string, command: string): WorkflowSummary => ({
      id, name, enabled: true, version: 1, updated_at: now(), triggers: [{ type: 'chat.command.received', where: { command } }],
    });
    mockFetch({
      '/v1/workflows': (_m, _b, q) => ({
        status: 200,
        body: { workflows: q.includes('deleted') ? [] : [mk('chat.brb', 'Be right back', 'brb'), mk('chat.lurk', 'Lurk', 'lurk'), mk('chat.so', 'Shoutout', 'so')] },
      }),
      '/v1/jobs': () => ({ status: 200, body: { jobs: [] } }),
      '/v1/commands': () => ({ status: 200, body: { commands: [] } }),
      '/v1/events/catalog': () => ({ status: 200, body: catalog }),
    });
    const el = start();
    await settle(60);
    const names = () => Array.from(el.querySelectorAll('ul[aria-label="Workflows"] strong')).map((x) => x.textContent);
    expect(names()).toEqual(['Be right back', 'Lurk', 'Shoutout']);
    const box = el.querySelector<HTMLInputElement>('input[type="search"]')!;
    set(box, '!LURK');
    flushSync();
    expect(names()).toEqual(['Lurk']);
    set(box, 'right back');
    flushSync();
    expect(names()).toEqual(['Be right back']);
    set(box, 'nothing like this');
    flushSync();
    expect(names()).toEqual([]);
    expect(el.textContent).toContain('No workflow matches “nothing like this”.');
    box.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    flushSync();
    expect(names().length).toBe(3);
  });

  it('shows a test of an alert in a preview where Test was pressed, once the preview is connected', async () => {
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
    const calls = alertEngine(2);
    const el = start();
    await settle(60);
    button(el, 'Test')!.click();
    await settle(600);
    const frame = el.querySelector('iframe')!;
    expect(frame.getAttribute('src')).toBe('http://127.0.0.1:7431/o/tok/?preview=1');
    expect(el.textContent).toContain('Test preview: Follower alerts');
    // The test event goes only once the preview is there to see it.
    const sim = calls.findIndex((c) => c.path === '/v1/events/simulate');
    const connected = calls.filter((c) => c.path === '/v1/overlays').length;
    expect(sim).toBeGreaterThan(-1);
    expect(connected).toBeGreaterThanOrEqual(3);
    expect(el.textContent).toContain('its alert shows in the preview below, never in OBS');
    button(el, 'Close the preview')!.click();
    flushSync();
    expect(el.querySelector('iframe')).toBeNull();
  });

  it('says so when the preview does not connect in time, and tests anyway', async () => {
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
    const calls = alertEngine(1000);
    const target = document.createElement('main');
    document.body.appendChild(target);
    comp = mount(Workflows, { target, props: { refreshMs: 20, previewWaitMs: 200 } });
    await settle(60);
    button(target, 'Test')!.click();
    await settle(500);
    expect(calls.some((c) => c.path === '/v1/events/simulate')).toBe(true);
    expect(target.textContent).toContain("didn't connect in time");
  });

  it('builds a workflow in plain words, then turns it on, tests it and shows the trace', async () => {
    const e = engine();
    const el = start();
    await settle(40);
    expect(el.textContent).toContain('No workflows yet');
    button(el, 'New workflow')!.click();
    await settle(40);
    button(el, 'Start from scratch')!.click();
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
    // An add-on's steps come last, marked as an add-on's.
    const groups = Array.from(byLabel(el, 'Add a step').querySelectorAll('optgroup')).map((g) => g.label);
    expect(groups.slice(-2)).toEqual(['Dice tray (add-on)', 'Flow']);
    expect(byLabel(el, 'Add a step').textContent).toContain('Roll dice (add-on)');
    // An add-on's events come last among the triggers, marked as an add-on's.
    const when = el.querySelector<HTMLSelectElement>('.trigger-pick select')!;
    expect(Array.from(when.querySelectorAll('optgroup')).map((g) => g.label).at(-1)).toBe('Dice tray (add-on)');
    expect(when.textContent).toContain('A dice round ends (add-on)');
    set(byLabel<HTMLSelectElement>(el, 'Add a step'), 'action:dice-tray.roll');
    expect(el.textContent).toContain('Roll dice (from the Dice tray add-on)');
    button(el, 'Remove step 2')!.click();
    await settle(10);
    const msg = field<HTMLTextAreaElement>(el, 'Message');
    set(msg, 'Hi ');
    msg.setSelectionRange(3, 3);
    set(byLabel<HTMLSelectElement>(el, 'Insert a value into Message'), 'viewer');
    expect(msg.value).toBe('Hi {viewer}');
    expect(field<HTMLSelectElement>(el, 'Send as').selectedOptions[0].textContent).toBe('The bot if one is signed in, otherwise you');
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

  it('starts from a template, and won’t save until its blanks are filled in', async () => {
    const e = engine({ wfs: [{ id: 'chat.hello', name: 'Say hello back', enabled: true, version: 1, updated_at: now(), triggers: [] }] });
    const el = start();
    await settle(40);
    button(el, 'New workflow')!.click();
    await settle(40);
    const gallery = el.querySelector('section[aria-labelledby="gallery-heading"]')!;
    expect(gallery.querySelector('[aria-label="Scenes"]')!.textContent).toContain('1 thing to fill in');
    expect(gallery.textContent).toContain('You already have “Say hello back”');
    // An add-on's templates are in its own section, last, saying whose they are.
    const headings = Array.from(gallery.querySelectorAll('h3')).map((h) => h.textContent);
    expect(headings.at(-1)).toBe('Dice tray (add-on)');
    expect(gallery.querySelector('[aria-label="Dice tray"]')!.textContent).toContain('!roll: roll dice in chat');
    expect(gallery.textContent!.replace(/\s+/g, ' ')).toContain('From the Dice tray add-on, published by Someone (not verified). Dice tray is off: turn it on on Add-ons');
    await noViolations('gallery');
    button(el, 'Use Be right back')!.click();
    await settle(60);
    expect(el.querySelector('h2')!.textContent).toBe('New workflow from “Be right back”');
    expect(el.textContent).toContain('Fill in before saving:');
    expect(field(el, 'Name').value).toBe('Be right back');
    expect(field(el, 'Id').value).toBe('chat.brb'); // the template's own id, while it's free
    button(el, 'Save')!.click();
    await settle(40);
    expect(e.calls.some((c) => c.method === 'PUT')).toBe(false); // refused before asking the engine
    expect(el.querySelector('.step-card .error')!.textContent).toContain('Fill in: What to say in chat');
    set(field(el, 'Message'), 'BRB!');
    expect(el.textContent).toContain('Everything to fill in is filled in.');
    button(el, 'Save')!.click();
    await settle(60);
    expect((lastPut(e.calls) as { id: string; steps: { with: { message: string } }[] }).steps[0].with.message).toBe('BRB!');
    expect(el.textContent).toContain('Saved “Be right back” as version 1. It’s off until you turn it on.');
  });

  it('plays one of several sounds, and back to one', async () => {
    const def = {
      schema: 'streamgremlin.workflow/v1', id: 'chat.hello', name: 'Horn',
      triggers: [{ type: 'chat.command.received', where: { command: 'horn' } }],
      steps: [{ action: 'sounds.play', with: { sound: 'airhorn' } }],
    };
    const e = engine({
      wfs: [{ id: 'chat.hello', name: 'Horn', enabled: true, version: 1, updated_at: now(), triggers: def.triggers as WorkflowSummary['triggers'] }],
      defs: { 'chat.hello': def },
    });
    const el = start();
    await settle(40);
    button(el, 'Edit Horn')!.click();
    await settle(60);
    button(el, '+ Another sound')!.click();
    flushSync();
    expect(field<HTMLSelectElement>(el, 'Sound 1').value).toBe('airhorn');
    set(field<HTMLSelectElement>(el, 'Sound 2'), 'chime');
    button(el, '+ Another sound')!.click();
    flushSync();
    set(field<HTMLSelectElement>(el, 'Sound 3'), 'boing');
    await noViolations('sounds');
    button(el, 'Save')!.click();
    await settle(60);
    expect(lastPut(e.calls)).toEqual({ ...def, steps: [{ action: 'sounds.play', with: { sound: { one_of: ['airhorn', 'chime', 'boing'] } } }] });
    button(el, 'Edit Horn')!.click();
    await settle(60);
    button(el, 'Remove sound 1')!.click();
    flushSync();
    button(el, 'Remove sound 1')!.click();
    flushSync();
    button(el, 'Save')!.click();
    await settle(60);
    expect(lastPut(e.calls)).toEqual({ ...def, steps: [{ action: 'sounds.play', with: { sound: 'boing' } }] });
  });

  it('gives a message several ways to say it, and back to one', async () => {
    const def = {
      schema: 'streamgremlin.workflow/v1', id: 'chat.hello', name: 'Hello',
      triggers: [{ type: 'chat.command.received', where: { command: 'hi' } }],
      steps: [{ action: 'chat.send', with: { message: 'Hi ${event.actor.display_name}!' } }],
    };
    const e = engine({
      wfs: [{ id: 'chat.hello', name: 'Hello', enabled: true, version: 1, updated_at: now(), triggers: def.triggers as WorkflowSummary['triggers'] }],
      defs: { 'chat.hello': def },
    });
    const el = start();
    await settle(40);
    button(el, 'Edit Hello')!.click();
    await settle(60);
    button(el, '+ Another way to say it')!.click();
    flushSync();
    expect(field(el, 'Way 1').value).toBe('Hi {viewer}!');
    set(field(el, 'Way 2'), 'Welcome in, {viewer}!');
    button(el, '+ Another way to say it')!.click();
    flushSync();
    set(field(el, 'Way 3'), 'Good day to you!');
    await noViolations('versions');
    button(el, 'Save')!.click();
    await settle(60);
    expect(lastPut(e.calls)).toEqual({
      ...def,
      steps: [{ action: 'chat.send', with: { message: { one_of: ['Hi ${event.actor.display_name}!', 'Welcome in, ${event.actor.display_name}!', 'Good day to you!'] } } }],
    });
    // Removing down to one makes it a plain message again.
    button(el, 'Edit Hello')!.click();
    await settle(60);
    expect(field(el, 'Way 2').value).toBe('Welcome in, {viewer}!');
    button(el, 'Remove way 1')!.click();
    flushSync();
    expect(field(el, 'Way 1').value).toBe('Welcome in, {viewer}!');
    button(el, 'Remove way 2')!.click();
    flushSync();
    button(el, 'Save')!.click();
    await settle(60);
    expect(lastPut(e.calls)).toEqual({ ...def, steps: [{ action: 'chat.send', with: { message: 'Welcome in, ${event.actor.display_name}!' } }] });
  });

  it('puts a workflow in a group from the editor, and takes it out', async () => {
    const def = {
      schema: 'streamgremlin.workflow/v1', id: 'chat.hello', name: 'Hello',
      triggers: [{ type: 'chat.command.received', where: { command: 'hi' } }],
      steps: [{ action: 'chat.send', with: { message: 'hi' } }],
    };
    const e = engine({
      wfs: [{ id: 'chat.hello', name: 'Hello', enabled: true, version: 1, updated_at: now(), triggers: def.triggers as WorkflowSummary['triggers'] }],
      defs: { 'chat.hello': def },
    });
    const el = start();
    await settle(40);
    button(el, 'Edit Hello')!.click();
    await settle(60);
    set(field(el, 'Group'), '  Alerts ');
    button(el, 'Save')!.click();
    await settle(60);
    expect(lastPut(e.calls)).toEqual({ ...def, group: 'Alerts' });
    button(el, 'Edit Hello')!.click();
    await settle(60);
    expect(field(el, 'Group').value).toBe('Alerts');
    set(field(el, 'Group'), '');
    button(el, 'Save')!.click();
    await settle(60);
    expect(lastPut(e.calls)).toEqual(def);
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
    button(el, 'Start from scratch')!.click();
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
    button(el, 'Start from scratch')!.click();
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

  it('shows who answers each command, refuses a second answer, and warns in the editor', async () => {
    const calls = engineWithRefusal();
    const el = start();
    await settle(40);
    const list = el.querySelector('[aria-label="Chat commands"]')!;
    expect(list.textContent).toContain('!hello');
    expect(list.textContent).toContain('Hi there (off)');
    expect(list.textContent).toContain('Shout A for mods and you');
    expect(list.textContent).toContain('More than one that’s on answers this. Turn one off.');

    const sw = el.querySelectorAll<HTMLInputElement>('input[role="switch"]')[1];
    sw.click();
    await settle(60);
    expect(calls.some((c) => c.path === '/v1/workflows/chat.hi/enabled')).toBe(true);
    expect(el.querySelector('[role="alert"]')!.textContent).toContain('!hello is already answered by “Hello”');
    expect(sw.checked).toBe(false); // the switch shows what the engine says
    await noViolations('commands');

    // The editor warns as soon as the command is taken.
    button(el, 'New workflow')!.click();
    await settle(40);
    button(el, 'Start from scratch')!.click();
    await settle(40);
    set(field(el, 'Command'), 'Hello');
    expect(el.textContent).toContain('!hello is already answered by “Hello”. You can save this');
    set(field<HTMLSelectElement>(el, 'Who can use it'), 'mods');
    expect(el.textContent).toContain('!hello is already answered by “Hello”'); // both are for mods
    set(field<HTMLSelectElement>(el, 'Who can use it'), 'not-mods');
    expect(el.textContent).not.toContain('is already answered by'); // the one answer for mods doesn't meet the rest
    set(field<HTMLSelectElement>(el, 'Who can use it'), 'everyone');
    set(field(el, 'Command'), 'unique');
    expect(el.textContent).not.toContain('is already answered by');
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
