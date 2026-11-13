<script lang="ts">
  // Discord webhooks (D-089): a webhook posts to one Discord channel with no
  // bot account. Its link holds Discord's token, so it's pasted once, kept
  // with your passwords, and never shown again.
  import { onMount } from 'svelte';
  import ConfirmDialog from '../lib/ConfirmDialog.svelte';
  import { eventTime } from '../lib/events';
  import {
    ApiError,
    addDiscordWebhook,
    forgetDiscordWebhook,
    getDiscordWebhooks,
    testDiscordWebhook,
    type DiscordWebhook,
  } from '../lib/api';

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));

  let hooks = $state<DiscordWebhook[] | undefined>();
  let listError = $state('');
  let adding = $state(false);
  let name = $state('');
  let link = $state('');
  let busy = $state(false);
  let notice = $state('');
  let problem = $state('');
  let forgetting = $state<DiscordWebhook | undefined>();

  const nameOk = $derived(/^[a-z0-9][a-z0-9-]{0,31}$/.test(name.trim()));

  async function load() {
    try {
      hooks = (await getDiscordWebhooks()).webhooks;
      listError = '';
    } catch (e) {
      listError = message(e);
    }
  }

  async function add(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    notice = '';
    problem = '';
    try {
      const w = await addDiscordWebhook(name.trim(), link.trim());
      notice = `Added “${w.name}”: Discord’s webhook “${w.discord_name}”. Send a test to see it arrive, then use it in “Post to Discord”.`;
      name = '';
      link = '';
      adding = false;
      await load();
    } catch (err) {
      problem = message(err);
    } finally {
      link = '';
      busy = false;
    }
  }

  async function test(w: DiscordWebhook) {
    busy = true;
    notice = '';
    problem = '';
    try {
      await testDiscordWebhook(w.name);
      notice = `Posted a test through “${w.name}”. Look in its Discord channel.`;
    } catch (err) {
      problem = message(err);
    } finally {
      busy = false;
    }
  }

  async function forget(w: DiscordWebhook) {
    forgetting = undefined;
    notice = '';
    problem = '';
    try {
      await forgetDiscordWebhook(w.name);
      notice = `Forgot “${w.name}” here. It still exists in Discord until you delete it there.`;
    } catch (err) {
      problem = message(err);
    }
    await load();
  }

  onMount(load);
</script>

<section class="sg-panel card" aria-labelledby="discord-heading" aria-busy={busy}>
  <h2 id="discord-heading">Discord</h2>
  <p class="sg-muted">
    Post to your Discord server from workflows: go-live announcements, new clips, a summary when you go offline. Each webhook posts to
    one channel, with no bot to set up. Its link is kept with your passwords and never shown again.
  </p>

  {#if listError}
    <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {listError}</p>
  {:else if hooks && hooks.length === 0}
    <p>No webhooks yet.</p>
  {:else if hooks}
    <ul class="hook-list">
      {#each hooks as w (w.name)}
        <li>
          <div>
            <strong>{w.name}</strong>
            <span class="sg-muted">Discord’s webhook “{w.discord_name}”. Added {eventTime(w.added_at)}.</span>
          </div>
          <div class="row-actions">
            <button type="button" class="button" disabled={busy} onclick={() => test(w)}>Send a test</button>
            <button type="button" class="button button-danger" onclick={() => (forgetting = w)}>Forget…</button>
          </div>
        </li>
      {/each}
    </ul>
  {/if}

  {#if adding}
    <form class="add-form" onsubmit={add} aria-label="Add a Discord webhook">
      <ol class="sg-muted steps">
        <li>In Discord, open your server’s <strong>Server Settings → Integrations → Webhooks</strong>.</li>
        <li><strong>New Webhook</strong>, choose its channel and name, then <strong>Copy Webhook URL</strong>.</li>
        <li>Paste it here, and give it a short name to pick it by in workflows.</li>
      </ol>
      <label class="field"
        ><span>Name here</span>
        <input class="sg-input" bind:value={name} maxlength="32" placeholder="announcements" spellcheck="false" autocomplete="off" />
        <span class="sg-muted field-hint">Lower-case letters, digits and dashes.</span></label
      >
      <label class="field"
        ><span>Webhook link</span>
        <input class="sg-input" type="password" bind:value={link} placeholder="https://discord.com/api/webhooks/…" autocomplete="off" spellcheck="false" /></label
      >
      <div class="row-actions">
        <button type="submit" class="sg-button" disabled={busy || !nameOk || !link.trim()}>Add</button>
        <button type="button" class="button" onclick={() => ((adding = false), (link = ''))}>Cancel</button>
      </div>
    </form>
  {:else}
    <button type="button" class="sg-button" onclick={() => ((adding = true), (notice = ''), (problem = ''))}>Add a webhook</button>
  {/if}

  <p class="saved" role="status">{notice}</p>
  {#if problem}<p class="error" role="alert">{problem}</p>{/if}
</section>

{#if forgetting}
  {@const w = forgetting}
  <ConfirmDialog
    title={`Forget “${w.name}”?`}
    message="Workflows that post through it will fail until you add a webhook with that name again. The webhook stays in Discord: delete it there to stop it for good."
    confirmLabel="Forget"
    onconfirm={() => forget(w)}
    oncancel={() => (forgetting = undefined)}
  />
{/if}

<style>
  .hook-list {
    list-style: none;
    margin: 0 0 1rem;
    padding: 0;
    display: grid;
    gap: 0.5rem;
  }
  .hook-list li {
    display: flex;
    justify-content: space-between;
    align-items: center;
    flex-wrap: wrap;
    gap: 0.5rem 1rem;
    padding: 0.5rem 0.75rem;
    border: 1px solid var(--sg-border);
    border-radius: 8px;
  }
  .hook-list li > div:first-child {
    display: grid;
  }
  .add-form {
    display: grid;
    gap: 0.75rem;
    max-width: 36rem;
  }
  /* An empty notice takes no room, but stays in place to be announced. */
  .saved:empty {
    margin: 0;
    min-height: 0;
  }
  .steps {
    margin: 0;
    padding-left: 1.25rem;
  }
</style>
