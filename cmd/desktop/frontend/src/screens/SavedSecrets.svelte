<script lang="ts">
  // Settings → Passwords and sign-ins. Shows where secrets are kept and which
  // are saved, checks they can still be read, and forgets one. Values never
  // reach the app: the API doesn't return them.
  import { onMount } from 'svelte';
  import { ApiError, forgetSecret, getSecrets, type SecretItem, type SecretList } from '../lib/api';
  import ConfirmDialog from '../lib/ConfirmDialog.svelte';

  let list = $state<SecretList | undefined>();
  let error = $state('');
  let notice = $state('');
  let busy = $state(false);
  let forgetting = $state<SecretItem | undefined>();

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));
  const when = (iso: string) =>
    new Date(iso).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });

  async function load(check = false) {
    busy = true;
    error = '';
    try {
      list = await getSecrets(check);
      if (check) {
        const bad = list.secrets.filter((s) => s.state && s.state !== 'ok').length;
        notice = bad === 0 ? 'Checked: every one can be read.' : `Checked: ${bad} can't be read.`;
      }
    } catch (e) {
      error = check ? `Couldn't check them: ${message(e)}` : `Couldn't load them: ${message(e)}`;
    } finally {
      busy = false;
    }
  }

  onMount(() => {
    load();
  });

  async function forget(item: SecretItem) {
    forgetting = undefined;
    busy = true;
    notice = '';
    error = '';
    let failed = '';
    try {
      await forgetSecret(item.key);
    } catch (e) {
      failed = `Couldn't forget ${item.key}: ${message(e)}`;
    }
    await load();
    // After reloading, so the reload doesn't clear it.
    if (failed) error = failed;
    else notice = `Forgot ${item.key}.`;
  }
</script>

<section class="sg-panel card" aria-labelledby="secrets-heading" aria-busy={busy}>
  <h2 id="secrets-heading">Passwords and sign-ins</h2>
  {#if list}
    <p>
      StreamGremlin keeps them in <strong>{list.store.label}</strong>. It never shows them, and they're
      never put in backups, exports or logs.
    </p>
    {#if list.store.kind === 'file'}
      <p class="sg-muted">
        The file is <code>secrets.enc</code>, with its key in <code>secrets.key</code> beside it. Anyone
        who has both files can read them, so keep your data folder private.
      </p>
    {/if}

    {#if list.secrets.length === 0}
      <p class="sg-muted">Nothing saved yet. When you connect Twitch or OBS, their sign-ins are kept here.</p>
    {:else}
      <ul class="secret-list">
        {#each list.secrets as s (s.key)}
          <li class="secret-row">
            <span class="secret-info">
              <code>{s.key}</code>
              <span class="sg-muted secret-saved">Saved {when(s.updated_at)}</span>
              {#if s.state === 'ok'}
                <span class="secret-check"><span class="icon-ok" aria-hidden="true">✓</span> Can be read</span>
              {:else if s.state === 'missing'}
                <span class="secret-check"><span class="icon-bad" aria-hidden="true">✗</span> Missing: {s.detail}</span>
              {:else if s.state === 'unreadable'}
                <span class="secret-check"
                  ><span class="icon-bad" aria-hidden="true">✗</span> Can't be read: {s.detail}</span
                >
              {/if}
            </span>
            <button
              type="button"
              class="button button-danger"
              aria-label={`Forget ${s.key}`}
              disabled={busy}
              onclick={() => (forgetting = s)}>Forget</button
            >
          </li>
        {/each}
      </ul>
      <div class="row-actions">
        <button type="button" class="sg-button" disabled={busy} onclick={() => load(true)}
          >Check they can be read</button
        >
      </div>
    {/if}
  {:else if !error}
    <p class="sg-muted">Loading…</p>
  {/if}
  <p class="saved" role="status">{notice}</p>
  {#if error}
    <p class="error" role="alert">{error}</p>
  {/if}
</section>

{#if forgetting && list}
  {@const item = forgetting}
  <ConfirmDialog
    title={`Forget ${item.key}?`}
    message={`StreamGremlin deletes it from ${list.store.label}. Anything that uses it stops working until you sign in or enter it again.`}
    confirmLabel="Forget"
    onconfirm={() => forget(item)}
    oncancel={() => (forgetting = undefined)}
  />
{/if}
