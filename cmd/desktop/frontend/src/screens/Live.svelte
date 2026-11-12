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
    getChatModes,
    getConnections,
    getModeration,
    getEvents,
    getUISettings,
    getWorkflows,
    moderate,
    orderButtons,
    pressButton,
    putUISettings,
    saveButton,
    sendChat,
    type ButtonColor,
    type ChatModes,
    type ChatModesChange,
    type EventRecord,
    type ModAction,
    type ModRecord,
    type LiveButton,
    type LivePlace,
    type TwitchStatus,
    type WorkflowSummary,
  } from '../lib/api';
  import { eventTime } from '../lib/events';
  import {
    CHAT_MESSAGE,
    SUSPICIOUS_MESSAGE,
    buttonId,
    chatLine,
    historyWords,
    lengthWords,
    modWords,
    modesWords,
    newRequestId,
    pressWords,
    startsFrom,
    startsWords,
    starterWorkflow,
    suspiciousLine,
    type ChatLine, badgeLetter, confirmWords, type ConfirmWords } from '../lib/live';
  import ConfirmDialog from '../lib/ConfirmDialog.svelte';
  import ViewerCard from './ViewerCard.svelte';
  import WorkflowEditor from '../editor/WorkflowEditor.svelte';
  import { onOffWords } from '../lib/workflows';

  let {
    idleMs = 250,
    retryMs = 2000,
    refreshMs = 5000,
    hoverMs = 350,
  }: {
    /** Pause after an empty answer; the engine already waits up to 20 s. */
    idleMs?: number;
    retryMs?: number;
    /** How often the chat connection and the buttons' workflows are checked. */
    refreshMs?: number;
    /** How long the pointer rests on a name before its card opens. */
    hoverMs?: number;
  } = $props();

  const keep = 300;
  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));
  const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

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

  // The viewer card (D-078): hovering a name opens it; clicking or Enter
  // pins it open and moves focus into it; Escape or Close shuts it.
  let card = $state<{ line: ChatLine; anchor?: DOMRect; pinned: boolean; from?: HTMLElement } | undefined>();
  let hoverTimer: ReturnType<typeof setTimeout> | undefined;
  let leaveTimer: ReturnType<typeof setTimeout> | undefined;
  const clearTimers = () => {
    clearTimeout(hoverTimer);
    clearTimeout(leaveTimer);
  };

  function hoverName(l: ChatLine, el: HTMLElement) {
    clearTimers();
    if (card?.pinned) return;
    hoverTimer = setTimeout(() => (card = { line: l, anchor: el.getBoundingClientRect(), pinned: false, from: el }), hoverMs);
  }

  function leaveName() {
    clearTimeout(hoverTimer);
    if (card && !card.pinned) leaveTimer = setTimeout(() => (card = card?.pinned ? card : undefined), 250);
  }

  function openCard(l: ChatLine, el: HTMLElement) {
    clearTimers();
    if (card?.pinned && card.line.seq === l.seq) {
      closeCard(true);
      return;
    }
    card = { line: l, anchor: el.getBoundingClientRect(), pinned: true, from: el };
  }

  function closeCard(restoreFocus: boolean) {
    clearTimers();
    const from = card?.from;
    card = undefined;
    if (restoreFocus) from?.focus();
  }

  // A chat message and Twitch's suspicious-user flag on it arrive as two
  // events; they're one line, matched by the message's id, in either order.
  function add(recs: EventRecord[]) {
    if (recs.length === 0) return;
    const next = [...lines];
    let fresh = 0;
    for (const ev of recs) {
      const l = ev.type === SUSPICIOUS_MESSAGE ? suspiciousLine(ev) : chatLine(ev);
      const i = l.messageId ? next.findIndex((x) => x.messageId === l.messageId) : -1;
      if (i < 0) {
        next.push(l);
        fresh++;
      } else if (ev.type === SUSPICIOUS_MESSAGE) {
        next[i] = { ...next[i], suspicious: l.suspicious };
      } else {
        next[i] = { ...l, seq: next[i].seq, suspicious: next[i].suspicious, deleted: next[i].deleted };
      }
    }
    lines = next.slice(-keep);
    if (fresh === 0) return;
    if (atBottom) {
      tick().then(() => logEl?.scrollTo?.({ top: logEl.scrollHeight }));
    } else {
      unseen += fresh;
    }
  }

  // Moderating from a chat line (D-079): delete, a 10-minute timeout, and
  // ban (asked first). The card has the rest.
  let modNotice = $state('');
  let modProblem = $state('');
  // Actions that can't be taken back ask first (confirmWords).
  let asking = $state<{ line: ChatLine; action: ModAction; extra: { seconds?: number; message_id?: string }; words: ConfirmWords } | undefined>();

  function askThen(l: ChatLine, action: ModAction, extra: { seconds?: number; message_id?: string } = {}) {
    const words = confirmWords(action, l.name, { seconds: extra.seconds, text: action === 'delete' ? l.text : undefined });
    if (words) asking = { line: l, action, extra, words };
    else moderateLine(l, action, extra);
  }
  const canModerate = (l: ChatLine) => youIn && !l.test && !!l.userId && l.userId !== twitch?.streamer.user_id;

  // The gear beside a name opens a small menu of quick actions, so they
  // stay next to the name however long the message is.
  let gearMenu = $state<{ line: ChatLine; anchor: DOMRect; from: HTMLElement } | undefined>();
  let menuEl = $state<HTMLElement | undefined>();

  function toggleGear(l: ChatLine, el: HTMLElement) {
    if (gearMenu?.line.seq === l.seq) {
      closeGear(true);
      return;
    }
    gearMenu = { line: l, anchor: el.getBoundingClientRect(), from: el };
    tick().then(() => menuEl?.querySelector<HTMLElement>('[role="menuitem"]')?.focus());
  }

  function closeGear(restoreFocus: boolean) {
    const from = gearMenu?.from;
    gearMenu = undefined;
    if (restoreFocus) from?.focus();
  }

  // The menu's line and gear are read before it closes, since closing
  // clears them.
  function gearPick(run: (l: ChatLine, from: HTMLElement) => void) {
    if (!gearMenu) return;
    const { line, from } = gearMenu;
    closeGear(false);
    run(line, from);
  }

  function gearKey(e: KeyboardEvent) {
    const items = Array.from(menuEl?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? []);
    const i = items.indexOf(document.activeElement as HTMLElement);
    const go = (n: number) => {
      e.preventDefault();
      items[(n + items.length) % items.length]?.focus();
    };
    if (e.key === 'ArrowDown') go(i + 1);
    else if (e.key === 'ArrowUp') go(i - 1);
    else if (e.key === 'Home') go(0);
    else if (e.key === 'End') go(items.length - 1);
    else if (e.key === 'Escape' || e.key === 'Tab') {
      if (e.key === 'Escape') e.preventDefault();
      closeGear(e.key === 'Escape');
    }
  }

  function gearOutside(e: PointerEvent) {
    if (!gearMenu) return;
    const t = e.target as Node;
    if (menuEl?.contains(t) || gearMenu.from.contains(t)) return;
    closeGear(false);
  }

  function gearStyle(a: DOMRect): string {
    if (typeof window === 'undefined') return '';
    const w = 220;
    const left = Math.max(8, Math.min(a.left, window.innerWidth - w - 8));
    return window.innerHeight - a.bottom > 180 ? `left:${left}px;top:${a.bottom + 2}px` : `left:${left}px;bottom:${window.innerHeight - a.top + 2}px`;
  }

  async function moderateLine(l: ChatLine, action: ModAction, extra: { seconds?: number; message_id?: string } = {}) {
    modNotice = '';
    modProblem = '';
    try {
      const res = await moderate({ request_id: newRequestId(), action, platform: 'twitch', user_id: l.userId, login: l.login, from: 'live', ...extra });
      moderated(res.record);
      const words = modWords(res.record, l.name, res.duplicate);
      if (res.record.state === 'done') modNotice = words;
      else modProblem = words;
    } catch (e) {
      modProblem = `Not sent: ${message(e)}`;
    }
  }

  function moderated(r: ModRecord) {
    if (r.state === 'done' && r.action === 'delete' && r.message_id) {
      lines = lines.map((x) => (x.messageId === r.message_id ? { ...x, deleted: true } : x));
    }
    if (logOpen) loadLog();
  }

  // Chat modes (D-079).
  let modes = $state<ChatModes | null>(null);
  let modesProblem = $state('');
  let modesBusy = $state(false);
  let modesNotice = $state('');
  let followMinutes = $state(10);
  let slowSeconds = $state(30);
  const FOLLOW_CHOICES = [0, 10, 30, 60, 1440, 10080];
  const SLOW_CHOICES = [3, 5, 10, 30, 60, 120];

  async function loadModes() {
    if (modesBusy) return;
    try {
      const v = await getChatModes();
      modes = v.settings;
      modesProblem = v.settings ? '' : (v.problem ?? 'Twitch didn’t say.');
      if (v.settings?.follower_mode_duration != null) followMinutes = v.settings.follower_mode_duration;
      if (v.settings?.slow_mode_wait_time != null) slowSeconds = v.settings.slow_mode_wait_time;
    } catch (e) {
      modes = null;
      modesProblem = message(e);
    }
  }

  async function changeModes(change: ChatModesChange) {
    modesBusy = true;
    modesNotice = '';
    try {
      const res = await moderate({ request_id: newRequestId(), action: 'chat_modes', platform: 'twitch', chat_modes: change, from: 'live' });
      if (res.record.state === 'done' && res.record.settings) {
        modes = res.record.settings;
        modesNotice = modesWords(res.record.settings);
        modesProblem = '';
      } else {
        modesProblem = modWords(res.record);
      }
      if (logOpen) loadLog();
    } catch (e) {
      modesProblem = `Not sent: ${message(e)}`;
    } finally {
      modesBusy = false;
    }
  }

  // What was done from StreamGremlin.
  let logOpen = $state(false);
  let log = $state<ModRecord[] | undefined>();
  let logProblem = $state('');
  async function loadLog() {
    try {
      log = (await getModeration(20)).moderation;
      logProblem = '';
    } catch (e) {
      logProblem = message(e);
    }
  }

  function onScroll() {
    if (gearMenu) closeGear(false);
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
    loadModes();
    const timer = setInterval(() => {
      loadTwitch();
      loadModes();
      if (!editing && !form) loadButtons();
    }, refreshMs);
    abort.signal.addEventListener('abort', () => clearInterval(timer));
    (async () => {
      let cursor: number | undefined;
      while (!abort.signal.aborted) {
        try {
          if (cursor === undefined) {
            const page = await getEvents({ limit: 100, types: [CHAT_MESSAGE, SUSPICIOUS_MESSAGE] }, abort.signal);
            lines = [];
            add(page.events);
            cursor = page.head;
          } else {
            const page = await getEvents({ after: cursor, wait: 20, limit: 200, types: [CHAT_MESSAGE, SUSPICIOUS_MESSAGE] }, abort.signal);
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
    return () => {
      abort.abort();
      clearTimers();
    };
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
    onsaved={(w, _changed, offBecause) => {
      starting = undefined;
      notice = `Saved “${w.name}”. ${onOffWords(w, offBecause)}`;
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
          <li class="live-line" class:live-deleted={l.deleted} class:live-flagged={!!l.suspicious}>
            <span class="sg-muted live-time">{eventTime(l.at)}</span>
            {#if l.color}<span class="live-swatch" style:background={l.color} aria-hidden="true"></span>{/if}
            <button
              type="button"
              class="live-name"
              aria-haspopup="dialog"
              aria-expanded={card?.line.seq === l.seq}
              onpointerenter={(e) => hoverName(l, e.currentTarget)}
              onpointerleave={leaveName}
              onclick={(e) => openCard(l, e.currentTarget)}>{l.name}</button
            >{#if canModerate(l)}<button
                type="button"
                class="live-gear"
                aria-label={`Moderate ${l.name}`}
                aria-haspopup="menu"
                aria-expanded={gearMenu?.line.seq === l.seq}
                onclick={(e) => toggleGear(l, e.currentTarget)}><span aria-hidden="true">⚙&#xFE0E;</span></button
              >{/if}{#each l.badges as b (b)}<span class="live-badge live-role" title={b}><span aria-hidden="true">{badgeLetter(b)}</span><span class="visually-hidden">{b}</span></span>{/each}{#if l.test}<span
                class="live-badge">Test</span
              >{/if}{#if l.shared}<span class="live-badge">Shared chat</span>{/if}{#if l.suspicious}<span
                class="live-badge live-suspicious">Suspicious: {l.suspicious}</span
              >{/if}{#if l.deleted}<span class="live-badge">Deleted</span>{/if}<span class="live-text">: {l.text}</span>
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
      <p class="saved" role="status">{modNotice}</p>
      {#if modProblem}<p class="error" role="alert">{modProblem}</p>{/if}
    </section>

    <div class="live-side">

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

    <section class="sg-panel live-modes" aria-labelledby="live-modes-heading">
      <h2 id="live-modes-heading">Chat modes</h2>
      {#if modes}
        <div class="live-mode-list">
          <button type="button" class="button" aria-pressed={modes.emote_mode} disabled={modesBusy || !youIn}
            onclick={() => changeModes({ emote_mode: !modes!.emote_mode })}>Emote-only</button
          >
          <button type="button" class="button" aria-pressed={modes.subscriber_mode} disabled={modesBusy || !youIn}
            onclick={() => changeModes({ subscriber_mode: !modes!.subscriber_mode })}>Subscribers-only</button
          >
          <button type="button" class="button" aria-pressed={modes.unique_chat_mode} disabled={modesBusy || !youIn}
            onclick={() => changeModes({ unique_chat_mode: !modes!.unique_chat_mode })}>Unique chat</button
          >
          <div class="live-mode-with">
            <button type="button" class="button" aria-pressed={modes.follower_mode} disabled={modesBusy || !youIn}
              onclick={() => changeModes(modes!.follower_mode ? { follower_mode: false } : { follower_mode: true, follower_mode_duration: followMinutes })}
              >Followers-only</button
            >
            <label>
              <span class="visually-hidden">Followed for at least</span>
              <select class="sg-input" bind:value={followMinutes} disabled={modesBusy || !youIn}
                onchange={() => modes!.follower_mode && changeModes({ follower_mode: true, follower_mode_duration: followMinutes })}>
                {#each FOLLOW_CHOICES as m (m)}<option value={m}>{m === 0 ? 'any follower' : `followed ${lengthWords(m * 60)}`}</option>{/each}
              </select>
            </label>
          </div>
          <div class="live-mode-with">
            <button type="button" class="button" aria-pressed={modes.slow_mode} disabled={modesBusy || !youIn}
              onclick={() => changeModes(modes!.slow_mode ? { slow_mode: false } : { slow_mode: true, slow_mode_wait_time: slowSeconds })}
              >Slow</button
            >
            <label>
              <span class="visually-hidden">Seconds between messages</span>
              <select class="sg-input" bind:value={slowSeconds} disabled={modesBusy || !youIn}
                onchange={() => modes!.slow_mode && changeModes({ slow_mode: true, slow_mode_wait_time: slowSeconds })}>
                {#each SLOW_CHOICES as n (n)}<option value={n}>{n} seconds</option>{/each}
              </select>
            </label>
          </div>
        </div>
        <p class="saved" role="status">{modesNotice}</p>
      {/if}
      {#if modesProblem}
        <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> {modes ? modesProblem : `Unknown: ${modesProblem}`}</p>
      {/if}
      <details class="live-log-details" ontoggle={(e) => {
        logOpen = (e.currentTarget as HTMLDetailsElement).open;
        if (logOpen) loadLog();
      }}>
        <summary>Done from StreamGremlin</summary>
        {#if logProblem}
          <p class="state state-unknown"><span aria-hidden="true">?</span> Unknown: {logProblem}</p>
        {:else if log && log.length === 0}
          <p class="sg-muted">Nothing yet.</p>
        {:else if log}
          <ul class="live-modlog">
            {#each log as r (r.id)}
              <li><span class="sg-muted">{eventTime(r.created_at)}</span> {r.login ? `${r.login}: ` : ''}{historyWords(r)}</li>
            {/each}
          </ul>
        {/if}
      </details>
    </section>
    </div>
  </div>
{/if}

<svelte:document onpointerdown={gearOutside} />
{#if gearMenu}
  {@const l = gearMenu.line}
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div class="live-gear-menu" role="menu" aria-label={`Moderate ${l.name}`} style={gearStyle(gearMenu.anchor)} bind:this={menuEl} onkeydown={gearKey} tabindex="-1">
    {#if l.messageId && !l.deleted}
      <button type="button" role="menuitem" tabindex="-1" onclick={() => gearPick((l) => askThen(l, 'delete', { message_id: l.messageId }))}
        >Delete message…</button
      >
    {/if}
    <button type="button" role="menuitem" tabindex="-1" onclick={() => gearPick((l) => askThen(l, 'timeout', { seconds: 600 }))}
      >Time out 10 minutes…</button
    >
    <button type="button" role="menuitem" tabindex="-1" class="live-gear-danger" onclick={() => gearPick((l) => askThen(l, 'ban'))}>Ban…</button>
    <button type="button" role="menuitem" tabindex="-1" onclick={() => gearPick(openCard)}
      >More in their card…</button
    >
  </div>
{/if}

{#if asking}
  {@const q = asking}
  <ConfirmDialog
    title={q.words.title}
    message={q.action === 'ban' ? `${q.words.message} To give a reason, use their card (click their name).` : q.words.message}
    confirmLabel={q.words.confirmLabel}
    onconfirm={() => {
      const { line, action, extra } = q; // read before closing clears it
      asking = undefined;
      moderateLine(line, action, extra);
    }}
    oncancel={() => (asking = undefined)}
  />
{/if}

{#if card}
  {#key card.line.seq}
    <ViewerCard
      line={card.line}
      anchor={card.anchor}
      pinned={card.pinned}
      onclose={closeCard}
      onpointerenter={() => clearTimeout(leaveTimer)}
      onpointerleave={leaveName}
      canModerate={canModerate(card.line)}
      onpin={() => card && !card.pinned && (card = { ...card, pinned: true })}
      onmoderated={moderated}
    />
  {/key}
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
  .live-side {
    grid-area: buttons;
    display: grid;
    gap: 1rem;
    align-content: start;
  }
  .live-modes h2 {
    margin-bottom: 0.5rem;
  }
  .live-mode-list {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
  }
  .live-mode-with {
    display: flex;
    gap: 0.3rem;
    align-items: center;
  }
  .live-modes .button[aria-pressed='true'] {
    border-color: var(--sg-primary);
    box-shadow: inset 0 0 0 1px var(--sg-primary);
  }
  .live-log-details {
    margin-top: 0.75rem;
  }
  .live-modlog {
    list-style: none;
    margin: 0.4rem 0 0;
    padding: 0;
    font-size: 0.9rem;
  }
  .live-line {
    position: relative;
  }
  .live-gear {
    margin-left: 0.15rem;
    padding: 0 0.25rem;
    border: 1px solid transparent;
    border-radius: 4px;
    background: none;
    color: var(--sg-muted);
    font: inherit;
    line-height: 1.2;
    cursor: pointer;
    opacity: 0.55;
  }
  .live-line:hover .live-gear,
  .live-gear:focus-visible,
  .live-gear[aria-expanded='true'] {
    opacity: 1;
    color: var(--sg-foreground);
  }
  .live-gear:hover,
  .live-gear[aria-expanded='true'] {
    border-color: var(--sg-border);
  }
  .live-gear-menu {
    position: fixed;
    z-index: 30;
    min-width: 12rem;
    display: grid;
    padding: 0.25rem;
    background: var(--sg-surface-raised);
    border: 1px solid var(--sg-border);
    border-radius: 6px;
    box-shadow: 0 6px 20px rgb(0 0 0 / 0.25);
  }
  .live-gear-menu button {
    text-align: left;
    padding: 0.35rem 0.6rem;
    border: 0;
    border-radius: 4px;
    background: none;
    color: var(--sg-foreground);
    font: inherit;
    cursor: pointer;
  }
  .live-gear-menu button:hover,
  .live-gear-menu button:focus-visible {
    background: var(--sg-surface-hover);
  }
  .live-gear-menu .live-gear-danger {
    color: var(--sg-danger);
  }
  .live-deleted .live-text {
    text-decoration: line-through;
    color: var(--sg-muted);
  }
  .live-flagged {
    border-left: 3px solid var(--sg-warning, var(--sg-primary));
    padding-left: 0.4rem;
  }
  .live-suspicious {
    color: var(--sg-foreground);
    font-weight: 650;
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
  .live-name {
    font: inherit;
    font-weight: 700;
    color: var(--sg-foreground);
    background: none;
    border: none;
    padding: 0;
    cursor: pointer;
    text-decoration: underline dotted;
    text-underline-offset: 3px;
  }
  .live-name:hover,
  .live-name[aria-expanded='true'] {
    text-decoration-style: solid;
  }
  .live-badge {
    font-size: 0.75rem;
    border: 1px solid var(--sg-border);
    border-radius: 4px;
    padding: 0 0.3rem;
    margin-left: 0.3rem;
    color: var(--sg-muted);
  }
  .live-role {
    display: inline-block;
    min-width: 1.1rem;
    text-align: center;
    font-weight: 650;
    padding: 0 0.15rem;
    cursor: default;
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
