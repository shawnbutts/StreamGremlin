<script lang="ts">
  // Viewers (docs/adr/0032-viewers-and-points.md): everyone recorded from
  // your real chat and events, never test events. Find one, see their
  // record and points history, give or take points, undo a change once,
  // export their record or everyone's, and forget them. Its own screen
  // because viewers are about more than points (D-060). Twitch and YouTube
  // viewers are listed alike; one person's two accounts can be linked to
  // share one balance (D-101).
  import { onMount } from 'svelte';
  import {
    ApiError,
    adjustPoints,
    exportAllViewers,
    exportViewer,
    forgetViewer,
    getPoints,
    getViewer,
    getViewers,
    linkViewer,
    saveFile,
    unlinkViewer,
    undoPoints,
    type PointsTransaction,
    type Viewer,
    type ViewerDetail,
  } from '../lib/api';
  import ConfirmDialog from '../lib/ConfirmDialog.svelte';
  import { eventTime } from '../lib/events';

  // openId: a viewer to open at once (from Points' top viewers, say).
  let { refreshMs = 5000, openId }: { refreshMs?: number; openId?: string } = $props();

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));
  const key = () => (crypto.randomUUID ? crypto.randomUUID() : `${Date.now()}-${Math.random()}`);

  let name = $state('points');
  let loadError = $state('');
  let query = $state('');
  let found = $state<Viewer[]>([]);
  let chosen = $state<ViewerDetail | undefined>();
  let busy = $state(false);
  let notice = $state('');
  let error = $state('');
  let amount = $state(10);
  let reason = $state('');
  // One request id per change, kept while that change is retried.
  let changeKey = key();
  let undoing = $state<PointsTransaction | undefined>();
  let forgetting = $state<ViewerDetail | undefined>();
  // Linking (D-101): find their account on the other platform, then ask.
  let linkQuery = $state('');
  let linkFound = $state<Viewer[]>([]);
  let linking = $state<{ a: ViewerDetail; b: Viewer } | undefined>();
  let unlinking = $state<ViewerDetail | undefined>();
  const where = (p: string) => (p === 'youtube' ? 'YouTube' : 'Twitch');

  async function findOther() {
    const v = chosen;
    if (!v || !linkQuery.trim()) {
      linkFound = [];
      return;
    }
    try {
      linkFound = (await getViewers(linkQuery.trim())).viewers.filter((x) => x.platform !== v.platform && !x.link).slice(0, 8);
    } catch (e) {
      error = message(e);
    }
  }

  async function link(a: ViewerDetail, b: Viewer) {
    linking = undefined;
    if (await act(() => linkViewer(a.id, b.id), `Linked ${a.display_name} and ${b.display_name}: they share one ${name} balance.`)) {
      linkQuery = '';
      linkFound = [];
    }
  }

  async function unlink(v: ViewerDetail) {
    unlinking = undefined;
    await act(() => unlinkViewer(v.id), `Unlinked. The shared ${name} stay on Twitch; on YouTube they start from 0.`);
  }

  const kindWords: Record<string, string> = { credit: 'earned', debit: 'spent', adjust: 'by hand', reverse: 'undo' };
  const signed = (n: number) => (n > 0 ? `+${n}` : String(n));

  async function load() {
    try {
      name = (await getPoints()).currency.name;
      found = (await getViewers(query.trim())).viewers;
      if (chosen) chosen = await getViewer(chosen.id);
      loadError = '';
    } catch (e) {
      loadError = message(e);
    }
  }

  onMount(() => {
    load();
    if (openId) open({ id: openId });
    const t = setInterval(load, refreshMs);
    return () => clearInterval(t);
  });

  async function act(f: () => Promise<unknown>, done: string): Promise<boolean> {
    busy = true;
    error = '';
    notice = '';
    try {
      await f();
      notice = done;
      return true;
    } catch (e) {
      error = message(e);
      return false;
    } finally {
      busy = false;
      await load();
    }
  }

  async function open(v: { id: string }) {
    error = '';
    try {
      chosen = await getViewer(v.id);
      changeKey = key();
    } catch (e) {
      error = message(e);
    }
  }

  async function change(sign: 1 | -1) {
    if (!chosen) return;
    const v = chosen;
    const n = Math.round(Number(amount));
    if (!(n > 0)) {
      error = 'Type how many, above 0.';
      return;
    }
    const k = changeKey;
    if (await act(() => adjustPoints(v.id, sign * n, reason.trim(), k), sign > 0 ? `Gave ${v.display_name} ${n} ${name}.` : `Took ${n} ${name} from ${v.display_name}.`)) {
      changeKey = key();
      reason = '';
    }
  }

  async function undo(t: PointsTransaction) {
    undoing = undefined;
    await act(() => undoPoints(t.id, key()), 'Undone.');
  }

  async function save(get: () => Promise<unknown>, file: string, done: (where: string) => string) {
    busy = true;
    error = '';
    notice = '';
    try {
      const where = await saveFile(file, JSON.stringify(await get(), null, 2));
      notice = where ? done(where) : 'Not saved.';
    } catch (e) {
      error = message(e);
    } finally {
      busy = false;
    }
  }

  const exportOne = (v: ViewerDetail) =>
    save(() => exportViewer(v.id), `${(v.login || v.id).replace(/[^A-Za-z0-9_-]/g, '_')}-streamgremlin.json`, (w) => `Saved ${v.display_name}'s record to ${w}.`);
  const exportAll = () => save(exportAllViewers, 'streamgremlin-viewers.json', (w) => `Saved every viewer's record to ${w}.`);

  async function forget(v: ViewerDetail) {
    forgetting = undefined;
    if (await act(() => forgetViewer(v.id), `${v.display_name} is forgotten. Their ${name} history stays, unnamed.`)) chosen = undefined;
  }
