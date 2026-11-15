<script lang="ts">
  // Ban lists (M8 slice 2, D-106): the streamer's own lists of accounts to
  // ban on Twitch, from a file. Each is checked first and shown before
  // anything is banned; then banned at Twitch's pace in the background,
  // picking up where it left off after a restart. A newer file bans only
  // the new names; undo lifts only what the list banned.
  import { onMount } from 'svelte';
  import {
    ApiError,
    banListAction,
    banListFile,
    chooseFile,
    confirmBanList,
    copyTwitchBans,
    deleteBanList,
    getBanListEntries,
    getBanLists,
    getFollowBots,
    actOnNames,
    type FollowBotOptions,
    type FollowBotReview,
    importBanList,
    saveFile,
    setBanList,
    type BanList,
    type BanListEntry,
  } from '../lib/api';
  import ConfirmDialog from '../lib/ConfirmDialog.svelte';

  let { refreshMs = 2000 }: { refreshMs?: number } = $props();

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));
  const n = (x: number | undefined) => (x ?? 0).toLocaleString();

  let lists = $state<BanList[]>([]);
  let loadError = $state('');
  let busy = $state(false);
  let notice = $state('');
  let error = $state('');

  // Importing.
  let form = $state({ name: '', path: '', reason: '' });
  let copyName = $state('');
  // The ready list's choices, and a sample of its names.
  let unbanRemoved = $state<Record<string, boolean>>({});
  let rebanByHand = $state<Record<string, boolean>>({});
  let samples = $state<Record<string, BanListEntry[]>>({});
  let confirmUndo = $state<BanList | undefined>();

  // The follow-bot review (D-109).
  let fbOptions = $state<FollowBotOptions>({ hours: 24, days: 7, burst: 5, minutes: 10 });
  let review = $state<FollowBotReview | undefined>();
  let picked = $state<Record<string, boolean>>({});
  let fbBusy = $state(false);
  let fbError = $state('');
  let fbNotice = $state('');
  let fbConfirm = $state<'block' | 'ban' | undefined>();
  const pickedNames = $derived(review ? review.suspects.filter((s) => picked[s.user_id]).map((s) => s.login) : []);

  async function findBots(e: SubmitEvent) {
    e.preventDefault();
    fbBusy = true;
    fbError = '';
    fbNotice = '';
    try {
      review = await getFollowBots({ hours: Number(fbOptions.hours), days: Number(fbOptions.days), burst: Number(fbOptions.burst), minutes: Number(fbOptions.minutes) });
      picked = Object.fromEntries(review.suspects.map((s) => [s.user_id, s.in_burst]));
    } catch (err) {
      fbError = message(err);
    } finally {
      fbBusy = false;
    }
  }

  async function actOnBots(how: 'block' | 'ban') {
    const names = pickedNames;
    fbConfirm = undefined;
    const name = `Follow bots ${new Date().toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}`;
    try {
      await actOnNames(name, names, how);
      fbNotice = `${how === 'block' ? 'Blocking' : 'Banning'} ${names.length} as the list “${name}” below; Undo there lifts them.`;
      review = undefined;
      await load();
    } catch (err) {
      fbError = message(err);
    }
  }
  let confirmForget = $state<BanList | undefined>();

  async function load() {
    try {
      lists = (await getBanLists()).lists;
      loadError = '';
      for (const l of lists) {
        if (l.phase === 'ready' && !samples[l.id]) {
          getBanListEntries(l.id, ['waiting'], 10)
            .then((r) => (samples = { ...samples, [l.id]: r.entries }))
            .catch(() => undefined);
        }
      }
    } catch (e) {
      loadError = message(e);
    }
  }

  onMount(() => {
    load();
    const id = setInterval(load, refreshMs);
    return () => clearInterval(id);
  });

  async function act(f: () => Promise<unknown>, done: string) {
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

  async function choose() {
    try {
      const p = await chooseFile('ban_list');
      if (p) {
        form.path = p;
        if (!form.name.trim()) form.name = (p.split(/[\\/]/).pop() ?? '').replace(/\.(txt|csv)$/i, '');
      }
    } catch (e) {
      error = message(e);
    }
  }

  async function submitImport(e: SubmitEvent) {
    e.preventDefault();
    error = '';
    if (!form.name.trim()) return void (error = 'Name the list.');
    if (!form.path) return void (error = 'Choose the file first.');
    const again = lists.some((l) => l.name.toLowerCase() === form.name.trim().toLowerCase());
    const ok = await act(
      () => importBanList(form.name.trim(), form.path, form.reason.trim() || undefined),
      again ? 'Reading the newer file: only new names will be checked.' : 'Reading the file. Nothing is banned until you confirm.',
    );
    if (ok) form = { name: '', path: '', reason: '' };
  }

  async function submitCopy(e: SubmitEvent) {
    e.preventDefault();
    if (!copyName.trim()) return void (error = 'Name the copy.');
    if (await act(() => copyTwitchBans(copyName.trim()), 'Copying Twitch’s ban list.')) copyName = '';
  }

  async function save(l: BanList, states: string[], what: string) {
    busy = true;
    error = '';
    notice = '';
    try {
      const text = await banListFile(l.id, states);
      const where = await saveFile(`${l.name}${states.length ? `-${what}` : ''}.txt`, text);
      if (where) notice = `Saved to ${where}.`;
    } catch (e) {
      error = message(e);
    } finally {
      busy = false;
    }
  }

  const kept = (l: BanList) => l.counts.kept ?? 0;
  const total = (l: BanList) => Object.values(l.counts).reduce((a, b) => a + b, 0);
  const banningDone = (l: BanList) => (l.counts.banned ?? 0) + (l.counts.refused ?? 0) + (l.counts.unbanned ?? 0);
  const hours = (finish?: string) => {
    if (!finish) return '';
    const mins = Math.max(1, Math.round((new Date(finish).getTime() - Date.now()) / 60000));
    return mins < 90 ? `about ${mins} minutes` : `about ${Math.round(mins / 60)} hours`;
  };
  const busyPhase = (l: BanList) => ['reading', 'checking', 'banning', 'undoing', 'copying', 'comparing'].includes(l.phase);
</script>

<h1>Ban lists</h1>
<p class="sg-muted screen-intro">
  Your own lists of accounts to ban on Twitch, from a file: one Twitch name or user id a line, or a CSV with a login or id column.
  Each list is checked first and shown here before anything is banned. Then it bans one account at a time at Twitch’s pace,
  keeping room for chat and moderating, and carries on where it left off after a restart. You, your mods and VIPs, and
  viewers with points are left alone. A newer file bans only the new names; undo lifts only what the list banned.
</p>

<section class="sg-panel card" aria-labelledby="bl-import-heading" aria-busy={busy}>
  <h2 id="bl-import-heading">Import a list</h2>
  <form class="test-form" onsubmit={submitImport} aria-label="Import a ban list">
    <label class="field"><span>Name</span><input class="sg-input" bind:value={form.name} maxlength="60" placeholder="Hate raid bots" /></label>
    <div class="field">
      <span>File</span>
      <div class="row-actions">
        <button type="button" class="button" onclick={choose}>Choose file…</button>
        <span class="sg-muted">{form.path || 'No file chosen.'}</span>
      </div>
      <span class="sg-muted field-hint">Up to 5 million lines. Use the name of a list you have to import a newer version of it.</span>
    </div>
    <label class="field">
      <span>Reason Twitch shows with each ban</span>
      <input class="sg-input" bind:value={form.reason} maxlength="500" placeholder="Ban list: (its name)" />
    </label>
    <div class="row-actions"><button type="submit" class="sg-button" disabled={busy}>Import</button></div>
  </form>
  <form class="test-form" onsubmit={submitCopy} aria-label="Copy Twitch's ban list">
    <h3>Copy Twitch’s ban list</h3>
    <p class="sg-muted">Makes a list of everyone banned on your channel now, to save as a file or keep. It never unbans them.</p>
    <label class="field"><span>Name of the copy</span><input class="sg-input" bind:value={copyName} maxlength="60" placeholder="My bans" /></label>
    <div class="row-actions"><button type="submit" class="button" disabled={busy}>Copy</button></div>
  </form>
  <p class="saved" role="status">{notice}</p>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
</section>

<section class="sg-panel card" aria-labelledby="fb-heading" aria-busy={fbBusy}>
  <h2 id="fb-heading">Follow-bot review</h2>
  <p class="sg-muted">
    Finds Twitch follows from accounts made only days before they followed that never chatted, marking those that came in a
    burst. You pick which to block or ban. A block stops an account following, whispering or chatting with you; Twitch doesn’t
    say whether it removes a follow already made, so check your follower list after.
  </p>
  <form class="test-form" aria-label="Find follow bots" onsubmit={findBots}>
    <label class="field inline-field"><span>Follows from the last (hours)</span><input class="sg-input" type="number" min="1" max="720" bind:value={fbOptions.hours} /></label>
    <label class="field inline-field"><span>Accounts younger than (days)</span><input class="sg-input" type="number" min="1" max="365" bind:value={fbOptions.days} /></label>
    <label class="field inline-field"><span>A burst is (follows)</span><input class="sg-input" type="number" min="2" max="100" bind:value={fbOptions.burst} /></label>
    <label class="field inline-field"><span>Within (minutes)</span><input class="sg-input" type="number" min="1" max="120" bind:value={fbOptions.minutes} /></label>
    <div class="row-actions"><button type="submit" class="button" disabled={fbBusy}>Find follow bots</button></div>
  </form>
  {#if review}
    <p>
      {review.suspects.length} of {n(review.follows)} follows look like bots{review.unknown ? ` (${review.unknown} couldn’t be checked)` : ''}.
      Those in a burst are ticked.
    </p>
    {#if review.suspects.length}
      <ul class="bl-suspects" aria-label="Suspected follow bots">
        {#each review.suspects as s (s.user_id)}
          <li>
            <label class="field-check">
              <input type="checkbox" bind:checked={picked[s.user_id]} />
              {s.display_name} <span class="sg-muted">({s.age_days === 0 ? 'made the same day' : `${s.age_days} ${s.age_days === 1 ? 'day' : 'days'} old`} when they followed{s.in_burst ? ', in a burst' : ''})</span>
            </label>
          </li>
        {/each}
      </ul>
      <div class="row-actions">
        <button type="button" class="button button-danger" disabled={!pickedNames.length} onclick={() => (fbConfirm = 'block')}>Block {pickedNames.length}…</button>
        <button type="button" class="button" disabled={!pickedNames.length} onclick={() => (fbConfirm = 'ban')}>Ban {pickedNames.length}…</button>
      </div>
    {/if}
  {/if}
  <p class="saved" role="status">{fbNotice}</p>
  {#if fbError}<p class="error" role="alert">{fbError}</p>{/if}
</section>

{#if loadError}
  <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {loadError}</p>
{/if}

{#each lists as l (l.id)}
  <section class="sg-panel card" aria-labelledby={`bl-${l.id}`}>
    <h2 id={`bl-${l.id}`}>{l.name}{l.kind === 'block' ? ' (blocks)' : ''}</h2>
    <p class={l.problem ? 'state state-unknown' : ''} role="status">
      {#if l.problem}<span aria-hidden="true">!</span>{/if}
      {l.words}
      {#if l.progress.waiting_live && !l.paused}Waiting until you’re offline on Twitch (you can change that below).{/if}
      {#if l.progress.wait_until}Twitch says to wait a moment.{/if}
    </p>
    <p class="sg-muted">
      {n(total(l))} names{l.file_name ? ` from ${l.file_name}` : ''}{l.skipped ? `; ${n(l.skipped)} lines weren’t a Twitch name` : ''}.
    </p>

    {#if l.phase === 'ready'}
      <ul class="bl-counts">
        <li><strong>{n(l.counts.waiting)}</strong> to ban</li>
        <li>{n(l.counts.already)} already banned on Twitch</li>
        <li>{n(l.counts.not_found)} not found (no such account, or deleted or suspended)</li>
        <li>{n(kept(l))} left alone (you, your mods and VIPs{l.keep_points ? ', viewers with points' : ''})</li>
        {#if l.counts.banned}<li>{n(l.counts.banned)} banned by this list before: not asked about again</li>{/if}
      </ul>
      {#if samples[l.id]?.length}
        <p class="sg-muted">For example: {samples[l.id].map((e) => e.login || e.name).join(', ')}</p>
      {/if}
      {#if l.counts.removed}
        <label class="field-check">
          <input type="checkbox" bind:checked={unbanRemoved[l.id]} /> Also unban the {n(l.counts.removed)} this list banned that aren’t in the newer file
        </label>
      {/if}
      {#if l.counts.by_hand}
        <label class="field-check">
          <input type="checkbox" bind:checked={rebanByHand[l.id]} /> Ban again the {n(l.counts.by_hand)} someone unbanned on Twitch by hand
        </label>
      {/if}
      <div class="row-actions">
        <button type="button" class="sg-button" disabled={busy || !(l.counts.waiting || (unbanRemoved[l.id] && l.counts.removed) || (rebanByHand[l.id] && l.counts.by_hand))}
          onclick={() => act(() => confirmBanList(l.id, { unban_removed: !!unbanRemoved[l.id], reban_by_hand: !!rebanByHand[l.id] }), `Banning ${n(l.counts.waiting)} at Twitch’s pace.`)}
          >Start banning</button
        >
        <button type="button" class="button" disabled={busy} onclick={() => save(l, ['not_found', 'kept'], 'skipped')}>Save the skipped names…</button>
      </div>
    {/if}

    {#if l.phase === 'banning' || l.phase === 'undoing'}
      {@const done = banningDone(l)}
      {@const all = done + l.left}
      <p>
        {l.phase === 'undoing' ? (l.kind === 'block' ? 'Unblocked' : 'Unbanned') : 'Done'}: {n(done)} of {n(all)}.
        {#if l.progress.per_minute > 0}{Math.round(l.progress.per_minute)} a minute{l.progress.finish ? `, finished in ${hours(l.progress.finish)}` : ''}.{/if}
      </p>
      <progress max={all || 1} value={done} aria-label="Progress"></progress>
    {/if}

    {#if l.phase === 'done'}
      <ul class="bl-counts">
        <li><strong>{n(l.counts.banned)}</strong> {l.kind === 'block' ? 'blocked' : 'banned'} by this list</li>
        {#if l.counts.already}<li>{n(l.counts.already)} banned already, not by this list</li>{/if}
        {#if l.counts.refused}<li>{n(l.counts.refused)} Twitch refused</li>{/if}
        {#if l.counts.by_hand}<li>{n(l.counts.by_hand)} unbanned on Twitch by hand: left alone</li>{/if}
        {#if l.counts.removed}<li>{n(l.counts.removed)} not in the newer file, still banned</li>{/if}
        {#if l.counts.unbanned}<li>{n(l.counts.unbanned)} {l.kind === 'block' ? 'unblocked' : 'unbanned'} by this list</li>{/if}
      </ul>
    {/if}

    <div class="row-actions">
      {#if busyPhase(l)}
        {#if l.paused}
          <button type="button" class="button" disabled={busy} onclick={() => act(() => banListAction(l.id, 'resume'), 'Carrying on.')}>Resume</button>
        {:else}
          <button type="button" class="button" disabled={busy} onclick={() => act(() => banListAction(l.id, 'pause'), 'Paused.')}>Pause</button>
        {/if}
      {/if}
      {#if l.phase === 'done'}
        {#if l.kind !== 'block'}
          <button type="button" class="button" disabled={busy} onclick={() => act(() => banListAction(l.id, 'compare'), 'Checking its bans against Twitch.')}
            >Check against Twitch</button
          >
        {/if}
        <button type="button" class="button" disabled={busy} onclick={() => save(l, [], 'all')}>Save to a file…</button>
        {#if l.counts.banned || l.counts.removed}
          <button type="button" class="button button-danger" disabled={busy} onclick={() => (confirmUndo = l)}>Undo…</button>
        {/if}
      {/if}
      {#if !busyPhase(l) || l.paused}
        <button type="button" class="button" disabled={busy} onclick={() => (confirmForget = l)}>Forget…</button>
      {/if}
    </div>

    <details>
      <summary>Settings</summary>
      <label class="field-check">
        <input type="checkbox" checked={l.pause_live} onchange={(e) => act(() => setBanList(l.id, { pause_live: e.currentTarget.checked }), 'Saved.')} />
        Wait while I’m live on Twitch, so moderating is never slowed
      </label>
      <label class="field-check">
        <input type="checkbox" checked={l.keep_points} onchange={(e) => act(() => setBanList(l.id, { keep_points: e.currentTarget.checked }), 'Saved.')} />
        Leave viewers with points alone (for names checked from now on)
      </label>
      <p class="sg-muted">Reason sent with each ban: {l.reason}</p>
    </details>
  </section>
{/each}

{#if confirmUndo}
  {@const l = confirmUndo}
  <ConfirmDialog
    title={`Undo “${l.name}”?`}
    message={l.kind === 'block'
      ? `This unblocks the ${n(l.counts.banned ?? 0)} accounts this list blocked, one at a time at Twitch’s pace.`
      : `This unbans the ${n((l.counts.banned ?? 0) + (l.counts.removed ?? 0))} accounts this list banned, one at a time at Twitch’s pace, and bans nothing more. Bans made elsewhere are never lifted.`}
    confirmLabel="Undo the list"
    onconfirm={() => {
      const id = l.id;
      confirmUndo = undefined;
      act(() => banListAction(id, 'undo'), 'Unbanning what this list banned.');
    }}
    oncancel={() => (confirmUndo = undefined)}
  />
{/if}
{#if fbConfirm}
  {@const how = fbConfirm}
  <ConfirmDialog
    title={`${how === 'block' ? 'Block' : 'Ban'} ${pickedNames.length} accounts?`}
    message={how === 'block'
      ? 'They’re blocked as you, one at a time at Twitch’s pace, as a list you can undo below. Your mods, VIPs and viewers with points are left alone.'
      : 'They’re banned from your chat, one at a time at Twitch’s pace, as a list you can undo below. Your mods, VIPs and viewers with points are left alone.'}
    confirmLabel={how === 'block' ? 'Block them' : 'Ban them'}
    onconfirm={() => actOnBots(how)}
    oncancel={() => (fbConfirm = undefined)}
  />
{/if}
{#if confirmForget}
  {@const l = confirmForget}
  <ConfirmDialog
    title={`Forget “${l.name}”?`}
    message="StreamGremlin forgets the list and what it did. Nobody is unbanned: undo it first for that."
    confirmLabel="Forget it"
    onconfirm={() => {
      const id = l.id;
      confirmForget = undefined;
      act(() => deleteBanList(id), 'Forgotten.');
    }}
    oncancel={() => (confirmForget = undefined)}
  />
{/if}

<style>
  .bl-counts {
    margin: 0.5rem 0;
    padding-left: 1.25rem;
  }
  progress {
    width: 100%;
  }
  .bl-suspects {
    list-style: none;
    padding: 0;
    max-height: 18rem;
    overflow-y: auto;
  }
</style>
