<script lang="ts">
  // Add-ons (docs/adr/0036-packages-plugins-and-themes.md): wheel looks
  // installed from a ZIP. Installing shows what it is first and asks;
  // removing says which overlays go back to Classic.
  import { onMount } from 'svelte';
  import {
    ApiError,
    checkPackage,
    getPackageAudit,
    getPackages,
    installPackage,
    removePackage,
    type InstalledPackage,
    type PackageAuditEntry,
    type PackagePreview,
  } from '../lib/api';
  import ConfirmDialog from '../lib/ConfirmDialog.svelte';
  import { eventTime } from '../lib/events';

  const MAX_BYTES = 16 * 1024 * 1024;

  let list = $state<InstalledPackage[]>([]);
  let audit = $state<PackageAuditEntry[]>([]);
  let loadError = $state('');
  let notice = $state('');
  let error = $state('');
  let busy = $state(false);
  let file = $state<File | undefined>();
  let preview = $state<PackagePreview | undefined>();
  let removing = $state<InstalledPackage | undefined>();

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));
  const kb = (n: number) => (n < 1e6 ? `${Math.max(1, Math.round(n / 1e3))} KB` : `${(n / 1e6).toFixed(1)} MB`);
  const ACTIONS: Record<PackageAuditEntry['action'], string> = {
    install: 'Installed',
    update: 'Updated',
    rollback: 'Rolled back',
    remove: 'Removed',
    refuse: 'Refused',
  };

  async function load() {
    try {
      const [p, a] = await Promise.all([getPackages(), getPackageAudit()]);
      list = p.packages;
      audit = a.audit;
      loadError = '';
    } catch (e) {
      loadError = message(e);
    }
  }

  onMount(load);

  function clearFile() {
    file = undefined;
    preview = undefined;
    const input = document.getElementById('package-file') as HTMLInputElement | null;
    if (input) input.value = '';
  }

  // Picking a ZIP checks it at once: nothing is stored until Install.
  async function pick(e: Event) {
    const f = (e.currentTarget as HTMLInputElement).files?.[0];
    preview = undefined;
    error = '';
    notice = '';
    file = f;
    if (!f) return;
    if (f.size > MAX_BYTES) {
      error = 'A package is up to 16 MB.';
      return;
    }
    busy = true;
    try {
      preview = await checkPackage(f);
    } catch (err) {
      error = `${f.name} can’t be installed: ${message(err)}`;
    } finally {
      busy = false;
    }
  }

  async function install() {
    if (!file || !preview) return;
    const p = preview;
    busy = true;
    error = '';
    try {
      await installPackage(file, p.digest);
      notice = `Installed ${p.name} ${p.version}. Pick it for a wheel overlay under Look, on Overlays.`;
      clearFile();
    } catch (e) {
      error = message(e);
    } finally {
      busy = false;
      await load();
    }
  }

  async function remove(p: InstalledPackage) {
    removing = undefined;
    busy = true;
    error = '';
    notice = '';
    try {
      const r = await removePackage(p.id);
      notice = `Removed ${p.name}.${r.overlays.length ? ` Back to the Classic look: ${r.overlays.join(', ')}.` : ''}`;
    } catch (e) {
      error = message(e);
    } finally {
      busy = false;
      await load();
    }
  }
</script>

<h1>Add-ons</h1>
<p class="sg-muted screen-intro">
  Wheel looks made by other people, installed from a ZIP file. Each runs in a sandbox: it can’t reach the internet, your
  accounts or StreamGremlin’s settings, and it only shows the result StreamGremlin drew.
</p>

