<script lang="ts">
  // Events: what StreamGremlin hears, as it arrives, and test events.
  // The list follows the inbox by cursor (GET /v1/events?after=&wait=), so
  // nothing is skipped; when the engine stops answering it says so instead
  // of looking live.
  import { onMount } from 'svelte';
  import {
    ApiError,
    getEventCatalog,
    getEvents,
    simulateEvent,
    type EventKind,
    type EventRecord,
  } from '../lib/api';
  import { eventSummary, eventTime } from '../lib/events';

  let {
    idleMs = 250,
    retryMs = 2000,
  }: {
    /** Pause after an empty answer; the engine already waits up to 20 s. */
    idleMs?: number;
    retryMs?: number;
  } = $props();

  const keep = 200;

  let shown = $state<EventRecord[]>([]); // newest first
  let listening = $state<'starting' | 'live' | 'lost'>('starting');
  let lostReason = $state('');
  let kinds = $state<EventKind[]>([]);
  let chosen = $state('');
  let values = $state<Record<string, string>>({});
  let sending = $state(false);
  let sent = $state('');
  let sendError = $state('');

  const kind = $derived(kinds.find((k) => k.type === chosen));
  const labelOf = (type: string) => kinds.find((k) => k.type === type)?.label ?? type;
  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));
  const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

  function add(recs: EventRecord[]) {
    if (recs.length === 0) return;
    shown = [...recs.slice().reverse(), ...shown].slice(0, keep);
  }

  onMount(() => {
    const abort = new AbortController();
    (async () => {
      try {
        kinds = (await getEventCatalog()).kinds;
        choose(kinds[0]?.type ?? '');
      } catch (e) {
        sendError = `Couldn't load the test events: ${message(e)}`;
      }
    })();
    (async () => {
      let cursor: number | undefined;
      while (!abort.signal.aborted) {
        try {
          if (cursor === undefined) {
            const page = await getEvents({ limit: 50 }, abort.signal);
            shown = [];
            add(page.events);
            cursor = page.head;
          } else {
            const page = await getEvents({ after: cursor, wait: 20, limit: 200 }, abort.signal);
            add(page.events);
            if (page.events.length > 0) cursor = page.events[page.events.length - 1].seq;
            if (page.head < cursor) cursor = undefined; // a different data folder: start again
            if (page.events.length === 0) await sleep(idleMs);
          }
          listening = 'live';
        } catch (e) {
          if (abort.signal.aborted) return;
          listening = 'lost';
          lostReason = message(e);
          await sleep(retryMs);
        }
      }
    })();
    return () => abort.abort();
  });

  function choose(type: string) {
    chosen = type;
    const k = kinds.find((x) => x.type === type);
    values = Object.fromEntries((k?.fields ?? []).map((f) => [f.name, f.default]));
    sent = '';
    sendError = '';
  }

  async function send(e: SubmitEvent) {
    e.preventDefault();
    if (!kind) return;
    sending = true;
    sent = '';
    sendError = '';
    try {
      // A new id per click; a retry of the same request can't make two events.
      await simulateEvent(kind.type, { ...values }, crypto.randomUUID());
      sent = `Sent a test ${kind.label.toLowerCase()}.`;
    } catch (err) {
      sendError = `Couldn't send it: ${message(err)}`;
    } finally {
      sending = false;
    }
  }
</script>

<h1>Events</h1>

<section class="sg-panel card" aria-labelledby="live-heading">
  <h2 id="live-heading">Live events</h2>
  <p class="state" role="status">
    {#if listening === 'live'}
      <span class="state-ok"><span aria-hidden="true">●</span></span> Listening. New events appear at the top.
    {:else if listening === 'lost'}
      <span class="state-unknown"><span aria-hidden="true">?</span></span> Not receiving events: {lostReason} Trying
      again…
    {:else}
      Connecting…
    {/if}
  </p>
  {#if shown.length === 0 && listening === 'live'}
    <p class="sg-muted">
      Nothing yet. Events from Twitch and OBS show up here once they're connected. Send a test event below to see one.
    </p>
  {/if}
  <ol class="event-list" aria-label="Events, newest first">
    {#each shown as ev (ev.seq)}
      <li class="event-row">
        <div class="event-head">
          <time datetime={ev.occurred_at} class="sg-muted">{eventTime(ev.occurred_at)}</time>
          <strong>{labelOf(ev.type)}</strong>
          {#if ev.delivery_quality === 'synthetic'}
            <span class="tag">Test</span>
          {:else if ev.delivery_quality === 'estimated'}
            <span class="tag">Estimated</span>
          {/if}
          <span class="sg-muted">from {ev.source}</span>
        </div>
        {#if eventSummary(ev)}
          <p class="event-summary">{eventSummary(ev)}</p>
        {/if}
        <details>
          <summary>Details</summary>
          <dl class="facts">
            <dt>Type</dt>
            <dd><code>{ev.type}</code></dd>
            <dt>Number</dt>
            <dd>#{ev.seq}</dd>
            <dt>Received</dt>
            <dd>{eventTime(ev.received_at)}</dd>
          </dl>
          <pre class="payload">{JSON.stringify(ev.payload, null, 2)}</pre>
        </details>
      </li>
    {/each}
  </ol>
</section>

<section class="sg-panel card" aria-labelledby="test-heading">
  <h2 id="test-heading">Send a test event</h2>
  <p class="sg-muted">
    Test events are marked Test. Later, workflows will run for them without doing anything on Twitch or OBS.
  </p>
  {#if kinds.length > 0}
    <form class="test-form" onsubmit={send}>
      <label class="field">
        <span>Event</span>
        <select class="sg-input" value={chosen} onchange={(e) => choose(e.currentTarget.value)}>
          {#each kinds as k (k.type)}
            <option value={k.type}>{k.label}</option>
          {/each}
        </select>
      </label>
      {#if kind}
        <p class="sg-muted field-hint">{kind.description}</p>
        {#each kind.fields as f (f.name)}
          <label class="field">
            <span>{f.label}</span>
            {#if f.kind === 'choice'}
              <select class="sg-input" bind:value={values[f.name]}>
                {#each f.choices ?? [] as c (c)}<option value={c}>{c}</option>{/each}
              </select>
            {:else}
              <input
                class="sg-input"
                type={f.kind === 'number' ? 'number' : 'text'}
                maxlength={f.kind === 'text' ? f.max : undefined}
                bind:value={values[f.name]}
              />
            {/if}
          </label>
        {/each}
      {/if}
      <div class="row-actions">
        <button type="submit" class="sg-button" disabled={sending || !kind}>Send test event</button>
      </div>
    </form>
  {/if}
  <p class="saved" role="status">{sent}</p>
  {#if sendError}
    <p class="error" role="alert">{sendError}</p>
  {/if}
</section>
