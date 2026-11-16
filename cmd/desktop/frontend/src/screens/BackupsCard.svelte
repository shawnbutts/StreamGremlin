<script lang="ts">
  // Settings → Backups and history: back up now, automatic backups, check,
  // restore (at the next start, after asking) and delete; and how long to
  // keep events and runs. Sign-ins and passwords are never in a backup.
  import { onMount } from 'svelte';
  import {
    ApiError,
    cancelRestore,
    createBackup,
    deleteBackup,
    getBackups,
    putMaintenance,
    restartEngine,
    stageRestore,
    sweepNow,
    verifyBackup,
    type BackupInfo,
    type Backups,
    type MaintenanceSettings,
  } from '../lib/api';
  import ConfirmDialog from '../lib/ConfirmDialog.svelte';
  import { eventTime } from '../lib/events';

  let { reload = () => window.location.reload() }: { reload?: () => void } = $props();

  let data = $state<Backups | undefined>();
  let form = $state<MaintenanceSettings>({ backup_every_hours: 24, backup_keep: 7, events_days: 30, runs_days: 90 });
  let loadError = $state('');
  let notice = $state('');
  let error = $state('');
  let busy = $state(false);
  let restoring = $state<BackupInfo | undefined>();
  let deleting = $state<BackupInfo | undefined>();

  const kindWords: Record<string, string> = {
    manual: 'Made by you',
    scheduled: 'Automatic',
    'pre-restore': 'Before a restore',
    'pre-migration': 'Before an update (database only)',
    'pre-update': 'Before installing an update',
  };
  const every = [
    { hours: 24, label: 'Every day' },
    { hours: 12, label: 'Every 12 hours' },
    { hours: 168, label: 'Every week' },
    { hours: 0, label: 'Never' },
  ];
  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));
  const mb = (n: number) => (n < 1e6 ? `${Math.max(1, Math.round(n / 1e3))} KB` : `${(n / 1e6).toFixed(1)} MB`);
  const when = (iso: string) => eventTime(iso);

  async function load(resetForm = false) {
    try {
      data = await getBackups();
      if (resetForm) form = { ...data.settings };
      loadError = '';
    } catch (e) {
      loadError = message(e);
    }
  }

  onMount(() => {
    load(true);
  });

  async function act(f: () => Promise<unknown>, done: (r: unknown) => string) {
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

  async function restore(b: BackupInfo) {
    restoring = undefined;
    busy = true;
    error = '';
    notice = '';
    try {
      await stageRestore(b.name);
    } catch (e) {
      error = `It can’t be restored: ${message(e)}`;
      busy = false;
      await load();
      return;
    }
    notice = 'Restarting StreamGremlin to restore it…';
    try {
      await restartEngine();
      reload();
    } catch (e) {
      busy = false;
      error = `It will be restored when StreamGremlin next starts. (Restarting now didn’t work: ${message(e)})`;
      await load();
    }
  }

  function remove(b: BackupInfo) {
    deleting = undefined;
    act(() => deleteBackup(b.name), () => 'Deleted.');
  }

  async function saveSettings(e: SubmitEvent) {
    e.preventDefault();
    await act(() => putMaintenance(form), () => 'Saved.');
  }
</script>

<section class="sg-panel card" aria-labelledby="backups-heading" aria-busy={busy}>
  <h2 id="backups-heading">Backups</h2>
  <p class="sg-muted">
    A backup holds your workflows, runs, events, variables and settings. Sign-ins and passwords are never in a backup: they
    stay in your system’s keyring, so after restoring on another computer you sign in again.
  </p>
  {#if loadError}
    <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {loadError}</p>
  {/if}
  {#if data?.last_restore}
    {@const r = data.last_restore}
    {#if r.ok}
      <p class="secret-check">
        <span class="icon-ok" aria-hidden="true">✓</span> Restored the backup from {r.from} ({when(r.at)}).
        {#if r.kept_aside}What it replaced couldn’t be read, so its files were kept in <code>backups/{r.kept_aside}</code>.{:else}What
          it replaced is the “Before a restore” backup.{/if}
      </p>
    {:else}
      <p class="error">The restore from {r.from} ({when(r.at)}) didn’t happen, and nothing was changed: {r.error}</p>
    {/if}
  {/if}
  {#if data?.pending}
    <div class="fill-in" role="status">
      <p><strong>{data.pending}</strong> will be restored when StreamGremlin next starts.</p>
      <div class="row-actions">
        <button type="button" class="sg-button" disabled={busy}
          onclick={async () => { await restartEngine().then(reload).catch((e) => (error = message(e))); }}>Restart now</button>
        <button type="button" class="button" disabled={busy}
          onclick={() => act(() => cancelRestore(data!.pending!), () => 'Cancelled: nothing will be restored.')}>Cancel the restore</button>
      </div>
    </div>
  {/if}
  <div class="row-actions">
    <button type="button" class="sg-button" disabled={busy} onclick={() => act(createBackup, (b) => `Backed up: ${(b as BackupInfo).name}.`)}
      >Back up now</button
    >
  </div>
  {#if data && data.backups.length === 0}
    <p class="sg-muted">No backups yet.</p>
  {/if}
  <ul class="secret-list" aria-label="Backups">
    {#each data?.backups ?? [] as b (b.name)}
      <li class="secret-row">
        <span class="secret-info">
          <strong>{kindWords[b.kind] ?? b.kind}</strong>, {when(b.created_at)}
          <span class="sg-muted secret-saved">{mb(b.size)}{b.app_version ? `, from version ${b.app_version}` : ''}</span>
          {#if b.problem}<span class="error secret-saved">Can’t be used: {b.problem}</span>{/if}
        </span>
        <button type="button" class="button" aria-label={`Check the backup from ${when(b.created_at)}`} disabled={busy}
          onclick={() => act(() => verifyBackup(b.name), () => `The backup from ${when(b.created_at)} is good: every file matches, and it can be restored.`)}
          >Check</button
        >
        <button type="button" class="button" aria-label={`Restore the backup from ${when(b.created_at)}`} disabled={busy || !!b.problem}
          onclick={() => (restoring = b)}>Restore</button
        >
        <button type="button" class="button button-danger" aria-label={`Delete the backup from ${when(b.created_at)}`}
          disabled={busy || b.name === data?.pending} onclick={() => (deleting = b)}>Delete</button
        >
      </li>
    {/each}
  </ul>
  <p class="saved" role="status">{notice}</p>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
</section>

<section class="sg-panel card" aria-labelledby="history-heading" aria-busy={busy}>
  <h2 id="history-heading">Automatic backups and history</h2>
  <form class="test-form" onsubmit={saveSettings}>
    <label class="field">
      <span>Back up automatically</span>
      <select class="sg-input" bind:value={form.backup_every_hours}>
        {#each every as e (e.hours)}<option value={e.hours}>{e.label}</option>{/each}
        {#if !every.some((e) => e.hours === form.backup_every_hours)}<option value={form.backup_every_hours}>Every {form.backup_every_hours} hours</option>{/if}
      </select>
    </label>
    <label class="field">
      <span>Automatic backups to keep</span>
      <input class="sg-input" type="number" min="1" max="60" bind:value={form.backup_keep} />
      <span class="sg-muted field-hint">Older automatic ones are deleted. Ones you make, and ones from before a restore, are kept until you delete them.</span>
    </label>
    <label class="field">
      <span>Keep events for (days)</span>
      <input class="sg-input" type="number" min="0" max="3650" bind:value={form.events_days} />
    </label>
    <label class="field">
      <span>Keep finished runs for (days)</span>
      <input class="sg-input" type="number" min="0" max="3650" bind:value={form.runs_days} />
      <span class="sg-muted field-hint">0 keeps them forever. Workflows, their versions and saved variables are never cleared.</span>
    </label>
    <div class="row-actions">
      <button type="submit" class="sg-button" disabled={busy}>Save</button>
      <button type="button" class="button" disabled={busy}
        onclick={() => act(sweepNow, (r) => { const s = r as { events: number; runs: number }; return `Cleared ${s.events} old events and ${s.runs} old runs.`; })}
        >Clear old history now</button
      >
    </div>
  </form>
  {#if data}
    <dl class="facts">
      <dt>Last automatic backup</dt>
      <dd>{data.status.last_backup_at ? when(data.status.last_backup_at) : 'None yet'}</dd>
      <dt>Next</dt>
      <dd>{data.status.next_backup_at ? when(data.status.next_backup_at) : 'Not scheduled'}</dd>
      {#if data.status.last_sweep}
        <dt>History last cleared</dt>
        <dd>{when(data.status.last_sweep.at)}: {data.status.last_sweep.events} events, {data.status.last_sweep.runs} runs</dd>
      {/if}
    </dl>
    {#if data.status.last_backup_error}
      <p class="error">The last automatic backup failed: {data.status.last_backup_error}. It’s tried again in an hour.</p>
    {/if}
  {/if}
</section>

{#if restoring}
  {@const b = restoring}
  <ConfirmDialog
    title="Restore this backup?"
    message={`StreamGremlin restarts and goes back to how it was ${when(b.created_at)}: workflows, runs, events, variables and settings. What you have now is backed up first, so you can come back to it. Sign-ins and passwords aren’t changed.`}
    confirmLabel="Restore and restart"
    onconfirm={() => restore(b)}
    oncancel={() => (restoring = undefined)}
  />
{/if}

{#if deleting}
  {@const b = deleting}
  <ConfirmDialog
    title="Delete this backup?"
    message={`The ${(kindWords[b.kind] ?? b.kind).toLowerCase()} backup from ${when(b.created_at)} is deleted for good.`}
    confirmLabel="Delete"
    onconfirm={() => remove(b)}
    oncancel={() => (deleting = undefined)}
  />
{/if}
