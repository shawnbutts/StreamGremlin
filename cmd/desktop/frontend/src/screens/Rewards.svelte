<script lang="ts">
  // Rewards: your channel point rewards, read from Twitch
  // (docs/adr/0018-channel-point-rewards.md). StreamGremlin can change only
  // the rewards it made; rewards made on Twitch still start workflows.
  import { onMount } from 'svelte';
  import {
    ApiError,
    addReward,
    changeReward,
    deleteReward,
    getRedemptions,
    getRewards,
    setRedemption,
    type Redemption,
    type Reward,
    type RewardChange,
    type RewardList,
  } from '../lib/api';
  import ConfirmDialog from '../lib/ConfirmDialog.svelte';
  import { eventTime } from '../lib/events';

  let { refreshMs = 15000 }: { refreshMs?: number } = $props();

  interface Form {
    id?: string; // editing this one; none makes a new one
    title: string;
    cost: number;
    prompt: string;
    background_color: string;
    enabled: boolean;
    input_required: boolean;
    skip_queue: boolean;
    cooldown_seconds: number;
    max_per_stream: number;
    max_per_user_per_stream: number;
  }
  const blank = (): Form => ({
    title: '', cost: 100, prompt: '', background_color: '#9147ff', enabled: true, input_required: false, skip_queue: false,
    cooldown_seconds: 0, max_per_stream: 0, max_per_user_per_stream: 0,
  });

  let data = $state<RewardList | undefined>();
  let loadError = $state('');
  let notice = $state('');
  let error = $state('');
  let busy = $state(false);
  let form = $state<Form>(blank());
  let formError = $state('');
  let deleting = $state<Reward | undefined>();
  let queueOf = $state<Reward | undefined>();
  let queue = $state<Redemption[]>([]);
  let queueError = $state('');

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));
  const manageable = $derived(data?.state === 'ok' && data.can_manage);

  async function load() {
    try {
      data = await getRewards();
      loadError = '';
    } catch (e) {
      loadError = message(e);
    }
  }

  async function loadQueue() {
    const r = queueOf;
    if (!r) return;
    try {
      queue = (await getRedemptions(r.id)).redemptions;
      queueError = '';
    } catch (e) {
      queueError = message(e);
    }
  }

  onMount(() => {
    load();
    const id = setInterval(() => {
      load();
      loadQueue();
    }, refreshMs);
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

  function edit(r: Reward) {
    formError = '';
    form = {
      id: r.id, title: r.title, cost: r.cost, prompt: r.prompt, background_color: (r.background_color || '#9147ff').toLowerCase(),
      enabled: r.enabled, input_required: r.input_required, skip_queue: r.skip_queue, cooldown_seconds: r.cooldown_seconds,
      max_per_stream: r.max_per_stream, max_per_user_per_stream: r.max_per_user_per_stream,
    };
  }

  /** A reward made on Twitch can't be changed here: start a managed one like it. */
  function copy(r: Reward) {
    edit(r);
    form.id = undefined;
    notice = `The form below is filled in from “${r.title}”. Twitch needs a different title, or delete the original on Twitch first.`;
  }

  async function save(e: SubmitEvent) {
    e.preventDefault();
    formError = '';
    const title = form.title.trim();
    const n = (v: unknown) => Math.round(Number(v));
    if (!title || title.length > 45) {
      formError = 'A title is 1 to 45 characters.';
      return;
    }
    if (!(n(form.cost) >= 1)) {
      formError = 'A reward costs at least 1 point.';
      return;
    }
    if (form.input_required && !form.prompt.trim()) {
      formError = 'Say what viewers should type, in the description.';
      return;
    }
    const change: RewardChange = {
      title, cost: n(form.cost), prompt: form.prompt, background_color: form.background_color, enabled: form.enabled,
      input_required: form.input_required, skip_queue: form.skip_queue, cooldown_seconds: n(form.cooldown_seconds),
      max_per_stream: n(form.max_per_stream), max_per_user_per_stream: n(form.max_per_user_per_stream),
    };
    const id = form.id;
    if (await act(() => (id ? changeReward(id, change) : addReward(change)), id ? `Saved “${title}”.` : `Made “${title}”.`)) {
      form = blank();
    }
  }

  function remove(r: Reward) {
    deleting = undefined;
    if (queueOf?.id === r.id) queueOf = undefined;
    act(() => deleteReward(r.id), `Deleted “${r.title}”. Twitch marked any waiting redemptions done.`);
  }

  async function openQueue(r: Reward) {
    queueOf = r;
    queue = [];
    await loadQueue();
  }

  async function handle(d: Redemption, action: 'done' | 'refund') {
    const r = queueOf;
    if (!r) return;
    const ok = await act(
      () => setRedemption(r.id, d.id, action),
      action === 'done' ? `Marked ${d.user_name}’s “${r.title}” done.` : `Refunded ${d.user_name}’s ${d.cost} points.`,
    );
    if (ok) await loadQueue();
  }

  const stateWords = (r: Reward) => (!r.enabled ? 'Off' : r.paused ? 'Paused' : 'On');
  const limits = (r: Reward) =>
    [
      r.cooldown_seconds ? `cooldown ${r.cooldown_seconds}s` : '',
      r.max_per_stream ? `${r.max_per_stream} per stream` : '',
      r.max_per_user_per_stream ? `${r.max_per_user_per_stream} per viewer` : '',
      r.input_required ? 'viewers type something' : '',
      r.skip_queue ? 'skips the queue' : '',
    ]
      .filter(Boolean)
      .join(', ');
</script>

<h1>Rewards</h1>
<p class="sg-muted screen-intro">
  Your channel point rewards on Twitch. StreamGremlin can change the ones it made, and mark their redemptions done or refund
  them. Rewards made on Twitch still start workflows (“Someone redeems channel points”).
</p>

<section class="sg-panel card" aria-labelledby="rewards-heading" aria-busy={busy}>
  <h2 id="rewards-heading">Your rewards</h2>
  {#if loadError}
    <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {loadError}</p>
  {:else if data?.state === 'not_signed_in'}
    <p class="state state-unknown"><span aria-hidden="true">?</span> Sign in with your Twitch account on the Connections screen to see your rewards.</p>
  {:else if data?.state === 'needs_permission'}
    <p class="state state-unknown" role="alert">
      <span aria-hidden="true">!</span> Sign in to Twitch again on the Connections screen and allow channel point rewards.
    </p>
  {:else if data?.state === 'no_channel_points'}
    <p class="state state-unknown" role="status">
      <span aria-hidden="true">!</span> Twitch gives channel points to Affiliates and Partners, and says your channel isn’t one yet.
      Once it is, your rewards show here; workflows for redemptions are ready for then.
    </p>
  {:else if data?.state === 'unknown'}
    <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {data.detail}</p>
  {:else if data && data.rewards.length === 0}
    <p class="sg-muted">No rewards yet. Make one below.</p>
  {/if}
  {#if data?.state === 'ok' && !data.can_manage}
    <p class="state state-unknown"><span aria-hidden="true">!</span> To make and change rewards, sign in to Twitch again and allow it.</p>
  {/if}
  <ul class="secret-list" aria-label="Rewards">
    {#each data?.rewards ?? [] as r (r.id)}
      <li class="secret-row">
        <span class="reward-swatch" style:background-color={r.background_color} aria-hidden="true"></span>
        <span class="secret-info">
          <strong>{r.title}</strong>
          <span class="secret-saved sg-muted">
            {r.cost.toLocaleString()} points · {stateWords(r)} · {r.managed ? 'Made in StreamGremlin' : 'Made on Twitch: trigger only'}
            {#if limits(r)}<br />{limits(r)}{/if}
          </span>
        </span>
        {#if r.managed && manageable}
          <button type="button" class="button" aria-label={`Edit ${r.title}`} disabled={busy} onclick={() => edit(r)}>Edit</button>
          <button type="button" class="button" aria-label={`${r.paused ? 'Resume' : 'Pause'} ${r.title}`} disabled={busy}
            onclick={() => act(() => changeReward(r.id, { paused: !r.paused }), `${r.paused ? 'Resumed' : 'Paused'} “${r.title}”.`)}
            >{r.paused ? 'Resume' : 'Pause'}</button
          >
          <button type="button" class="button" aria-label={`Turn ${r.enabled ? 'off' : 'on'} ${r.title}`} disabled={busy}
            onclick={() => act(() => changeReward(r.id, { enabled: !r.enabled }), `Turned “${r.title}” ${r.enabled ? 'off' : 'on'}.`)}
            >{r.enabled ? 'Turn off' : 'Turn on'}</button
          >
          {#if !r.skip_queue}
            <button type="button" class="button" aria-label={`Queue of ${r.title}`} aria-pressed={queueOf?.id === r.id}
              onclick={() => openQueue(r)}>Queue</button
            >
          {/if}
          <button type="button" class="button button-danger" aria-label={`Delete ${r.title}`} disabled={busy} onclick={() => (deleting = r)}
            >Delete</button
          >
        {:else if !r.managed && manageable}
          <button type="button" class="button" aria-label={`Make a copy of ${r.title}`} disabled={busy} onclick={() => copy(r)}
            >Make a copy</button
          >
        {/if}
      </li>
    {/each}
  </ul>
  <p class="saved" role="status">{notice}</p>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
</section>

{#if queueOf}
  {@const r = queueOf}
  <section class="sg-panel card" aria-labelledby="queue-heading">
    <h2 id="queue-heading">Waiting: {r.title}</h2>
    {#if queueError}
      <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {queueError}</p>
    {:else if queue.length === 0}
      <p class="sg-muted">Nothing waiting.</p>
    {/if}
    <ul class="secret-list" aria-label={`Redemptions of ${r.title}`}>
      {#each queue as d (d.id)}
        <li class="secret-row">
          <span class="secret-info">
            <strong>{d.user_name}</strong>
            <span class="secret-saved sg-muted">{eventTime(d.redeemed_at)}{d.input ? ` · “${d.input}”` : ''}</span>
          </span>
          <button type="button" class="button" aria-label={`Mark ${d.user_name}'s redemption done`} disabled={busy} onclick={() => handle(d, 'done')}
            >Done</button
          >
          <button type="button" class="button" aria-label={`Refund ${d.user_name}`} disabled={busy} onclick={() => handle(d, 'refund')}
            >Refund</button
          >
        </li>
      {/each}
    </ul>
    <div class="row-actions">
      <button type="button" class="button" onclick={() => (queueOf = undefined)}>Close the queue</button>
    </div>
  </section>
{/if}

{#if manageable}
  <section class="sg-panel card" aria-labelledby="reward-form-heading">
    <h2 id="reward-form-heading">{form.id ? `Edit “${form.title}”` : 'Make a reward'}</h2>
    <form class="test-form" onsubmit={save}>
      <label class="field">
        <span>Title</span>
        <input class="sg-input" bind:value={form.title} maxlength="45" placeholder="Hydrate" />
      </label>
      <label class="field">
        <span>Cost (points)</span>
        <input class="sg-input" type="number" min="1" bind:value={form.cost} />
      </label>
      <label class="field">
        <span>Description</span>
        <input class="sg-input" bind:value={form.prompt} maxlength="200" placeholder="Remind me to drink water" />
      </label>
      <label class="choice-inline">
        <input type="checkbox" bind:checked={form.input_required} /> Viewers type something with it
      </label>
      <label class="field">
        <span>Color</span>
        <input class="sg-input color-input" type="color" bind:value={form.background_color} />
      </label>
      <label class="field">
        <span>Cooldown (seconds, 0 for none)</span>
        <input class="sg-input" type="number" min="0" max="604800" bind:value={form.cooldown_seconds} />
      </label>
      <label class="field">
        <span>Most per stream (0 for no limit)</span>
        <input class="sg-input" type="number" min="0" bind:value={form.max_per_stream} />
      </label>
      <label class="field">
        <span>Most per viewer per stream (0 for no limit)</span>
        <input class="sg-input" type="number" min="0" bind:value={form.max_per_user_per_stream} />
      </label>
      <label class="choice-inline">
        <input type="checkbox" bind:checked={form.skip_queue} /> Skip the queue (done at once; can’t be refunded)
      </label>
      <label class="choice-inline">
        <input type="checkbox" bind:checked={form.enabled} /> Turned on
      </label>
      {#if formError}<p class="error" role="alert">{formError}</p>{/if}
      <div class="row-actions">
        <button type="submit" class="sg-button" disabled={busy}>{form.id ? 'Save' : 'Make it'}</button>
        {#if form.id}
          <button type="button" class="button" onclick={() => (form = blank())}>Cancel</button>
        {/if}
      </div>
    </form>
  </section>
{/if}

{#if deleting}
  {@const r = deleting}
  <ConfirmDialog
    title={`Delete “${r.title}”?`}
    message="It's removed from your channel. Twitch marks any redemptions still waiting as done, so they can't be refunded after."
    confirmLabel="Delete"
    onconfirm={() => remove(r)}
    oncancel={() => (deleting = undefined)}
  />
{/if}
