<script lang="ts">
  // One run's trace: every step, what it was given (secrets hidden), what
  // it gave back, and why it failed or was skipped.
  import { ApiError, cancelJob, getJob, type Job, type StepRecord } from '../lib/api';

  let { id, onchange }: { id: string; onchange: () => void } = $props();

  let job = $state<Job | undefined>();
  let steps = $state<StepRecord[]>([]);
  let error = $state('');

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));
  const name = (s: StepRecord) =>
    s.action ?? ({ set: 'Set variables', do: 'Group', foreach: 'For each', choose: 'Random choice', wait: 'Wait' }[s.kind] ?? s.kind);
  const ms = (s: StepRecord) => Math.max(0, Date.parse(s.finished_at) - Date.parse(s.started_at));

  async function load() {
    try {
      const r = await getJob(id);
      job = r.job;
      steps = r.steps;
    } catch (e) {
      error = message(e);
    }
  }

  $effect(() => {
    void id;
    load();
  });

  async function cancel() {
    try {
      await cancelJob(id);
    } catch (e) {
      error = message(e);
    }
    await load();
    onchange();
  }
</script>

<div class="trace">
  {#if job}
    {#if job.dry_run}
      <p class="sg-muted">A test run: steps that would reach Twitch or OBS were skipped.</p>
    {/if}
    {#if job.status === 'running' || job.status === 'pending'}
      <button type="button" class="button button-danger" onclick={cancel}>Stop this run</button>
    {/if}
    {#if steps.length === 0}
      <p class="sg-muted">No steps ran.</p>
    {/if}
    <ol class="trace-steps">
      {#each steps as s, i (i)}
        <li class={`trace-step trace-${s.status}`}>
          <div>
            <span aria-hidden="true">{s.status === 'ok' ? '✓' : s.status === 'failed' ? '✗' : '–'}</span>
            <strong>{name(s)}</strong>
            <span class="sg-muted">{s.status === 'ok' ? 'done' : s.status}{s.reason ? `: ${s.reason}` : ''}, {ms(s)} ms</span>
          </div>
          {#if s.error}<p class="error">{s.error}</p>{/if}
          {#if s.input !== undefined}
            <pre class="payload" aria-label="Input">{JSON.stringify(s.input, null, 2)}</pre>
          {/if}
          {#if s.output !== undefined}
            <pre class="payload" aria-label="Output">{JSON.stringify(s.output, null, 2)}</pre>
          {/if}
        </li>
      {/each}
    </ol>
  {/if}
  {#if error}
    <p class="error" role="alert">{error}</p>
  {/if}
</div>
