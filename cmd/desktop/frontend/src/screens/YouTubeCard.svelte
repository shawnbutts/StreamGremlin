<script lang="ts">
  // YouTube on Connections (M6 slice 1, D-096): whose Google project signs
  // in, with a step-by-step guide to making your own; your and the bot's
  // sign-ins; chat (M6 slice 2, D-097): the live broadcast being read,
  // and Find my stream; and today's quota as StreamGremlin counts it. Polls faster
  // while a sign-in is pending; when the engine stops answering it shows
  // Unknown, never the last answer as if it were current.
  import { onMount } from 'svelte';
  import {
    ApiError,
    cancelYouTubeSignIn,
    findYouTubeStream,
    getYouTube,
    openInBrowser,
    setYouTubeClient,
    startYouTubeSignIn,
    youtubeSignOut,
    type YouTubeAccount,
    type YouTubeStatus,
  } from '../lib/api';
  import ConfirmDialog from '../lib/ConfirmDialog.svelte';

  let { fastMs = 1500, slowMs = 15_000 }: { fastMs?: number; slowMs?: number } = $props();

  let yt = $state<YouTubeStatus | undefined>();
  let loadError = $state('');
  let clientId = $state('');
  let clientSecret = $state('');
  let showForm = $state(false);
  let saving = $state(false);
  let busy = $state(false);
  let formError = $state('');
  let notice = $state('');
  let error = $state('');
  let signOutOf = $state<YouTubeAccount | undefined>();

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));
  let wake: (() => void) | undefined;

  async function load(signal?: AbortSignal) {
    try {
      const v = await getYouTube(signal);
      if (!v?.streamer || !v.bot || !v.quota) throw new Error('an answer this version of the app doesn’t understand');
      yt = v;
      loadError = '';
    } catch (e) {
      if (signal?.aborted) return;
      yt = undefined;
      loadError = message(e);
    }
  }

  onMount(() => {
    const abort = new AbortController();
    (async () => {
      while (!abort.signal.aborted) {
        await load(abort.signal);
        const pending = yt && [yt.streamer, yt.bot].some((a) => a.state === 'signing_in' || a.state === 'unknown');
        await new Promise<void>((r) => {
          const t = setTimeout(r, pending || !yt ? fastMs : slowMs);
          wake = () => {
            clearTimeout(t);
            r();
          };
        });
      }
    })();
    return () => {
      abort.abort();
      wake?.();
    };
  });

  const refresh = () => wake?.();

  async function saveClient(id: string, secret: string) {
    saving = true;
    formError = '';
    notice = '';
    try {
      yt = await setYouTubeClient(id.trim(), secret.trim());
      notice = id ? 'Saved. You can sign in now.' : 'Using StreamGremlin’s Google project.';
      clientId = '';
      clientSecret = '';
      showForm = false;
    } catch (e) {
      formError = message(e);
    } finally {
      saving = false;
    }
  }

  async function run(action: () => Promise<unknown>, done = '') {
    busy = true;
    error = '';
    notice = '';
    try {
      await action();
      notice = done;
    } catch (e) {
      error = message(e);
    } finally {
      busy = false;
      refresh();
    }
  }

  const signIn = (a: YouTubeAccount) =>
    run(async () => {
      const p = await startYouTubeSignIn(a.role);
      // Open Google's page for them; if the browser can't be opened, the button is on screen.
      await openInBrowser(p.auth_url).catch(() => undefined);
    });

  const open = (url: string) => openInBrowser(url).catch((e) => (formError = message(e)));
  const until = (iso: string) => new Date(iso).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
  const at = (iso: string) => new Date(iso).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' });
  const findStream = () => run(async () => (yt = await findYouTubeStream()));
  const channelName = (a: YouTubeAccount) => (a.channel ? a.channel.title + (a.channel.handle ? ` (${a.channel.handle})` : '') : '');
</script>

