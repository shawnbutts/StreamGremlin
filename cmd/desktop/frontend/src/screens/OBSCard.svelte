<script lang="ts">
  // OBS on Connections: the WebSocket address and password, the connection's
  // state in words, and, while connected, the scenes and their sources.
  import { ApiError, configureOBS, type OBSStatus } from '../lib/api';
  import OBSScenes from './OBSScenes.svelte';

  let { status, onchange }: { status: OBSStatus; onchange: () => void } = $props();

  let url = $state('');
  let password = $state('');
  let editing = $state(false);
  let busy = $state(false);
  let error = $state('');
  let notice = $state('');

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));

  async function save(enabled: boolean, clearPassword = false) {
    busy = true;
    error = '';
    notice = '';
    try {
      await configureOBS((editing ? url : status.url).trim(), enabled, password || undefined, clearPassword);
      password = '';
      editing = false;
      notice = clearPassword ? 'Password forgotten.' : enabled ? 'Connecting…' : 'Disconnected.';
    } catch (e) {
      error = message(e);
    } finally {
      busy = false;
      onchange();
    }
  }

  function startEditing() {
    url = status.url;
    password = '';
    editing = true;
  }
</script>

<section class="sg-panel card" aria-labelledby="obs-heading" aria-busy={busy}>
  <h2 id="obs-heading">OBS</h2>

  <p class="state">
    {#if status.state === 'connected'}
      <span class="state-ok"><span aria-hidden="true">●</span></span> Connected to OBS {status.obs_version}.
    {:else if status.state === 'connecting'}
      Connecting to OBS…
    {:else if status.state === 'disconnected'}
      <span class="state-unknown"><span aria-hidden="true">?</span></span> Not connected.
    {:else if status.state === 'needs_action'}
      <span class="state-unknown"><span aria-hidden="true">!</span></span> Needs your attention.
    {:else}
      Not connected. StreamGremlin can switch scenes and show or hide sources once it’s connected.
    {/if}
  </p>
  {#if status.detail}
    <p>{status.detail}</p>
  {/if}

  {#if editing || (!status.enabled && status.state === 'off')}
    <form
      class="test-form"
      onsubmit={(e) => {
        e.preventDefault();
        save(true);
      }}
    >
      <p class="sg-muted">
        In OBS, open <strong>Tools → WebSocket Server Settings</strong>, tick <strong>Enable WebSocket server</strong>,
        and click <strong>Show Connect Info</strong> for the port and password.
      </p>
      <label class="field">
        <span>Address</span>
        <input class="sg-input" value={editing ? url : status.url} oninput={(e) => { url = e.currentTarget.value; editing = true; }} spellcheck="false" />
      </label>
      <label class="field">
        <span>Password</span>
        <input
          class="sg-input"
          type="password"
          bind:value={password}
          autocomplete="off"
          placeholder={status.has_password ? 'Saved. Leave empty to keep it.' : 'Leave empty if OBS has none'}
        />
      </label>
      <div class="row-actions">
        <button type="submit" class="sg-button" disabled={busy}>Connect</button>
        {#if editing && status.enabled}
          <button type="button" class="button" onclick={() => (editing = false)}>Cancel</button>
        {/if}
      </div>
    </form>
  {:else}
    <p class="sg-muted">
      <code>{status.url}</code>, {status.has_password ? 'with a saved password' : 'with no password'}.
    </p>
    <div class="row-actions">
      <button type="button" class="button" onclick={startEditing}>Change</button>
      {#if status.enabled}
        <button type="button" class="button" disabled={busy} onclick={() => save(false)}>Disconnect</button>
      {:else}
        <button type="button" class="sg-button" disabled={busy} onclick={() => save(true)}>Connect</button>
      {/if}
      {#if status.has_password}
        <button type="button" class="button" disabled={busy} onclick={() => save(status.enabled, true)}>Forget the password</button>
      {/if}
    </div>
  {/if}
  <p class="saved" role="status">{notice}</p>
  {#if error}
    <p class="error" role="alert">{error}</p>
  {/if}

  {#if status.state === 'connected'}
    <OBSScenes current={status.scene ?? ''} />
  {/if}
</section>
