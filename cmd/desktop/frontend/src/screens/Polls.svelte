<script lang="ts">
  // Polls and predictions (docs/adr/0023-polls-and-predictions.md): the
  // newest of each from Twitch, live, and forms to start them. Twitch runs
  // one of each at a time, on Affiliate and Partner channels.
  import { onMount } from 'svelte';
  import { ApiError, endPoll, endPrediction, getPolls, startPoll, startPrediction, type PollsView } from '../lib/api';
  import ConfirmDialog from '../lib/ConfirmDialog.svelte';

  let { refreshMs = 3000 }: { refreshMs?: number } = $props();

  let view = $state<PollsView | undefined>();
  let loadError = $state('');
  let notice = $state('');
  let error = $state('');
  let busy = $state(false);

  let pollForm = $state({ title: '', choices: ['', ''], seconds: 60, points: 0 });
  let predForm = $state({ title: '', outcomes: ['', ''], seconds: 120 });
  let formError = $state('');
  let confirm = $state<{ how: 'resolve' | 'cancel'; winner?: string } | undefined>();
  let winner = $state('');

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));
  const ok = $derived(view?.state === 'ok');
  const poll = $derived(view?.poll ?? null);
  const pred = $derived(view?.prediction ?? null);
  const pollRunning = $derived(poll?.status === 'active');
  const predOpen = $derived(pred?.status === 'active' || pred?.status === 'locked');

  async function load() {
    try {
      view = await getPolls();
      loadError = '';
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

  const filled = (list: string[]) => list.map((x) => x.trim()).filter(Boolean);
  const totalVotes = $derived(poll ? poll.choices.reduce((n, c) => n + c.votes, 0) : 0);
  const share = (votes: number) => (totalVotes ? Math.round((votes / totalVotes) * 100) : 0);
  const statusWords: Record<string, string> = {
    active: 'Running', completed: 'Ended', terminated: 'Ended early', archived: 'Removed', moderated: 'Removed by Twitch',
    invalid: 'Unknown', locked: 'Locked: no more bets', resolved: 'Resolved', canceled: 'Canceled and refunded',
  };

  async function submitPoll(e: SubmitEvent) {
    e.preventDefault();
    formError = '';
    const choices = filled(pollForm.choices);
    if (!pollForm.title.trim() || pollForm.title.length > 60) return void (formError = 'A poll’s question is 1 to 60 characters.');
    if (choices.length < 2) return void (formError = 'A poll needs at least 2 choices.');
    if (choices.some((c) => c.length > 25)) return void (formError = 'Each choice is up to 25 characters.');
    const ok = await act(
      () => startPoll({ title: pollForm.title.trim(), choices, seconds: Number(pollForm.seconds), channel_points_per_vote: Number(pollForm.points) || 0 }),
      'The poll started in your chat.',
    );
    if (ok) pollForm = { title: '', choices: ['', ''], seconds: 60, points: 0 };
  }

  async function submitPrediction(e: SubmitEvent) {
    e.preventDefault();
    formError = '';
    const outcomes = filled(predForm.outcomes);
    if (!predForm.title.trim() || predForm.title.length > 45) return void (formError = 'A prediction’s question is 1 to 45 characters.');
    if (outcomes.length < 2) return void (formError = 'A prediction needs at least 2 outcomes.');
    if (outcomes.some((o) => o.length > 25)) return void (formError = 'Each outcome is up to 25 characters.');
    const ok = await act(
      () => startPrediction({ title: predForm.title.trim(), outcomes, seconds: Number(predForm.seconds) }),
      'The prediction opened for bets.',
    );
    if (ok) predForm = { title: '', outcomes: ['', ''], seconds: 120 };
  }

  function finish(how: 'resolve' | 'cancel', w = '') {
    confirm = undefined;
    act(() => endPrediction(how, w), how === 'resolve' ? `Resolved: “${w}” won, and the points were paid out.` : 'Canceled: everyone got their points back.');
  }
</script>

<h1>Polls and predictions</h1>
<p class="sg-muted screen-intro">
  Run Twitch polls and channel point predictions from here or from workflows (“Start a poll”, “Start a prediction”). Twitch runs
  one of each at a time, on Affiliate and Partner channels.
</p>

<section class="sg-panel card" aria-labelledby="poll-heading" aria-busy={busy}>
  <h2 id="poll-heading">Poll</h2>
  {#if loadError}
    <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {loadError}</p>
  {:else if view?.state === 'not_signed_in'}
    <p class="state state-unknown"><span aria-hidden="true">?</span> Sign in with your Twitch account on the Connections screen.</p>
  {:else if view?.state === 'needs_permission'}
    <p class="state state-unknown" role="alert"><span aria-hidden="true">!</span> Sign in to Twitch again on the Connections screen and allow polls and predictions.</p>
  {:else if view?.state === 'no_channel_points'}
    <p class="state state-unknown" role="status">
      <span aria-hidden="true">!</span> Twitch gives polls and predictions to Affiliates and Partners, and says your channel isn’t one yet.
      Workflows for them are ready for then.
    </p>
  {:else if view?.state === 'unknown'}
    <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {view.detail}</p>
  {:else if ok && !poll}
    <p class="sg-muted">No polls yet.</p>
  {/if}
  {#if ok && poll}
    <p><strong>{poll.title}</strong> <span class="tag">{statusWords[poll.status] ?? poll.status}</span></p>
    <ul class="poll-results" aria-label="Votes">
      {#each poll.choices as c (c.id)}
        <li>
          <span class="poll-choice">{c.title}</span>
          <span class="poll-bar" aria-hidden="true"><span class="poll-fill" style:width={`${share(c.votes)}%`}></span></span>
          <span class="sg-muted">{c.votes} {c.votes === 1 ? 'vote' : 'votes'} ({share(c.votes)}%)</span>
        </li>
      {/each}
    </ul>
    {#if pollRunning}
      <div class="row-actions">
        <button type="button" class="button" disabled={busy} onclick={() => act(() => endPoll(false), 'The poll ended; its results show in chat for a while.')}
          >End and show results</button
        >
        <button type="button" class="button" disabled={busy} onclick={() => act(() => endPoll(true), 'The poll ended and was removed from chat.')}
          >End and remove</button
        >
      </div>
    {/if}
  {/if}
  {#if ok && !pollRunning}
    <form class="test-form" onsubmit={submitPoll} aria-label="Start a poll">
      <h3>Start a poll</h3>
      <label class="field"><span>Question</span><input class="sg-input" bind:value={pollForm.title} maxlength="60" placeholder="What should I play next?" /></label>
      {#each pollForm.choices as _, i (i)}
        <label class="field"><span>Choice {i + 1}</span><input class="sg-input" bind:value={pollForm.choices[i]} maxlength="25" /></label>
      {/each}
      {#if pollForm.choices.length < 5}
        <button type="button" class="button add-version" onclick={() => (pollForm.choices = [...pollForm.choices, ''])}>+ Another choice</button>
      {/if}
      <label class="field"><span>How long it runs (seconds)</span><input class="sg-input" type="number" min="15" max="1800" bind:value={pollForm.seconds} /></label>
      <label class="field">
        <span>Channel points for each extra vote</span>
        <input class="sg-input" type="number" min="0" max="1000000" bind:value={pollForm.points} />
        <span class="sg-muted field-hint">0: one free vote each.</span>
      </label>
      <div class="row-actions"><button type="submit" class="sg-button" disabled={busy}>Start the poll</button></div>
    </form>
  {/if}
</section>

<section class="sg-panel card" aria-labelledby="pred-heading" aria-busy={busy}>
  <h2 id="pred-heading">Prediction</h2>
  {#if ok && !pred}
    <p class="sg-muted">No predictions yet.</p>
  {/if}
  {#if ok && pred}
    <p><strong>{pred.title}</strong> <span class="tag">{statusWords[pred.status] ?? pred.status}</span></p>
    <ul class="poll-results" aria-label="Bets">
      {#each pred.outcomes as o, i (o.id)}
        <li>
          <span class="poll-choice">{i + 1}. {o.title}{o.id === pred.winning_outcome_id ? ' ✓ won' : ''}</span>
          <span class="sg-muted">{o.users} {o.users === 1 ? 'viewer' : 'viewers'}, {o.channel_points.toLocaleString()} points</span>
        </li>
      {/each}
    </ul>
    {#if predOpen}
      <div class="row-actions">
        {#if pred.status === 'active'}
          <button type="button" class="button" disabled={busy} onclick={() => act(() => endPrediction('lock'), 'Locked: no more bets.')}>Lock bets</button>
        {/if}
        <label class="field inline-field">
          <span>Winner</span>
          <select class="sg-input" bind:value={winner}>
            <option value="">Choose the outcome…</option>
            {#each pred.outcomes as o (o.id)}<option value={o.title}>{o.title}</option>{/each}
          </select>
        </label>
        <button type="button" class="button" disabled={busy || !winner} onclick={() => (confirm = { how: 'resolve', winner })}>Resolve</button>
        <button type="button" class="button button-danger" disabled={busy} onclick={() => (confirm = { how: 'cancel' })}>Cancel and refund</button>
      </div>
    {/if}
  {/if}
  {#if ok && !predOpen}
    <form class="test-form" onsubmit={submitPrediction} aria-label="Start a prediction">
      <h3>Start a prediction</h3>
      <label class="field"><span>Question</span><input class="sg-input" bind:value={predForm.title} maxlength="45" placeholder="Will I beat this boss?" /></label>
      {#each predForm.outcomes as _, i (i)}
        <label class="field"><span>Outcome {i + 1}</span><input class="sg-input" bind:value={predForm.outcomes[i]} maxlength="25" /></label>
      {/each}
      {#if predForm.outcomes.length < 10}
        <button type="button" class="button add-version" onclick={() => (predForm.outcomes = [...predForm.outcomes, ''])}>+ Another outcome</button>
      {/if}
      <label class="field"><span>How long it takes bets (seconds)</span><input class="sg-input" type="number" min="30" max="1800" bind:value={predForm.seconds} /></label>
      <div class="row-actions"><button type="submit" class="sg-button" disabled={busy}>Open the prediction</button></div>
    </form>
  {/if}
  {#if formError}<p class="error" role="alert">{formError}</p>{/if}
  <p class="saved" role="status">{notice}</p>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
</section>

{#if confirm}
  {@const c = confirm}
  <ConfirmDialog
    title={c.how === 'resolve' ? `Resolve: “${c.winner}” won?` : 'Cancel the prediction?'}
    message={c.how === 'resolve'
      ? 'The channel points are paid out to the viewers who chose it. This can’t be undone.'
      : 'Everyone gets their channel points back. This can’t be undone.'}
    confirmLabel={c.how === 'resolve' ? 'Resolve' : 'Cancel and refund'}
    onconfirm={() => finish(c.how, c.winner)}
    oncancel={() => (confirm = undefined)}
  />
{/if}
