<script lang="ts">
  // Home: what needs you, the Get set up checklist (ticked from real state,
  // never from clicks), live events and runs, and whether everything is
  // connected. Unknown is shown as unknown (D-018 in docs/DECISIONS.md).
  import { onMount } from 'svelte';
  import {
    ApiError,
    getConnections,
    getEvents,
    getJobs,
    getOverview,
    markFailuresSeen,
    getStatus,
    getWorkflows,
    type EventRecord,
    type Job,
    type OBSStatus,
    type Overview,
    type Status,
    type TwitchStatus,
    type WorkflowSummary,
  } from '../lib/api';
  import { eventSummary, eventTime } from '../lib/events';
  import { duration } from '../lib/format';
  import { accountHealth, chatHealth, obsHealth, toneIcon, type Health } from '../lib/health';
  import { jobOk, jobWords } from '../lib/workflows';

  let {
    refreshMs = 5000,
    idleMs = 250,
    retryMs = 2000,
    ongo = () => undefined,
  }: { refreshMs?: number; idleMs?: number; retryMs?: number; ongo?: (screen: string) => void } = $props();

  const HIDDEN = 'streamgremlin.setup-hidden';
  const remembered = () => {
    try {
      return localStorage.getItem(HIDDEN) === '1';
    } catch {
      return false;
    }
  };

  let status = $state<Status | undefined>();
  let error = $state<string | undefined>();
  let now = $state(Date.now());
  let ov = $state<Overview | undefined>();
  let twitch = $state<TwitchStatus | undefined>();
  let obs = $state<OBSStatus | undefined>();
  let jobs = $state<Job[]>([]);
  let workflows = $state<WorkflowSummary[]>([]);
  let events = $state<EventRecord[]>([]);
  let live = $state<'starting' | 'live' | 'lost'>('starting');
  let hidden = $state(remembered());

  const screens: Record<string, string> = { connections: 'Connections', workflows: 'Workflows' };
  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));
  const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

  // The failed runs counted so far are seen; later ones count again.
  let seenError = $state('');
  async function markSeen() {
    seenError = '';
    try {
      await markFailuresSeen();
      await load();
    } catch (e) {
      seenError = `Couldn’t mark them as seen: ${message(e)}`;
    }
  }

  async function load() {
    try {
      status = await getStatus();
      error = undefined;
    } catch (e) {
      // Never keep showing "Running", or anything else, from an old answer.
      status = undefined;
      ov = undefined;
      twitch = undefined;
      obs = undefined;
      error = message(e);
      return;
    }
    await Promise.all([
      getOverview().then((o) => (ov = o)).catch(() => (ov = undefined)),
      getConnections()
        .then((c) => ((twitch = c.twitch), (obs = c.obs)))
        .catch(() => ((twitch = undefined), (obs = undefined))),
      getJobs().then((j) => (jobs = j.jobs.slice(0, 5))).catch(() => undefined),
      getWorkflows().then((w) => (workflows = w.workflows)).catch(() => undefined),
    ]);
  }

  onMount(() => {
    load();
    const refresh = setInterval(load, refreshMs);
    const tick = setInterval(() => (now = Date.now()), 1000);
    // Live events, followed by cursor like the Events screen.
    const abort = new AbortController();
    (async () => {
      let cursor: number | undefined;
      while (!abort.signal.aborted) {
        try {
          const page =
            cursor === undefined
              ? await getEvents({ limit: 8 }, abort.signal)
              : await getEvents({ after: cursor, wait: 20, limit: 50 }, abort.signal);
          if (cursor === undefined) events = [];
          events = [...page.events.slice().reverse(), ...events].slice(0, 8);
          cursor = page.events.length ? page.events[page.events.length - 1].seq : (cursor ?? page.head);
          if (page.head < cursor) cursor = undefined;
          live = 'live';
          if (page.events.length === 0) await sleep(idleMs);
        } catch {
          if (abort.signal.aborted) return;
          live = 'lost';
          await sleep(retryMs);
        }
      }
    })();
    return () => {
      clearInterval(refresh);
      clearInterval(tick);
      abort.abort();
    };
  });

  function setHidden(h: boolean) {
    hidden = h;
    try {
      if (h) localStorage.setItem(HIDDEN, '1');
      else localStorage.removeItem(HIDDEN);
    } catch {
      // Not remembered: it shows again next time, which is harmless.
    }
  }

  const uptime = $derived(status ? duration((now - Date.parse(status.started_at)) / 1000) : '');
  const required = $derived(ov?.checklist.filter((s) => !s.optional) ?? []);
  const doneCount = $derived(required.filter((s) => s.done).length);
  const nextStep = $derived(ov?.checklist.find((s) => !s.done && !s.optional));
  const wfName = (id: string) => workflows.find((w) => w.id === id)?.name ?? id;
</script>

