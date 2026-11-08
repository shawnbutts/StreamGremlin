<script lang="ts">
  // One Twitch sign-in (yours or the bot's): its state in words, the code to
  // enter while signing in, and the buttons that fit the state.
  import {
    ApiError,
    cancelTwitchSignIn,
    openInBrowser,
    startTwitchSignIn,
    twitchSignOut,
    type AccountStatus,
  } from '../lib/api';
  import ConfirmDialog from '../lib/ConfirmDialog.svelte';

  let {
    account,
    title,
    intro,
    onchange,
  }: { account: AccountStatus; title: string; intro: string; onchange: () => void } = $props();

  let busy = $state(false);
  let error = $state('');
  let notice = $state('');
  let confirmSignOut = $state(false);

  const id = $derived(`twitch-${account.role}`);
  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));
  const until = (iso: string) => new Date(iso).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });

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
      onchange();
    }
  }

  const signIn = () =>
    run(async () => {
      const p = await startTwitchSignIn(account.role);
      // Open the page for them; if the browser can't be opened, the link and code are on screen.
      await openInBrowser(p.verification_uri).catch(() => undefined);
    });

  async function copyCode(code: string) {
    try {
      await navigator.clipboard.writeText(code);
      notice = 'Code copied.';
    } catch {
      notice = 'Couldn’t copy; type the code instead.';
    }
  }
</script>

<section class="account" aria-labelledby={`${id}-heading`} aria-busy={busy}>
  <h3 id={`${id}-heading`}>{title}</h3>
  <p class="sg-muted">{intro}</p>

  <p class="state">
    {#if account.state === 'connected'}
      <span class="state-ok"><span aria-hidden="true">●</span></span> Signed in as <strong>{account.login}</strong>
    {:else if account.state === 'signing_in'}
      Waiting for you to enter the code at Twitch…
    {:else if account.state === 'signed_out'}
      Not signed in.
    {:else if account.state === 'needs_sign_in'}
      <span class="state-unknown"><span aria-hidden="true">!</span></span> Needs signing in again.
    {:else if account.state === 'not_configured'}
      Add a Twitch Client ID above first.
    {:else}
      <span class="state-unknown"><span aria-hidden="true">?</span></span> Unknown{account.login
        ? `: was signed in as ${account.login}`
        : ''}.
    {/if}
  </p>
  {#if account.detail}
    <p>{account.detail}</p>
  {/if}

  {#if account.state === 'signing_in' && account.sign_in}
    {@const p = account.sign_in}
    <div class="code-box">
      <p>Go to <code>{p.verification_uri.split('?')[0]}</code> and enter this code:</p>
      <p class="user-code" aria-label={`Code ${p.user_code.split('').join(' ')}`}>{p.user_code}</p>
      <p class="sg-muted">The code works until {until(p.expires_at)}.</p>
      <div class="row-actions">
        <button type="button" class="sg-button" onclick={() => run(() => openInBrowser(p.verification_uri))}
          >Open Twitch in my browser</button
        >
        <button type="button" class="button" onclick={() => copyCode(p.user_code)}>Copy code</button>
        <button type="button" class="button" disabled={busy} onclick={() => run(() => cancelTwitchSignIn(account.role))}
          >Cancel</button
        >
      </div>
    </div>
  {:else}
    <div class="row-actions">
      {#if account.state === 'connected' || (account.state === 'unknown' && account.login)}
        {#if account.missing_scopes.length > 0}
          <button type="button" class="sg-button" disabled={busy} onclick={signIn}>Sign in again</button>
        {/if}
        <button type="button" class="button" disabled={busy} onclick={() => (confirmSignOut = true)}>Sign out</button>
      {:else if account.state !== 'not_configured'}
        <button type="button" class="sg-button" disabled={busy} onclick={signIn}
          >{account.state === 'needs_sign_in' ? 'Sign in again' : 'Sign in with Twitch'}</button
        >
      {/if}
    </div>
  {/if}
  <p class="saved" role="status">{notice}</p>
  {#if error}
    <p class="error" role="alert">{error}</p>
  {/if}
</section>

{#if confirmSignOut}
  <ConfirmDialog
    title={`Sign out ${account.login ?? ''}?`}
    message={account.role === 'bot'
      ? 'StreamGremlin forgets the bot’s sign-in and tells Twitch to end it. Chat goes out as you until a bot signs in again.'
      : 'StreamGremlin forgets your sign-in and tells Twitch to end it. Chat and Twitch events stop until you sign in again.'}
    confirmLabel="Sign out"
    onconfirm={() => {
      confirmSignOut = false;
      run(() => twitchSignOut(account.role), 'Signed out.');
    }}
    oncancel={() => (confirmSignOut = false)}
  />
{/if}
