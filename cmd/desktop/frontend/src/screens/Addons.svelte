<script lang="ts">
  // Add-ons (docs/adr/0036-packages-plugins-and-themes.md): wheel looks and
  // Lua plugins (docs/adr/0038-lua-plugins.md) installed from a ZIP.
  // Installing shows what it is first and asks, a plugin's permissions one
  // by one; removing says which overlays go back to Classic and which
  // workflows are turned off.
  import { onMount } from 'svelte';
  import {
    ApiError,
    checkPackage,
    getPackageAudit,
    getPackages,
    installPackage,
    removePackage,
    rollbackPackage,
    type InstalledPackage,
    type PackageAuditEntry,
    type PackagePreview,
    type VersionAnswer,
  } from '../lib/api';
  import ConfirmDialog from '../lib/ConfirmDialog.svelte';
  import PluginCard from './PluginCard.svelte';
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
  let rollingBack = $state<InstalledPackage | undefined>();
  // Another publisher's version: their name, typed, to replace the installed one.
  let confirmPublisher = $state('');
  // A plugin's permissions, each ticked.
  let approved = $state<Record<string, boolean>>({});
  // A plugin's update asks only for what isn't allowed already (D-072).
  const change = $derived(preview?.plugin_update);
  const toApprove = $derived(change ? change.new_permissions : (preview?.plugin?.permissions ?? []));
  const allApproved = $derived(toApprove.every((x) => approved[x.name]));
  const confirmed = $derived((!preview?.publisher_change || confirmPublisher === preview.publisher) && allApproved);

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));
  const kb = (n: number) => (n < 1e6 ? `${Math.max(1, Math.round(n / 1e3))} KB` : `${(n / 1e6).toFixed(1)} MB`);
  const ACTIONS: Record<PackageAuditEntry['action'], string> = {
    install: 'Installed',
    update: 'Updated',
    rollback: 'Rolled back',
    remove: 'Removed',
    refuse: 'Refused',
    on: 'Turned on',
    off: 'Turned off',
    settings: 'Settings changed for',
    failed: 'Stopped',
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

  // What a new version did to the overlays using it.
  function resets(a: VersionAnswer) {
    return a.overlays
      .filter((o) => o.reset.length)
      .map((o) => ` On ${o.overlay}, ${o.reset.join(', ')} went back to the default: ${a.version} doesn’t take what was set.`)
      .join('');
  }

  // What a plugin's new version turned off and reset, and whether it runs.
  function pluginNotes(a: VersionAnswer) {
    let out = '';
    if (a.workflows?.length) out += ` Turned off, as they used a step it doesn’t have (not deleted): ${a.workflows.join(', ')}.`;
    if (a.reset?.length) out += ` Back to the default: ${a.reset.join(', ')}.`;
    if (a.plugin?.error) out += ` It’s off: ${a.plugin.error}.`;
    else if (a.plugin?.enabled) out += ` It started again, fresh, as ${a.version}.`;
    return out;
  }

  // What rolling a plugin back does, for the dialog.
  function rollbackNotes(p: InstalledPackage) {
    const rb = p.plugin?.rollback;
    if (!rb) return '';
    let out = '';
    if (rb.new_permissions.length) out += ` ${p.rollback_to} asks for more: rolling back allows it to ${rb.new_permissions.map((x) => x.what.charAt(0).toLowerCase() + x.what.slice(1)).join('; ')}.`;
    if (rb.removed_steps.length) {
      out += ` ${p.rollback_to} doesn’t have ${rb.removed_steps.map((a) => a.label).join(', ')}`;
      out += rb.workflows.length ? `: these workflows use it and are turned off, not deleted: ${rb.workflows.join(', ')}.` : '.';
    }
    if (rb.reset.length) out += ` Back to the default: ${rb.reset.join(', ')}.`;
    if (p.plugin?.enabled) out += ' It starts again, fresh: its memory starts over.';
    return out;
  }

  function clearFile() {
    file = undefined;
    preview = undefined;
    confirmPublisher = '';
    approved = {};
    const input = document.getElementById('package-file') as HTMLInputElement | null;
    if (input) input.value = '';
  }

  // Picking a ZIP checks it at once: nothing is stored until Install.
  async function pick(e: Event) {
    const f = (e.currentTarget as HTMLInputElement).files?.[0];
    preview = undefined;
    approved = {};
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
      const a = await installPackage(
        file,
        p.digest,
        p.publisher_change ? confirmPublisher : '',
        p.plugin ? p.plugin.permissions.map((x) => x.name) : [],
      );
      notice = a.plugin && a.from
        ? `Updated ${a.name} from ${a.from} to ${a.version}.${pluginNotes(a)}`
        : a.plugin
        ? `Installed ${a.name} ${a.version}. It’s off: turn it on below when you’re ready, then add its steps to a workflow.`
        : a.from
        ? `Updated ${a.name} from ${a.from} to ${a.version}.${a.overlays.length ? ` ${a.overlays.map((o) => o.overlay).join(', ')} use${a.overlays.length === 1 ? 's' : ''} it now.` : ''}${resets(a)}`
        : `Installed ${a.name} ${a.version}. Pick it for a wheel overlay under Look, on Overlays.`;
      clearFile();
    } catch (e) {
      error = message(e);
    } finally {
      busy = false;
      await load();
    }
  }

  async function rollback(p: InstalledPackage) {
    rollingBack = undefined;
    busy = true;
    error = '';
    notice = '';
    try {
      const rb = p.plugin?.rollback;
      const a = await rollbackPackage(p.id, rb?.new_permissions.length ? rb.permissions : []);
      notice = `Rolled ${a.name} back from ${a.from} to ${a.version}.${a.plugin ? pluginNotes(a) : resets(a)}`;
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
      notice = `Removed ${p.name}.${r.overlays.length ? ` Back to the Classic look: ${r.overlays.join(', ')}.` : ''}${
        r.workflows?.length ? ` Turned off (not deleted): ${r.workflows.join(', ')}.` : ''
      }`;
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
  Wheel looks and Lua plugins made by other people, installed from a ZIP file. A look runs in a sandbox in OBS: it can’t
  reach the internet, your accounts or StreamGremlin’s settings, and the real result is always in chat and on the Wheels
  screen. A plugin adds steps to your workflows; it runs in a separate process and can only do what you allow it.
</p>

<section class="sg-panel card" aria-labelledby="addons-heading" aria-busy={busy}>
  <h2 id="addons-heading">Installed</h2>
  {#if loadError}
    <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {loadError}</p>
  {:else if list.length === 0}
    <p class="sg-muted">Nothing installed yet. Install a wheel look or a plugin from a ZIP below.</p>
  {/if}
  <ul class="secret-list" aria-label="Installed add-ons">
    {#each list as p (p.id)}
      {#if p.plugin}
        <li class="secret-row">
          <PluginCard {p} {busy} onchanged={load} onremove={() => (removing = p)} onrollback={() => (rollingBack = p)} />
        </li>
      {:else}
      <li class="secret-row">
        <div>
          <strong>{p.name}</strong>
          {p.version} <span class="sg-muted">· wheel look · published by {p.publisher} (not verified)</span>
          {#if p.description}<p class="sg-muted field-hint">{p.description}</p>{/if}
          <p class="sg-muted field-hint">
            {p.used_by.length ? `Used by ${p.used_by.join(', ')}.` : 'Not used by an overlay yet.'}
            StreamGremlin can’t check how it draws the odds.
            {p.kept.length ? `Kept to roll back to: ${p.kept.join(', ')}.` : ''}
          </p>
        </div>
        <div class="actions">
          {#if p.rollback_to}
            <button type="button" class="button" aria-label={`Roll ${p.name} back to ${p.rollback_to}`} disabled={busy} onclick={() => (rollingBack = p)}
              >Roll back to {p.rollback_to}</button
            >
          {/if}
          <button type="button" class="button button-danger" aria-label={`Remove ${p.name}`} disabled={busy} onclick={() => (removing = p)}
            >Remove</button
          >
        </div>
      </li>
      {/if}
    {/each}
  </ul>
  <p class="saved" role="status">{notice}</p>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
</section>

<section class="sg-panel card" aria-labelledby="install-heading">
  <h2 id="install-heading">Install an add-on</h2>
  <label class="field">
    <span>A look’s or plugin’s ZIP file</span>
    <input id="package-file" class="sg-input" type="file" accept=".zip,application/zip" disabled={busy} onchange={pick} />
  </label>
  <p class="sg-muted field-hint">A ZIP made by compressing its folder works too. Nothing is installed until you say so.</p>
  {#if preview}
    <div class="install-preview" aria-label="What it is">
      {#if preview.update && preview.installed}
        <h3>{preview.name} {preview.installed.version} → {preview.version}</h3>
        <p>
          {preview.older ? 'An older version than the one installed.' : 'An update.'}
          {preview.installed.version} is kept, so you can roll back to it.
          {#if preview.plugin}
            Its settings are kept where this version still takes them. If it’s on, it starts again, fresh: its memory starts over.
          {:else}
            Overlays using it switch together, keeping the settings this version still takes.
          {/if}
        </p>
      {:else}
        <h3>{preview.name} {preview.version}</h3>
      {/if}
      <p>{preview.plugin ? 'A Lua plugin' : 'A wheel look'}, published by {preview.publisher} <strong>(not verified)</strong>.</p>
      {#if preview.publisher_change && preview.installed}
        <div class="publisher-change" role="group" aria-labelledby="publisher-change-heading">
          <p id="publisher-change-heading">
            <strong>Another publisher.</strong> The installed {preview.installed.name} says it’s published by
            {preview.installed.publisher}; this one says {preview.publisher}. Neither is verified: someone else may have used the
            same id.
          </p>
          <label class="field">
            <span>To replace it, type the new publisher’s name: {preview.publisher}</span>
            <input class="sg-input" type="text" autocomplete="off" spellcheck="false" bind:value={confirmPublisher} />
          </label>
        </div>
      {/if}
      {#if preview.description}<p>{preview.description}</p>{/if}
      <p class="sg-muted">
        {preview.files} files, {kb(preview.size)}.
        {preview.settings?.length ? `Settings: ${preview.settings.map((s) => s.label).join(', ')}.` : ''}
        {preview.sounds ? 'It has its own sounds.' : ''}
        {preview.license ? `License: ${preview.license}.` : ''}
      </p>
      {#if preview.plugin}
        {@const pl = preview.plugin}
        <p>
          This plugin runs its own Lua in a separate process. It can’t reach your files, the internet or your accounts, only
          what you allow below. It keeps its own memory while it’s on. It’s installed off.
        </p>
        <p><strong>Steps it adds to workflows:</strong></p>
        <ul class="plugin-steps">
          {#each pl.actions as a (a.name)}<li><strong>{a.label}</strong>: {a.description}</li>{/each}
        </ul>
        {#if pl.events.length}
          <p><strong>Events it sends, to start your workflows:</strong></p>
          <ul class="plugin-steps">
            {#each pl.events as e (e.name)}<li><strong>{e.label}</strong>: {e.description}</li>{/each}
          </ul>
        {/if}
        {#if pl.templates.length}
          <p>
            <strong>Templates it adds to the gallery</strong>, in its own section: {pl.templates.map((t) => t.title).join(', ')}.
          </p>
        {/if}
        {#if change && (change.removed_steps.length || change.reset.length)}
          <div class="update-change" role="group" aria-label="What updating changes">
            {#if change.removed_steps.length}
              <p>
                <strong>Steps this version doesn’t have:</strong> {change.removed_steps.map((a) => a.label).join(', ')}.
                {#if change.workflows.length}
                  These workflows use them and are turned off, not deleted: {change.workflows.join(', ')}.
                {:else}
                  No workflow uses them.
                {/if}
              </p>
            {/if}
            {#if change.reset.length}
              <p>Back to the default, as this version doesn’t take what’s set: {change.reset.join(', ')}.</p>
            {/if}
          </div>
        {/if}
        {#if change && toApprove.length === 0}
          <p>
            {pl.permissions.length
              ? `It asks for what you allowed already: ${pl.permissions.map((x) => x.what).join('; ')}.`
              : 'It asks for no permissions: it only does what its steps give back.'}
          </p>
        {:else if toApprove.length}
          <fieldset class="permissions">
            <legend>{change ? 'This version asks for more. Tick each one you allow:' : 'It asks for permission to do these. Tick each one you allow:'}</legend>
            {#each toApprove as x (x.name)}
              <label class="choice-inline">
                <input type="checkbox" checked={approved[x.name] === true} onchange={(e) => (approved[x.name] = e.currentTarget.checked)} />
                {x.what}
              </label>
            {/each}
            <p class="sg-muted field-hint">
              It can only be {change ? 'updated' : 'installed'} with all of them allowed.
              {#if change && pl.permissions.length > toApprove.length}
                Allowed already: {pl.permissions.filter((x) => !toApprove.some((y) => y.name === x.name)).map((x) => x.what).join('; ')}.
              {/if}
            </p>
          </fieldset>
        {:else}
          <p>It asks for no permissions: it only does what its steps give back.</p>
        {/if}
      {:else}
      <p>
        This look runs its own code in OBS and the app’s previews. It can’t reach the internet, your accounts or
        StreamGremlin’s settings. It’s told the result StreamGremlin drew, but what it draws is up to whoever made it: the real
        result is always in chat and on the Wheels screen.
        StreamGremlin can’t check how it draws the odds: the real odds stay on the Wheels screen and in “Say a wheel’s odds”.
      </p>
      <p class="sg-muted">
        In OBS, it only loads while the wheel’s browser source has “Page permissions” at “No access to OBS”, the setting OBS
        starts with; otherwise Classic shows instead.
      </p>
      {/if}
      {#if preview.same}
        <p class="saved">It’s installed already.</p>
        <button type="button" class="button" onclick={clearFile}>Done</button>
      {:else}
        <div class="actions">
          <button type="button" class="button" disabled={busy || !confirmed} onclick={install}
            >{preview.update ? 'Update' : 'Install'} {preview.name}</button
          >
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

{#if rollingBack}
  {@const r = rollingBack}
  <ConfirmDialog
    title={`Roll ${r.name} back to ${r.rollback_to}?`}
    message={`${r.version} is kept, so you can update to it again.${r.used_by.length ? ` These overlays switch back with it: ${r.used_by.join(', ')}.` : ''}${rollbackNotes(r)}`}
    confirmLabel={r.plugin?.rollback?.new_permissions.length ? 'Allow and roll back' : 'Roll back'}
    onconfirm={() => rollback(r)}
    oncancel={() => (rollingBack = undefined)}
  />
{/if}

{#if removing}
  {@const r = removing}
  <ConfirmDialog
    title={`Remove ${r.name}?`}
    message={`Its files are deleted; its history stays.${r.used_by.length ? ` These overlays go back to the Classic look: ${r.used_by.join(', ')}.` : ''}${
      r.workflows?.length ? ` These workflows use its steps and are turned off, not deleted: ${r.workflows.join(', ')}.` : ''
    }${r.plugin ? ' Its settings and what it has done are deleted too.' : ''}`}
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
  .publisher-change,
  .update-change {
    margin: 8px 0;
    padding: 8px 12px;
    border-left: 4px solid var(--sg-warning);
  }
  .secret-row .actions {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
  }
  .permissions {
    margin: 8px 0;
    padding: 8px 12px;
    border-left: 4px solid var(--sg-warning);
    border-top: 0;
    border-right: 0;
    border-bottom: 0;
    display: grid;
    gap: 6px;
  }
  .plugin-steps {
    margin: 0 0 8px;
  }
  .history-list {
    margin: 0;
    padding: 0;
    list-style: none;
    display: grid;
    gap: 4px;
  }
</style>