</script>

<h1>Viewers</h1>
<p class="sg-muted screen-intro">
  Everyone who has chatted, followed, subscribed or cheered, on Twitch or YouTube, recorded from your real chat and events, never from
  test events. Chat text isn’t kept. Export a viewer’s record, or everyone’s, and forget a viewer who asks. One person’s Twitch and
  YouTube accounts can be linked to share one {name} balance: by you here, or by them with <code>!link</code> (from Templates).
</p>

{#if loadError}
  <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {loadError}</p>
{/if}

<section class="sg-panel card" aria-labelledby="viewers-heading">
  <h2 id="viewers-heading">Find a viewer</h2>
  <label class="field">
    <span>Name</span>
    <input class="sg-input" type="search" bind:value={query} oninput={load} placeholder="Name" />
  </label>
  <div class="row-actions">
    <button type="button" class="button" disabled={busy} onclick={exportAll}>Export everyone</button>
  </div>
  {#if found.length === 0}
    <p class="sg-muted">{query.trim() ? 'No one by that name.' : 'No viewers yet: they appear as they chat, follow, subscribe or cheer.'}</p>
  {/if}
  <ul class="live-list" aria-label="Viewers">
    {#each found as v (v.id)}
      <li>
        <button type="button" class="link-button" onclick={() => open(v)}>{v.display_name}</button>
        <span class="sg-muted"
          >{where(v.platform)} · {v.balance} {name}{v.link ? ` (shared with ${v.link.with.display_name} on ${where(v.link.with.platform)})` : ''} · last
          seen {eventTime(v.last_seen)}</span
        >
      </li>
    {/each}
  </ul>
</section>

{#if chosen}
  {@const v = chosen}
  <section class="sg-panel card" aria-labelledby="viewer-heading" aria-busy={busy}>
    <h2 id="viewer-heading">{v.display_name} <span class="sg-muted">{v.login ? `@${v.login} ` : ''}on {where(v.platform)}</span></h2>
    <p class="sg-muted">
      First seen {eventTime(v.first_seen)}, last seen {eventTime(v.last_seen)}; {v.messages} chat {v.messages === 1 ? 'message' : 'messages'}.
    </p>
    <h3>{name[0].toUpperCase() + name.slice(1)}</h3>
    <p><strong>{v.balance} {name}</strong>{v.link ? `, shared with ${v.link.with.display_name} on ${where(v.link.with.platform)}` : ''}</p>
    <form class="row-actions" onsubmit={(e) => e.preventDefault()} aria-label={`Give or take ${name}`}>
      <label class="field inline-field"><span>How many</span><input class="sg-input" type="number" min="1" bind:value={amount} /></label>
      <label class="field inline-field"><span>Reason</span><input class="sg-input" bind:value={reason} maxlength="200" placeholder="optional" /></label>
      <button type="button" class="sg-button" disabled={busy} onclick={() => change(1)}>Give</button>
      <button type="button" class="button" disabled={busy} onclick={() => change(-1)}>Take</button>
    </form>
    <h3>History</h3>
    {#if v.history.length === 0}<p class="sg-muted">No changes yet.</p>{/if}
    <table class="history">
      <caption class="visually-hidden">{v.display_name}'s {name} history</caption>
      <thead><tr><th scope="col">When</th><th scope="col">Change</th><th scope="col">What</th><th scope="col">By</th><th scope="col"><span class="visually-hidden">Undo</span></th></tr></thead>
      <tbody>
        {#each v.history as t (t.id)}
          <tr>
            <td>{eventTime(t.created_at)}</td>
            <td>{signed(t.amount)}</td>
            <td>{kindWords[t.kind] ?? t.kind}{t.memo ? `: ${t.memo}` : ''}{t.reversed_by ? ' (undone)' : ''}</td>
            <td>{t.by}</td>
            <td>
              {#if t.kind !== 'reverse' && !t.reversed_by}
                <button type="button" class="button" disabled={busy} aria-label={`Undo ${signed(t.amount)} ${t.memo ?? ''}`} onclick={() => (undoing = t)}
                  >Undo</button
                >
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
    <h3>Linked account</h3>
    {#if v.link}
      {@const l = v.link}
      <p>
        Linked to <button type="button" class="link-button" onclick={() => open(l.with)}>{l.with.display_name}</button> on {where(l.with.platform)}
        {l.linked_by === 'chat' ? 'by !link in chat' : `by ${l.linked_by}`} at {eventTime(l.linked_at)}. They share one {name} balance.
      </p>
      <div class="row-actions">
        <button type="button" class="button" disabled={busy} onclick={() => (unlinking = v)}>Unlink…</button>
      </div>
    {:else if v.forgotten_at}
      <p class="sg-muted">A forgotten viewer can’t be linked.</p>
    {:else}
      <p class="sg-muted">
        Not linked. If they’re also on {where(v.platform === 'youtube' ? 'twitch' : 'youtube')}, find that account to share one {name} balance,
        or they can type <code>!link</code> in chat.
      </p>
      <label class="field">
        <span>Their name on {where(v.platform === 'youtube' ? 'twitch' : 'youtube')}</span>
        <input class="sg-input" type="search" bind:value={linkQuery} oninput={findOther} placeholder="Name" />
      </label>
      {#if linkQuery.trim() && linkFound.length === 0}<p class="sg-muted">No one unlinked by that name on {where(v.platform === 'youtube' ? 'twitch' : 'youtube')}.</p>{/if}
      <ul class="live-list" aria-label="Accounts to link">
        {#each linkFound as o (o.id)}
          <li>
            {o.display_name} <span class="sg-muted">{o.balance} {name}</span>
            <button type="button" class="button" disabled={busy} onclick={() => (linking = { a: v, b: o })}>Link…</button>
          </li>
        {/each}
      </ul>
    {/if}
    <div class="row-actions">
      <button type="button" class="button" disabled={busy} onclick={() => exportOne(v)}>Export their record</button>
      <button type="button" class="button button-danger" disabled={busy} onclick={() => (forgetting = v)}>Forget them</button>
    </div>
  </section>
{/if}

<p class="saved" role="status">{notice}</p>
{#if error}<p class="error" role="alert">{error}</p>{/if}

{#if undoing}
  {@const t = undoing}
  <ConfirmDialog
    title={`Undo ${signed(t.amount)} ${name}?`}
    message={`${t.amount > 0 ? `${-t.amount} ${name} are taken back` : `${-t.amount} ${name} are given back`}. It can be undone only once.`}
    confirmLabel="Undo"
    onconfirm={() => undo(t)}
    oncancel={() => (undoing = undefined)}
  />
{/if}
{#if linking}
  {@const q = linking}
  {@const yt = q.a.platform === 'youtube' ? q.a : q.b}
  {@const tw = q.a.platform === 'youtube' ? q.b : q.a}
  <ConfirmDialog
    title={`Link ${q.a.display_name} and ${q.b.display_name}?`}
    message={`Only link two accounts you know are one person. ${yt.display_name}’s ${yt.balance} ${name} join ${tw.display_name}’s ${tw.balance}: they share one balance from now on. Unlinking later leaves it all on Twitch.`}
    confirmLabel="Link"
    onconfirm={() => link(q.a, q.b)}
    oncancel={() => (linking = undefined)}
  />
{/if}
{#if unlinking}
  {@const v = unlinking}
  <ConfirmDialog
    title={`Unlink ${v.display_name} and ${v.link?.with.display_name}?`}
    message={`The shared ${v.balance} ${name} stay with the Twitch account; the YouTube account starts again from 0. You or they can link them again later.`}
    confirmLabel="Unlink"
    onconfirm={() => unlink(v)}
    oncancel={() => (unlinking = undefined)}
  />
{/if}
{#if forgetting}
  {@const v = forgetting}
  <ConfirmDialog
    title={`Forget ${v.display_name}?`}
    message={`Their account, names and activity are blanked. Their ${name} history stays, unnamed, so the ledger still adds up. If they chat again, they start afresh.${v.link ? ' They’re unlinked first: the shared points stay on Twitch.' : ''}`}
    confirmLabel="Forget"
    onconfirm={() => forget(v)}
    oncancel={() => (forgetting = undefined)}
  />
{/if}

<style>
  .history { border-collapse: collapse; width: 100%; }
  .history th, .history td { text-align: left; padding: 0.25rem 0.75rem 0.25rem 0; vertical-align: top; }
</style>
