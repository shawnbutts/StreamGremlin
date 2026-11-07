<script lang="ts">
  import { onMount } from 'svelte';
  import { ApiError, getStatus, type Status } from '../lib/api';
  import { duration } from '../lib/format';

  let { refreshMs = 10_000 }: { refreshMs?: number } = $props();

  let status = $state<Status | undefined>();
  let error = $state<string | undefined>();
  let now = $state(Date.now());

  async function load() {
    try {
      status = await getStatus();
      error = undefined;
    } catch (e) {
      // Never keep showing "Running" from an old answer.
      status = undefined;
      error = e instanceof ApiError ? e.message : String(e);
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

  const uptime = $derived(status ? duration((now - Date.parse(status.started_at)) / 1000) : '');
</script>

<h1>Home</h1>

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
