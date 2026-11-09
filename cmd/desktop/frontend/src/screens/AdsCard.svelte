<script lang="ts">
  // Ads on Home (docs/adr/0024-ads.md): Twitch's schedule, a break Twitch
  // said began (its end is an estimate), and asking for a break or a snooze.
  // What was asked and what happened are shown apart: "accepted" is never
  // "an ad is running". Hidden for channels that can't run ads.
  import { onMount } from 'svelte';
  import { ApiError, getAds, snoozeAd, startCommercial, type AdRequest, type AdsView } from '../lib/api';
  import ConfirmDialog from '../lib/ConfirmDialog.svelte';
  import { duration } from '../lib/format';

  let { refreshMs = 10000 }: { refreshMs?: number } = $props();

  let view = $state<AdsView | undefined>();
  let loadError = $state('');
  let now = $state(Date.now());
  let busy = $state(false);
  let notice = $state('');
  let error = $state('');
  let length = $state(90);
  let confirming = $state(false);
  // Shown once Twitch says this channel has ads; then an error shows as
  // unknown, never as the old schedule.
  let eligible = $state(false);

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));
  const LENGTHS = [30, 60, 90, 120, 150, 180];

  async function load() {
    try {
      view = await getAds();
      loadError = '';
      eligible = view.state !== 'not_signed_in' && view.state !== 'no_channel_points';
    } catch (e) {
      view = undefined;
      loadError = message(e);
    }
  }

  onMount(() => {
    load();
    const refresh = setInterval(load, refreshMs);
    const tick = setInterval(() => (now = Date.now()), 1000);
    return () => {
      clearInterval(refresh);
      clearInterval(tick);
    };
  });

  // A key per click: if the answer is lost and it's sent again, Twitch is
  // still asked once.
  const key = () => (crypto.randomUUID ? crypto.randomUUID() : `${Date.now()}-${Math.random()}`);

  async function act(f: () => Promise<AdRequest>, done: (q: AdRequest) => string) {
    busy = true;
    error = '';
    notice = '';
    try {
      notice = done(await f());
    } catch (e) {
      error = message(e);
    } finally {
      busy = false;
      await load();
    }
  }

  function runAd() {
    confirming = false;
    act(
      () => startCommercial(length, key()),
      (q) => `Twitch accepted a ${q.accepted_length ?? length}-second ad break. It shows here when Twitch says it began.`,
    );
  }

  const sc = $derived(view?.schedule ?? null);
  const clock = (iso?: string) => (iso ? new Date(iso).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' }) : '');
  const inSecs = (iso?: string) => (iso ? Math.max(0, Math.round((Date.parse(iso) - now) / 1000)) : 0);
  const running = $derived(view?.breaks.find((b) => Date.parse(b.end_estimated_at) > now));
  const recent = $derived(view?.requests.slice(0, 4) ?? []);
  const cooldownUntil = $derived(
    view?.requests.find((q) => q.action === 'commercial' && q.retry_until && Date.parse(q.retry_until) > now)?.retry_until,
  );
  const words = (q: AdRequest) => {
    const what = q.action === 'snooze' ? 'Snooze' : `${q.length}-second ad`;
    switch (q.status) {
      case 'accepted':
        return `${what}: Twitch accepted${q.confirmed_by === 'event' ? ', and it began' : q.confirmed_by === 'schedule' ? ', found in Twitch’s schedule' : ''}`;
      case 'refused':
        return `${what}: refused, nothing happened. ${q.message ?? ''}`;
      case 'unknown':
        return `${what}: unclear. It may or may not have started; StreamGremlin is checking and won’t ask again.`;
      default:
        return `${what}: sending…`;
    }
  };
</script>

{#if eligible}
  <section class="sg-panel card" aria-labelledby="ads-heading" aria-busy={busy}>
    <h2 id="ads-heading">Ads</h2>
    {#if loadError}
      <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {loadError}</p>
    {:else if view?.state === 'needs_permission'}
      <p class="state state-unknown" role="alert">
        <span aria-hidden="true">!</span> Sign in to Twitch again on the Connections screen and allow ads.
      </p>
    {:else if view?.state === 'unknown'}
      <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {view.detail}</p>
    {/if}
    {#if running}
      <p class="state state-ok" role="status">
        <span aria-hidden="true">●</span> Ad break running: about {duration(inSecs(running.end_estimated_at))} left (estimated; Twitch
        doesn’t say when ads end).
      </p>
    {/if}
    {#if sc}
      <dl class="facts">
        <dt>Next ad</dt>
        <dd>
          {#if sc.next_ad_at}{clock(sc.next_ad_at)}, in {duration(inSecs(sc.next_ad_at))} ({sc.duration} seconds){:else}None scheduled
            (Twitch shows one while you’re live){/if}
        </dd>
        <dt>Snoozes left</dt>
        <dd>{sc.snooze_count}{sc.snooze_refresh_at ? `, another at ${clock(sc.snooze_refresh_at)}` : ''}</dd>
        <dt>Last ad</dt>
        <dd>{sc.last_ad_at ? clock(sc.last_ad_at) : 'None this stream'}</dd>
        {#if sc.preroll_free_seconds > 0}
          <dt>Pre-roll free</dt>
          <dd>{duration(sc.preroll_free_seconds)}</dd>
        {/if}
      </dl>
      <div class="row-actions">
        <label class="field inline-field">
          <span>Length</span>
          <select class="sg-input" bind:value={length}>
            {#each LENGTHS as n (n)}<option value={n}>{n} seconds</option>{/each}
          </select>
        </label>
        <button type="button" class="button" disabled={busy || !!cooldownUntil} onclick={() => (confirming = true)}>Run an ad now</button>
        <button type="button" class="button" disabled={busy || !sc.next_ad_at || sc.snooze_count < 1}
          onclick={() => act(() => snoozeAd(key()), () => 'Snoozed: the next ad is 5 minutes later.')}>Snooze the next ad</button>
      </div>
      {#if cooldownUntil}
        <p class="sg-muted">Twitch needs a break between ads: you can run another in {duration(inSecs(cooldownUntil))}.</p>
      {/if}
    {/if}
    {#if recent.length}
      <h3>Asked recently</h3>
      <ul class="live-list" aria-label="Recent ad requests">
        {#each recent as q (q.id)}
          <li><span class="sg-muted">{clock(q.created_at)}</span> {words(q)}</li>
        {/each}
      </ul>
    {/if}
    <p class="saved" role="status">{notice}</p>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
  </section>
{/if}

{#if confirming}
  <ConfirmDialog
    title={`Run a ${length}-second ad now?`}
    message="Twitch starts an ad break for your viewers. It can’t be stopped once it starts."
    confirmLabel="Run the ad"
    onconfirm={runAd}
    oncancel={() => (confirming = false)}
  />
{/if}