<section class="sg-panel card" aria-labelledby="addons-heading" aria-busy={busy}>
  <h2 id="addons-heading">Installed</h2>
  {#if loadError}
    <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {loadError}</p>
  {:else if list.length === 0}
    <p class="sg-muted">Nothing installed yet. Install a wheel look from a ZIP below.</p>
  {/if}
  <ul class="secret-list" aria-label="Installed add-ons">
    {#each list as p (p.id)}
      <li class="secret-row">
        <div>
          <strong>{p.name}</strong>
          {p.version} <span class="sg-muted">· wheel look · published by {p.publisher} (not verified)</span>
          {#if p.description}<p class="sg-muted field-hint">{p.description}</p>{/if}
          <p class="sg-muted field-hint">
            {p.used_by.length ? `Used by ${p.used_by.join(', ')}.` : 'Not used by an overlay yet.'}
            StreamGremlin can’t check how it draws the odds.
          </p>
        </div>
        <button type="button" class="button button-danger" aria-label={`Remove ${p.name}`} disabled={busy} onclick={() => (removing = p)}
          >Remove</button
        >
      </li>
    {/each}
  </ul>
  <p class="saved" role="status">{notice}</p>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
</section>

<section class="sg-panel card" aria-labelledby="install-heading">
  <h2 id="install-heading">Install a wheel look</h2>
  <label class="field">
    <span>A look’s ZIP file</span>
    <input id="package-file" class="sg-input" type="file" accept=".zip,application/zip" disabled={busy} onchange={pick} />
  </label>
  <p class="sg-muted field-hint">A ZIP made by compressing a look’s folder works too. Nothing is installed until you say so.</p>
  {#if preview}
    <div class="install-preview" aria-label="What it is">
      <h3>{preview.name} {preview.version}</h3>
      <p>A wheel look, published by {preview.publisher} <strong>(not verified)</strong>.</p>
      {#if preview.description}<p>{preview.description}</p>{/if}
      <p class="sg-muted">
        {preview.files} files, {kb(preview.size)}.
        {preview.settings?.length ? `Settings: ${preview.settings.map((s) => s.label).join(', ')}.` : ''}
        {preview.sounds ? 'It has its own sounds.' : ''}
        {preview.license ? `License: ${preview.license}.` : ''}
      </p>
      <p>
        This look runs its own code in OBS and the app’s previews. It can’t reach the internet, your accounts or
        StreamGremlin’s settings, and it only shows the result StreamGremlin drew.
        StreamGremlin can’t check how it draws the odds: the real odds stay on the Wheels screen and in “Say a wheel’s odds”.
      </p>
      <p class="sg-muted">
        In OBS, it only loads while the wheel’s browser source has “Page permissions” at “No access to OBS”, the setting OBS
        starts with; otherwise Classic shows instead.
      </p>
      {#if preview.same}
        <p class="saved">It’s installed already.</p>
        <button type="button" class="button" onclick={clearFile}>Done</button>
      {:else}
        <div class="actions">
          <button type="button" class="button" disabled={busy} onclick={install}>Install {preview.name}</button>
          <button type="button" class="button" disabled={busy} onclick={clearFile}>Cancel</button>
        </div>
      {/if}
    </div>
  {/if}
</section>

<section class="sg-panel card" aria-labelledby="addons-history-heading">
  <h2 id="addons-history-heading">History</h2>
  {#if audit.length === 0}
    <p class="sg-muted">Nothing yet.</p>
  {:else}
    <ul class="history-list" aria-label="Add-on history">
      {#each audit as a (a.seq)}
        <li>
          <span class="sg-muted">{eventTime(a.at)}</span>
          {ACTIONS[a.action] ?? a.action}
          {a.package_id}
          {a.version}{a.action === 'refuse' ? `: ${a.detail.replace(/^packages: [a-z]+: /, '')}` : ''}
        </li>
      {/each}
    </ul>
  {/if}
</section>

{#if removing}
  {@const r = removing}
  <ConfirmDialog
    title={`Remove ${r.name}?`}
    message={`Its files are deleted; its history stays.${r.used_by.length ? ` These overlays go back to the Classic look: ${r.used_by.join(', ')}.` : ''}`}
    confirmLabel="Remove"
    onconfirm={() => remove(r)}
    oncancel={() => (removing = undefined)}
  />
{/if}

<style>
  .install-preview {
    margin-top: 12px;
    padding: 12px 16px;
    border: 1px solid var(--sg-border);
    border-radius: 8px;
  }
  .install-preview h3 {
    margin: 0 0 6px;
  }
  .history-list {
    margin: 0;
    padding: 0;
    list-style: none;
    display: grid;
    gap: 4px;
  }
</style>