{#snippet healthLine(label: string, h: Health)}
  <dt>{label}</dt>
  <dd class={`health health-${h.tone}`}><span aria-hidden="true">{toneIcon(h.tone)}</span> {h.text}</dd>
{/snippet}

<h1>Home</h1>

{#if ov}
  <section class="sg-panel card" aria-labelledby="attention-heading">
    <h2 id="attention-heading">Needs you</h2>
    {#if ov.attention.length === 0}
      <p class="state state-ok"><span aria-hidden="true">●</span> Nothing needs you right now.</p>
    {:else}
      <ul class="attention">
        {#each ov.attention as a (a.message)}
          <li class={`attention-${a.level}`}>
            <span class="attention-icon" aria-hidden="true">{a.level === 'problem' ? '!' : a.level === 'unknown' ? '?' : '•'}</span>
            <span class="visually-hidden">{a.level === 'problem' ? 'Problem:' : a.level === 'unknown' ? 'Unknown:' : 'Tip:'}</span>
            <span class="attention-text">{a.message}</span>
            <button type="button" class="button" onclick={() => ongo(a.screen)}>Open {screens[a.screen] ?? a.screen}</button>
            {#if a.action === 'mark_failures_seen'}
              <button type="button" class="button" onclick={markSeen}>Mark as seen</button>
              {#if seenError}<span class="error" role="alert">{seenError}</span>{/if}
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
    {#if !ov.set_up && hidden}
      <button type="button" class="button" onclick={() => setHidden(false)}>Show the Get set up checklist</button>
    {/if}
  </section>

  {#if !ov.set_up && !hidden}
    <section class="sg-panel card" aria-labelledby="setup-heading">
      <h2 id="setup-heading">Get set up</h2>
      <p class="sg-muted">{doneCount} of {required.length} done. Each step ticks itself when it’s really done.</p>
      <ol class="checklist">
        {#each ov.checklist as s (s.id)}
          <li class={s.done ? 'step-done' : s === nextStep ? 'step-next' : ''}>
            <span class="check" aria-hidden="true">{s.done ? '✓' : '○'}</span>
            <span class="step-text">
              <strong>{s.label}</strong>{s.optional ? ' (optional)' : ''}<span class="visually-hidden">{s.done ? ': done' : ': not done yet'}</span>
              {#if !s.done}<span class="sg-muted step-help">{s.help}</span>{/if}
            </span>
            {#if !s.done}
              <button type="button" class={s === nextStep ? 'sg-button' : 'button'} onclick={() => ongo(s.screen)}
                >Open {screens[s.screen] ?? s.screen}</button
              >
            {/if}
          </li>
        {/each}
      </ol>
      <button type="button" class="button" onclick={() => setHidden(true)}>Hide the checklist</button>
    </section>
  {/if}
{/if}

<section class="sg-panel card" aria-labelledby="live-heading">
  <h2 id="live-heading">Happening now</h2>
  {#if live === 'lost'}
    <p class="state state-unknown"><span aria-hidden="true">?</span> Unknown: not hearing from the engine. Trying again.</p>
  {/if}
  <div class="live-grid">
    <div>
      <h3>Events</h3>
      {#if events.length === 0}
        <p class="sg-muted">Nothing yet. Chat messages and OBS changes show here as they happen.</p>
      {/if}
      <ul class="live-list" aria-label="Latest events">
        {#each events as ev (ev.seq)}
          <li>
            <span class="sg-muted">{eventTime(ev.received_at)}</span>
            {eventSummary(ev) || ev.type}
            {#if ev.delivery_quality === 'synthetic'}<span class="tag">Test</span>{/if}
          </li>
        {/each}
      </ul>
    </div>
    <div>
      <h3>Workflow runs</h3>
      {#if jobs.length === 0}
        <p class="sg-muted">No runs yet.</p>
      {/if}
      <ul class="live-list" aria-label="Latest runs">
        {#each jobs as j (j.id)}
          <li>
            <span class={jobOk(j) ? 'icon-ok' : 'icon-bad'} aria-hidden="true">{jobOk(j) ? '✓' : '✗'}</span>
            {wfName(j.workflow_id)}: {jobWords(j)}
            {#if j.dry_run}<span class="tag">Test</span>{/if}
            <span class="sg-muted">{eventTime(j.created_at)}</span>
          </li>
        {/each}
      </ul>
    </div>
  </div>
</section>

<section class="sg-panel card" aria-labelledby="conn-heading">
  <h2 id="conn-heading">Connections</h2>
  {#if twitch && obs}
    <dl class="facts">
      {@render healthLine('Twitch', accountHealth(twitch.streamer))}
      {@render healthLine('Bot', accountHealth(twitch.bot))}
      {@render healthLine('Chat', chatHealth(twitch.chat))}
      {@render healthLine('OBS', obsHealth(obs))}
    </dl>
  {:else}
    <p class="state state-unknown"><span aria-hidden="true">?</span> Unknown{error ? `: ${error}` : ''}</p>
  {/if}
</section>

<section class="sg-panel card" aria-labelledby="engine-heading">
  <h2 id="engine-heading">Engine</h2>
  {#if error}
    <p class="state state-unknown"><span aria-hidden="true">?</span> Unknown: {error}</p>
  {:else if !status}
    <p class="sg-muted">Checking…</p>
  {:else}
    <p class="state state-ok"><span aria-hidden="true">●</span> Running</p>
    <dl class="facts">
      <dt>Version</dt>
      <dd>{status.version} ({status.commit})</dd>
      <dt>Running for</dt>
      <dd>{uptime}</dd>
      <dt>Data folder</dt>
      <dd><code>{status.data_dir}</code></dd>
      <dt>Database schema</dt>
      <dd>{status.schema_version}</dd>
    </dl>
  {/if}
</section>