<section class="sg-panel card" aria-labelledby="youtube-heading">
  <h2 id="youtube-heading">YouTube</h2>

  {#if loadError}
    <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {loadError}</p>
  {:else if !yt}
    <p class="sg-muted">Checking…</p>
  {:else}
    {#if yt.test_server}
      <p class="warning-card">
        <strong>Test mode.</strong> YouTube sign-in goes to a stand-in at <code>{yt.test_server}</code>, not the real Google or YouTube.
      </p>
    {/if}

    {#snippet clientForm(intro: string)}
      <form
        class="test-form"
        onsubmit={(e) => {
          e.preventDefault();
          saveClient(clientId, clientSecret);
        }}
      >
        <p>{intro}</p>
        <details class="guide">
          <summary>How to make your Google project (about 10 minutes, free)</summary>
          <ol class="steps">
            <li>
              <button type="button" class="link-button" onclick={() => open('https://console.cloud.google.com/projectcreate')}
                >Create a Google Cloud project</button
              >. Any name, such as “StreamGremlin”. No billing is needed.
            </li>
            <li>
              <button
                type="button"
                class="link-button"
                onclick={() => open('https://console.cloud.google.com/apis/library/youtube.googleapis.com')}
                >Turn on the YouTube Data API v3</button
              > for it: press <strong>Enable</strong>.
            </li>
            <li>
              <button type="button" class="link-button" onclick={() => open('https://console.cloud.google.com/auth/overview')}
                >Set up Google’s sign-in screen</button
              >: press <strong>Get started</strong>, give an app name and your email, and choose <strong>External</strong>.
            </li>
            <li>
              On <button type="button" class="link-button" onclick={() => open('https://console.cloud.google.com/auth/audience')}
                >Audience</button
              >, press <strong>Publish app</strong>. While it says Testing, Google ends sign-ins after 7 days. Your project
              doesn’t need Google’s review for you to use it: when you sign in, Google warns the app isn’t verified; choose
              <strong>Advanced</strong>, then go on to it.
            </li>
            <li>
              On <button type="button" class="link-button" onclick={() => open('https://console.cloud.google.com/auth/clients')}
                >Clients</button
              >, press <strong>Create client</strong> and choose <strong>Desktop app</strong>. Copy its client ID and
              client secret into the boxes below.
            </li>
          </ol>
          <p class="sg-muted">
            Your project has its own daily YouTube quota of 10,000 units, just for you. The secret is kept with your passwords.
          </p>
        </details>
        <label class="field">
          <span>Client ID</span>
          <input class="sg-input" bind:value={clientId} autocomplete="off" spellcheck="false" maxlength="200" />
        </label>
        <label class="field">
          <span>Client secret</span>
          <input class="sg-input" type="password" bind:value={clientSecret} autocomplete="off" spellcheck="false" maxlength="100" />
        </label>
        <div class="row-actions">
          <button type="submit" class="sg-button" disabled={saving || !clientId.trim()}>Save</button>
          {#if yt?.client_source !== 'none'}
            <button type="button" class="button" onclick={() => (showForm = false)}>Cancel</button>
          {/if}
        </div>
      </form>
    {/snippet}

    {#if yt.client_source === 'none'}
      {@render clientForm('To sign in to YouTube, StreamGremlin uses your own Google project for now. Make one, then paste its client ID and secret.')}
    {:else}
      <p class="sg-muted">
        {#if yt.client_source === 'built_in'}
          Using StreamGremlin’s Google project.
        {:else}
          Using your own Google project: <code>{yt.client_id}</code>{yt.has_secret ? ', with its client secret.' : ', without a client secret.'}
        {/if}
      </p>
      {#if showForm}
        {@render clientForm('Changing the client ID signs both accounts out.')}
      {:else}
        <div class="row-actions">
          <button type="button" class="button" onclick={() => (showForm = true)}
            >{yt.client_source === 'own' ? 'Change Google project' : 'Use my own Google project'}</button
          >
          {#if yt.client_source === 'own' && yt.has_built_in}
            <button type="button" class="button" disabled={saving} onclick={() => saveClient('', '')}>Use StreamGremlin’s project</button>
          {/if}
        </div>
      {/if}
    {/if}
    {#if formError}
      <p class="error" role="alert">{formError}</p>
    {/if}

    {#snippet account(a: YouTubeAccount, title: string, intro: string)}
      <section class="account" aria-labelledby={`youtube-${a.role}-heading`} aria-busy={busy}>
        <h3 id={`youtube-${a.role}-heading`}>{title}</h3>
        <p class="sg-muted">{intro}</p>
        <p class="state">
          {#if a.state === 'connected'}
            <span class="state-ok"><span aria-hidden="true">●</span></span> Signed in as <strong>{channelName(a)}</strong>
          {:else if a.state === 'signing_in'}
            Waiting for you to sign in with Google in your browser…
          {:else if a.state === 'signed_out'}
            Not signed in.
          {:else if a.state === 'needs_sign_in'}
            <span class="state-unknown"><span aria-hidden="true">!</span></span> Needs signing in again.
          {:else if a.state === 'not_configured'}
            Add your Google project above first.
          {:else}
            <span class="state-unknown"><span aria-hidden="true">?</span></span> Unknown{a.channel ? `: was signed in as ${a.channel.title}` : ''}.
          {/if}
        </p>
        {#if a.detail}
          <p>{a.detail}</p>
        {/if}
        {#if a.state === 'signing_in' && a.sign_in}
          {@const p = a.sign_in}
          <div class="code-box">
            <p>Google’s page opened in your browser. Pick {a.role === 'bot' ? 'the bot’s channel' : 'your channel'}, allow StreamGremlin, and come back here.</p>
            <p class="sg-muted">It waits until {until(p.expires_at)}.</p>
            <div class="row-actions">
              <button type="button" class="sg-button" onclick={() => run(() => openInBrowser(p.auth_url))}>Open Google’s page again</button>
              <button type="button" class="button" disabled={busy} onclick={() => run(() => cancelYouTubeSignIn(a.role))}>Cancel</button>
            </div>
          </div>
        {:else}
          <div class="row-actions">
            {#if a.state === 'connected' || (a.state === 'unknown' && a.channel)}
              {#if a.missing_scopes.length > 0}
                <button type="button" class="sg-button" disabled={busy} onclick={() => signIn(a)}>Sign in again</button>
              {/if}
              <button type="button" class="button" disabled={busy} onclick={() => (signOutOf = a)}>Sign out</button>
            {:else if a.state !== 'not_configured'}
              <button type="button" class="sg-button" disabled={busy} onclick={() => signIn(a)}
                >{a.state === 'needs_sign_in' ? 'Sign in again' : 'Sign in with Google'}</button
              >
            {/if}
          </div>
        {/if}
      </section>
    {/snippet}

    {@render account(yt.streamer, 'Your channel', 'Your own YouTube channel. Chat, Super Chats and memberships come from it.')}
    {@render account(
      yt.bot,
      'Bot channel (optional)',
      'A second YouTube channel that sends StreamGremlin’s chat messages, so they don’t appear under your name. In Google’s list, pick the bot’s account or brand account.',
    )}
    <p class="saved" role="status">{notice}</p>
    {#if error}
      <p class="error" role="alert">{error}</p>
    {/if}

    {#if yt.chat}
      {@const c = yt.chat}
      <section class="account" aria-labelledby="youtube-chat-heading">
        <h3 id="youtube-chat-heading">Chat</h3>
        <p class="state" aria-live="polite">
          {#if c.state === 'connected'}
            <span class="state-ok"><span aria-hidden="true">●</span></span> Reading the chat of <strong>{c.broadcast?.title}</strong>
          {:else if c.state === 'connecting'}
            Connecting to the chat of {c.broadcast?.title ?? 'your broadcast'}…
          {:else if c.state === 'looking'}
            Not live on YouTube.{c.next_check ? ` Checking again at ${at(c.next_check)}.` : ''}
          {:else if c.state === 'disconnected'}
            <span class="state-unknown"><span aria-hidden="true">?</span></span> Unknown: not reading YouTube chat right now. Trying again.
          {:else if c.state === 'quota'}
            <span class="state-unknown"><span aria-hidden="true">!</span></span> Paused: today’s quota is used up.
          {:else}
            Off.
          {/if}
        </p>
        {#if c.detail}
          <p>{c.detail}</p>
        {/if}
        {#if c.state === 'looking' || c.state === 'disconnected'}
          <p class="sg-muted">
            StreamGremlin checks every 2 minutes while you’re not live, and at once when your Twitch stream starts. Each
            check costs 1 unit.
          </p>
          <div class="row-actions">
            <button type="button" class="sg-button" disabled={busy} onclick={findStream}>Find my stream</button>
          </div>
        {:else if c.state === 'connected'}
          <p class="sg-muted">
            YouTube messages are in the event log. Live shows them alongside Twitch chat in the next update.
          </p>
        {/if}
      </section>
    {/if}

    <h3>Today’s quota</h3>
    {#if yt.quota.exhausted}
      <p class="state" role="alert">
        <span class="state-unknown"><span aria-hidden="true">!</span></span> Used up: YouTube said so. YouTube calls stop until
        {until(yt.quota.resets_at)}, when it resets.
      </p>
    {:else}
      <p>
        About <strong>{yt.quota.used.toLocaleString()}</strong> of {yt.quota.limit.toLocaleString()} units used. It resets at {until(
          yt.quota.resets_at,
        )} (midnight Pacific time).
      </p>
    {/if}
    <p class="sg-muted">
      StreamGremlin counts its own calls; YouTube doesn’t say. Reading chat costs 5 units each time it connects;
      sending a chat message costs 20; timing someone out costs 200.
    </p>
  {/if}
</section>

{#if signOutOf}
  {@const a = signOutOf}
  <ConfirmDialog
    title={`Sign out ${a.channel?.title ?? ''}?`}
    message={a.role === 'bot'
      ? 'StreamGremlin forgets the bot’s sign-in and tells Google to end it. YouTube chat goes out as you until a bot signs in again.'
      : 'StreamGremlin forgets your sign-in and tells Google to end it. YouTube stops until you sign in again.'}
    confirmLabel="Sign out"
    onconfirm={() => {
      const role = a.role;
      signOutOf = undefined;
      run(() => youtubeSignOut(role), 'Signed out.');
    }}
    oncancel={() => (signOutOf = undefined)}
  />
{/if}

<style>
  .guide summary {
    cursor: pointer;
    font-weight: 600;
  }
  .guide .steps {
    margin-top: 0.5rem;
  }
  .guide .steps li + li {
    margin-top: 0.4rem;
  }
  .saved:empty {
    margin: 0;
    min-height: 0;
  }
</style>
