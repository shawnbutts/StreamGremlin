<script lang="ts">
  // Live (D-075): run the stream from one place. Twitch chat fills the
  // middle, with a box to send; the streamer's buttons sit beside it or
  // above it, and pressing one runs the workflows that start from it.
  // Chat follows the inbox by cursor, like Events; when the engine stops
  // answering it says so instead of looking live.
  import { onMount, tick } from 'svelte';
  import {
    ApiError,
    BUTTON_COLORS,
    deleteButton,
    getButtons,
    getConnections,
    getEvents,
    getUISettings,
    getWorkflows,
    orderButtons,
    pressButton,
    putUISettings,
    saveButton,
    sendChat,
    type ButtonColor,
    type LiveButton,
    type LivePlace,
    type TwitchStatus,
    type WorkflowSummary,
  } from '../lib/api';
  import { eventTime } from '../lib/events';
  import { CHAT_MESSAGE, buttonId, chatLine, pressWords, startsFrom, startsWords, starterWorkflow, type ChatLine } from '../lib/live';
  import ConfirmDialog from '../lib/ConfirmDialog.svelte';
  import WorkflowEditor from '../editor/WorkflowEditor.svelte';

  let {
    idleMs = 250,
    retryMs = 2000,
    refreshMs = 5000,
  }: {
    /** Pause after an empty answer; the engine already waits up to 20 s. */
    idleMs?: number;
    retryMs?: number;
    /** How often the chat connection and the buttons' workflows are checked. */
    refreshMs?: number;
  } = $props();

  const keep = 300;
  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));
  const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
  const newRequestId = () =>
    typeof crypto !== 'undefined' && 'randomUUID' in crypto ? crypto.randomUUID() : `${Date.now()}-${Math.random().toString(36).slice(2)}`;

  // Chat.
  let lines = $state<ChatLine[]>([]); // oldest first
  let listening = $state<'starting' | 'live' | 'lost'>('starting');
  let lostReason = $state('');
  let twitch = $state<TwitchStatus | undefined>();
  let twitchError = $state('');
  let logEl = $state<HTMLDivElement | undefined>();
  let atBottom = $state(true);
  let unseen = $state(0);

  // Sending.
  let text = $state('');
  let as = $state<'streamer' | 'bot'>('streamer');
  let sending = $state(false);
  let sendError = $state('');
  const youIn = $derived(twitch?.streamer.state === 'connected');
  const botIn = $derived(twitch?.bot.state === 'connected');

  // Buttons.
  let place = $state<LivePlace>('right');
  let placeError = $state('');
  let buttons = $state<LiveButton[]>([]);
  let workflows = $state<WorkflowSummary[]>([]);
  let buttonsError = $state('');
  let loaded = $state(false);
  let pressing = $state<string | undefined>();
  let pressed = $state('');
  let pressError = $state('');
  let editing = $state(false);
  let form = $state<{ id?: string; label: string; color: ButtonColor } | undefined>();
  let formError = $state('');
  let saving = $state(false);
  let notice = $state('');
  let confirmDelete = $state<LiveButton | undefined>();
  let starting = $state<ReturnType<typeof starterWorkflow> | undefined>();

  function add(recs: Parameters<typeof chatLine>[0][]) {
    if (recs.length === 0) return;
    const fresh = recs.map(chatLine);
    lines = [...lines, ...fresh].slice(-keep);
    if (atBottom) {
      tick().then(() => logEl?.scrollTo?.({ top: logEl.scrollHeight }));
    } else {
      unseen += fresh.length;
    }
  }

  function onScroll() {
    if (!logEl) return;
    atBottom = logEl.scrollHeight - logEl.scrollTop - logEl.clientHeight < 24;
    if (atBottom) unseen = 0;
  }

  function toBottom() {
    logEl?.scrollTo?.({ top: logEl.scrollHeight });
    atBottom = true;
    unseen = 0;
  }

  async function loadTwitch() {
    try {
      twitch = (await getConnections()).twitch;
      twitchError = '';
    } catch (e) {
      twitchError = message(e);
    }
  }

  async function loadButtons() {
    try {
      const [b, w] = await Promise.all([getButtons(), getWorkflows()]);
      buttons = b.buttons;
      workflows = w.workflows;
      buttonsError = '';
    } catch (e) {
      buttonsError = message(e);
    } finally {
      loaded = true;
    }
  }

  onMount(() => {
    const abort = new AbortController();
    getUISettings()
      .then((ui) => (place = ui.live_buttons ?? 'right'))
      .catch(() => undefined);
    loadTwitch();
    loadButtons();
    const timer = setInterval(() => {
      loadTwitch();
      if (!editing && !form) loadButtons();
    }, refreshMs);
    abort.signal.addEventListener('abort', () => clearInterval(timer));
    (async () => {
      let cursor: number | undefined;
      while (!abort.signal.aborted) {
        try {
          if (cursor === undefined) {
            const page = await getEvents({ limit: 100, types: [CHAT_MESSAGE] }, abort.signal);
            lines = [];
            add(page.events);
            cursor = page.head;
          } else {
            const page = await getEvents({ after: cursor, wait: 20, limit: 200, types: [CHAT_MESSAGE] }, abort.signal);
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

  async function send(e: SubmitEvent) {
    e.preventDefault();
    if (!text.trim()) return;
    sending = true;
    sendError = '';
    try {
      await sendChat(text.trim(), botIn ? as : 'streamer');
      text = '';
    } catch (err) {
      // Not retried on purpose: Twitch can't tell a repeat from a new message.
      sendError = `Not sent: ${message(err)}`;
    } finally {
      sending = false;
    }
  }

  async function setPlace(p: LivePlace) {
    if (p === place) return;
    const before = place;
    place = p;
    placeError = '';
    try {
      await putUISettings({ live_buttons: p });
    } catch (e) {
      place = before; // not saved: don't pretend
      placeError = `Couldn’t move the buttons: ${message(e)}`;
    }
  }

  async function press(b: LiveButton) {
    pressing = b.id;
    pressed = '';
    pressError = '';
    try {
      const res = await pressButton(b.id, newRequestId());
      pressed = pressWords(res.button.label, res.started, res.duplicate);
    } catch (e) {
      pressError = `“${b.label}” wasn’t pressed: ${message(e)}`;
    } finally {
      pressing = undefined;
    }
  }

  function newButton() {
    form = { label: '', color: '' };
    formError = '';
    notice = '';
  }

  function editButton(b: LiveButton) {
    form = { id: b.id, label: b.label, color: b.color };
    formError = '';
    notice = '';
  }

  async function saveForm(e: SubmitEvent) {
    e.preventDefault();
    if (!form) return;
    const label = form.label.trim();
    if (!label) {
      formError = 'A button needs a label.';
      return;
    }
    if ([...label].length > 40) {
      formError = 'A label is up to 40 characters.';
      return;
    }
    saving = true;
    formError = '';
    const id = form.id ?? buttonId(label, buttons.map((b) => b.id));
    try {
      const saved = await saveButton(id, label, form.color);
      const fresh = !form.id;
      form = undefined;
      await loadButtons();
      notice =
        `Saved “${saved.label}”.` +
        (fresh && startsFrom(saved.id, workflows).length === 0 ? ' No workflow starts from it yet: “Make a workflow” gives it something to do.' : '');
    } catch (err) {
      formError = message(err);
    } finally {
      saving = false;
    }
  }

  async function move(b: LiveButton, by: -1 | 1) {
    const ids = buttons.map((x) => x.id);
    const i = ids.indexOf(b.id);
    const j = i + by;
    if (j < 0 || j >= ids.length) return;
    [ids[i], ids[j]] = [ids[j], ids[i]];
    notice = '';
    try {
      buttons = (await orderButtons(ids)).buttons;
      notice = `Moved “${b.label}” ${by < 0 ? 'earlier' : 'later'}.`;
    } catch (e) {
      buttonsError = message(e);
    }
  }

  function deleteWords(b: LiveButton): string {
    const ws = startsFrom(b.id, workflows);
    const kept = ws.length === 0 ? '' : ` ${ws.map((w) => `“${w.name}”`).join(', ')} ${ws.length === 1 ? 'is' : 'are'} kept, but won’t start from it any more.`;
    return `“${b.label}” is deleted for good.${kept}`;
  }

  async function confirmDeleting() {
    const b = confirmDelete!;
    confirmDelete = undefined;
    try {
      await deleteButton(b.id);
      notice = `Deleted “${b.label}”.`;
      await loadButtons();
    } catch (e) {
      buttonsError = message(e);
    }
  }

  const colorName = (c: ButtonColor) => (c ? c[0].toUpperCase() + c.slice(1) : 'None');
</script>

{#if starting}
  <WorkflowEditor
    initial={starting.def}
    fresh
    blanks={starting.blanks}
    title="A workflow for a button"
    taken={workflows.map((w) => w.id)}
    {workflows}
    onsaved={(w) => {
      starting = undefined;
      notice = `Saved “${w.name}”. It’s off until you turn it on, on the Workflows screen.`;
      loadButtons();
    }}
    oncancel={() => (starting = undefined)}
  />
{:else}
  <div class="live-head">
    <h1>Live</h1>
    <p class="live-status" aria-live="polite">
      {#if listening === 'lost'}
        <span class="state-unknown"><span aria-hidden="true">?</span> Unknown: not hearing the engine ({lostReason}). Trying again…</span>
      {:else if twitchError}
        <span class="state-unknown"><span aria-hidden="true">?</span> Chat’s state is unknown: {twitchError}</span>
      {:else if !twitch}
        Checking chat…
      {:else if twitch.chat.state === 'connected'}
        <span class="state-ok"><span aria-hidden="true">●</span> Reading chat</span>
      {:else if twitch.chat.state === 'connecting'}
        Connecting to chat…
      {:else if twitch.chat.state === 'disconnected'}
        <span class="state-unknown"><span aria-hidden="true">?</span> Not reading chat right now{twitch.chat.detail ? `: ${twitch.chat.detail}` : ''}</span>
      {:else}
        Not reading chat: sign in to Twitch on Connections.
      {/if}
    </p>
    <div class="live-place" role="group" aria-label="Where the buttons go">
      <span class="sg-muted">Buttons:</span>
      <button type="button" class="button" aria-pressed={place === 'right'} onclick={() => setPlace('right')}>Right</button>
      <button type="button" class="button" aria-pressed={place === 'top'} onclick={() => setPlace('top')}>Top</button>
    </div>
  </div>
  {#if placeError}<p class="error" role="alert">{placeError}</p>{/if}

  <div class="live" data-place={place}>
    <section class="sg-panel live-chat" aria-labelledby="live-chat-heading">
      <h2 id="live-chat-heading">Chat</h2>
      {#if lines.length === 0}
        <p class="sg-muted live-empty">{listening === 'starting' ? 'Loading chat…' : 'No chat yet. Messages show here as they arrive.'}</p>
      {/if}
      <!-- The log scrolls, so it takes focus for the keyboard's arrow keys. -->
      <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
      <div class="live-log" role="log" aria-label="Chat messages" bind:this={logEl} onscroll={onScroll} tabindex="0">
      <ol class="live-lines">
        {#each lines as l (l.seq)}
          <li class="live-line">
            <span class="sg-muted live-time">{eventTime(l.at)}</span>
            {#if l.color}<span class="live-swatch" style:background={l.color} aria-hidden="true"></span>{/if}
            <strong class="live-name">{l.name}</strong>{#each l.badges as b (b)}<span class="live-badge">{b}</span>{/each}{#if l.test}<span
                class="live-badge">Test</span
              >{/if}{#if l.shared}<span class="live-badge">Shared chat</span>{/if}<span class="live-text">: {l.text}</span>
          </li>
        {/each}
      </ol>
      </div>
      {#if unseen > 0}
        <button type="button" class="button live-more" onclick={toBottom}>{unseen} new {unseen === 1 ? 'message' : 'messages'} ↓</button>
      {/if}
      {#if youIn}
        <form class="live-send" onsubmit={send}>
          <label class="live-send-text">
            <span class="visually-hidden">Message</span>
            <input class="sg-input" bind:value={text} maxlength="500" autocomplete="off" placeholder="Send a message" />
          </label>
          {#if botIn}
            <label>
              <span class="visually-hidden">Send as</span>
              <select class="sg-input" bind:value={as}>
                <option value="streamer">As you ({twitch?.streamer.login})</option>
                <option value="bot">As the bot ({twitch?.bot.login})</option>
              </select>
            </label>
          {/if}
          <button type="submit" class="sg-button" disabled={sending || !text.trim()}>Send</button>
        </form>
      {:else if twitch}
        <p class="sg-muted">Sign in to Twitch on Connections to send messages from here.</p>
      {/if}
      {#if sendError}<p class="error" role="alert">{sendError}</p>{/if}
    </section>

    <section class="sg-panel live-buttons" aria-labelledby="live-buttons-heading">
      <div class="live-buttons-head">
        <h2 id="live-buttons-heading">Buttons</h2>
        <button type="button" class="button" aria-pressed={editing} onclick={() => ((editing = !editing), (form = undefined), (notice = ''))}
          >{editing ? 'Done' : 'Edit buttons'}</button
        >
      </div>
      {#if buttonsError}
        <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {buttonsError}</p>
      {/if}
      {#if loaded && buttons.length === 0 && !form}
        <p class="sg-muted">No buttons yet. A button runs the workflows that start from it: be right back, a sound, a scene.</p>
      {/if}
      {#if !editing}
        <ul class="live-grid">
          {#each buttons as b (b.id)}
            <li>
              <button
                type="button"
                class="live-press"
                data-color={b.color || undefined}
                disabled={pressing === b.id}
                title={startsWords(b.id, workflows)}
                onclick={() => press(b)}>{b.label}</button
              >
            </li>
          {/each}
        </ul>
        <p class="saved" role="status">{pressed}</p>
        {#if pressError}<p class="error" role="alert">{pressError}</p>{/if}
      {:else}
        <ul class="live-edit-list">
          {#each buttons as b, i (b.id)}
            <li class="live-edit-item" data-color={b.color || undefined}>
              <div>
                <strong>{b.label}</strong>
                <span class="sg-muted">{colorName(b.color)}</span>
                <p class="sg-muted live-starts">{startsWords(b.id, workflows)}</p>
              </div>
              <div class="row-actions">
                <button type="button" class="button" onclick={() => editButton(b)}>Edit</button>
                {#if startsFrom(b.id, workflows).length === 0}
                  <button type="button" class="button" onclick={() => (starting = starterWorkflow(b))}>Make a workflow</button>
                {/if}
                <button type="button" class="button" disabled={i === 0} aria-label={`Move “${b.label}” earlier`} onclick={() => move(b, -1)}
                  >↑</button
                >
                <button
                  type="button"
                  class="button"
                  disabled={i === buttons.length - 1}
                  aria-label={`Move “${b.label}” later`}
                  onclick={() => move(b, 1)}>↓</button
                >
                <button type="button" class="button button-danger" onclick={() => (confirmDelete = b)}>Delete</button>
              </div>
            </li>
          {/each}
        </ul>
        {#if !form}
          <button type="button" class="sg-button" onclick={newButton}>New button</button>
        {/if}
      {/if}
      {#if form}
        <form class="live-form" onsubmit={saveForm} aria-label={form.id ? 'Change a button' : 'A new button'}>
          <label class="field">
            <span>Label</span>
            <input class="sg-input" bind:value={form.label} maxlength="40" placeholder="Be right back" />
          </label>
          <label class="field">
            <span>Colour</span>
            <select class="sg-input" bind:value={form.color}>
              <option value="">None</option>
              {#each BUTTON_COLORS as c (c)}<option value={c}>{colorName(c)}</option>{/each}
            </select>
          </label>
          {#if formError}<p class="error" role="alert">{formError}</p>{/if}
          <div class="row-actions">
            <button type="submit" class="sg-button" disabled={saving}>Save</button>
            <button type="button" class="button" onclick={() => (form = undefined)}>Cancel</button>
          </div>
        </form>
      {/if}
      <p class="saved" role="status">{notice}</p>
    </section>
  </div>
{/if}

{#if confirmDelete}
  <ConfirmDialog
    title="Delete this button?"
    message={deleteWords(confirmDelete)}
    confirmLabel="Delete"
    onconfirm={confirmDeleting}
    oncancel={() => (confirmDelete = undefined)}
  />
{/if}

<style>
  .live-head {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 0.5rem 1.25rem;
  }
  .live-head h1 {
    margin-bottom: 0.5rem;
  }
  .live-status {
    margin: 0;
    flex: 1;
  }
  .live-place {
    display: flex;
    align-items: center;
    gap: 0.4rem;
  }
  .live-place .button[aria-pressed='true'],
  .live-buttons-head .button[aria-pressed='true'] {
    border-color: var(--sg-primary);
    box-shadow: inset 0 0 0 1px var(--sg-primary);
  }
  .live {
    display: grid;
    gap: 1rem;
    grid-template-columns: minmax(0, 1fr) minmax(14rem, 18rem);
    grid-template-areas: 'chat buttons';
    align-items: start;
  }
  .live[data-place='top'] {
    grid-template-columns: minmax(0, 1fr);
    grid-template-areas: 'buttons' 'chat';
  }
  .live-chat {
    grid-area: chat;
    display: flex;
    flex-direction: column;
    min-height: 24rem;
    height: calc(100vh - 9rem);
  }
  .live[data-place='top'] .live-chat {
    height: auto;
    min-height: 20rem;
  }
  .live-buttons {
    grid-area: buttons;
  }
  .live-buttons-head {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    gap: 0.5rem;
  }
  .live-log {
    flex: 1;
    overflow-y: auto;
    padding: 0.25rem 0;
    min-height: 10rem;
  }
  .live-lines {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .live-line {
    padding: 0.2rem 0;
    overflow-wrap: anywhere;
  }
  .live-time {
    font-size: 0.8rem;
    margin-right: 0.4rem;
  }
  .live-swatch {
    display: inline-block;
    width: 0.6rem;
    height: 0.6rem;
    border-radius: 50%;
    margin-right: 0.3rem;
    border: 1px solid var(--sg-border);
  }
  .live-badge {
    font-size: 0.75rem;
    border: 1px solid var(--sg-border);
    border-radius: 4px;
    padding: 0 0.3rem;
    margin-left: 0.3rem;
    color: var(--sg-muted);
  }
  .live-more {
    align-self: center;
    margin: 0.25rem 0;
  }
  .live-send {
    display: flex;
    gap: 0.5rem;
    margin-top: 0.75rem;
    flex-wrap: wrap;
  }
  .live-send-text {
    flex: 1;
    min-width: 12rem;
  }
  .live-send-text input {
    width: 100%;
  }
  .live-grid {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 0.5rem;
    grid-template-columns: repeat(auto-fill, minmax(7.5rem, 1fr));
  }
  .live[data-place='right'] .live-grid {
    grid-template-columns: 1fr;
  }
  .live-press {
    width: 100%;
    min-height: 3rem;
    font: inherit;
    font-weight: 650;
    color: var(--sg-foreground);
    background: var(--sg-surface-raised);
    border: 1px solid var(--sg-border);
    border-left: 6px solid var(--live-color, var(--sg-border));
    border-radius: 8px;
    padding: 0.5rem 0.75rem;
    cursor: pointer;
    text-align: left;
  }
  .live-press:hover {
    background: var(--sg-surface-hover);
  }
  .live-press:disabled {
    cursor: progress;
    opacity: 0.75;
  }
  .live-edit-list {
    list-style: none;
    margin: 0 0 0.75rem;
    padding: 0;
  }
  .live-edit-item {
    border-left: 6px solid var(--live-color, var(--sg-border));
    padding: 0.4rem 0 0.4rem 0.6rem;
    margin-bottom: 0.5rem;
  }
  .live-starts {
    margin: 0.2rem 0 0.4rem;
    font-size: 0.9rem;
  }
  .live-form {
    margin-top: 0.75rem;
  }
  [data-color='red'] {
    --live-color: #d9534f;
  }
  [data-color='orange'] {
    --live-color: #e8892b;
  }
  [data-color='yellow'] {
    --live-color: #e6c229;
  }
  [data-color='green'] {
    --live-color: #4caf50;
  }
  [data-color='teal'] {
    --live-color: #26a69a;
  }
  [data-color='blue'] {
    --live-color: #3e8ed0;
  }
  [data-color='purple'] {
    --live-color: #9567d4;
  }
  [data-color='pink'] {
    --live-color: #e06ba8;
  }
  @media (max-width: 52rem) {
    .live {
      grid-template-columns: minmax(0, 1fr);
      grid-template-areas: 'buttons' 'chat';
    }
    .live-chat {
      height: auto;
    }
  }
</style>
