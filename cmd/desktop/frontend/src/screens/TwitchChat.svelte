<script lang="ts">
  // Twitch chat: whether StreamGremlin is reading it, and a box to send a
  // message as the bot or as you. Messages that arrive show on Events.
  import { ApiError, sendChat, type ChatStatus, type TwitchStatus } from '../lib/api';

  let { twitch }: { twitch: TwitchStatus } = $props();

  let text = $state('');
  let as = $state<'' | 'bot' | 'streamer'>('');
  let sending = $state(false);
  let sent = $state('');
  let error = $state('');

  const chat = $derived<ChatStatus>(twitch.chat);
  const botIn = $derived(twitch.bot.state === 'connected');
  const youIn = $derived(twitch.streamer.state === 'connected');
  const length = $derived([...text].length);

  async function send(e: SubmitEvent) {
    e.preventDefault();
    sending = true;
    sent = '';
    error = '';
    try {
      const res = await sendChat(text.trim(), as);
      sent = `Sent as ${res.login}.${res.note ? ' ' + res.note : ''}`;
      text = '';
    } catch (err) {
      // Not retried on purpose: Twitch can't tell a repeat from a new message.
      error = `Not sent: ${err instanceof ApiError ? err.message : String(err)}`;
    } finally {
      sending = false;
    }
  }
</script>

<section class="account" aria-labelledby="twitch-chat-heading">
  <h3 id="twitch-chat-heading">Chat</h3>
  <p class="state">
    {#if chat.state === 'connected'}
      <span class="state-ok"><span aria-hidden="true">●</span></span> Reading chat. Messages and commands show on Events.
    {:else if chat.state === 'connecting'}
      Connecting to chat…
    {:else if chat.state === 'disconnected'}
      <span class="state-unknown"><span aria-hidden="true">?</span></span> Not reading chat right now.
    {:else}
      Not reading chat.
    {/if}
  </p>
  {#if chat.detail}
    <p>{chat.detail}</p>
  {/if}
  {#if chat.state === 'connected' && chat.feeds?.length}
    <h4 id="twitch-events-heading">Events from your channel</h4>
    <ul class="feed-list" aria-labelledby="twitch-events-heading">
      {#each chat.feeds as f (f.type)}
        <li>
          {#if f.ok}
            <span class="icon-ok" aria-hidden="true">✓</span> {f.label}
          {:else}
            <span class="icon-bad" aria-hidden="true">✗</span> {f.label}: <span class="sg-muted">{f.problem}</span>
          {/if}
        </li>
      {/each}
    </ul>
    {#if twitch.channel?.known}
      <p class="sg-muted">Your channel: “{twitch.channel.title}”, in {twitch.channel.category || 'no category'}.</p>
    {/if}
  {/if}
  {#if botIn && chat.bot_moderator === false}
    <p class="sg-muted">
      The bot isn’t a moderator in your channel, so Twitch lets it send only one message a second. To lift that, type
      <code>/mod {twitch.bot.login}</code> in your chat.
    </p>
  {/if}

  {#if youIn}
    <form class="test-form" onsubmit={send}>
      <label class="field">
        <span>Send a message</span>
        <input class="sg-input" bind:value={text} maxlength="500" autocomplete="off" />
      </label>
      <p class="sg-muted field-hint" aria-live="polite">{length}/500</p>
      {#if botIn}
        <fieldset>
          <legend>Send as</legend>
          <label class="choice-inline"><input type="radio" name="send-as" value="" bind:group={as} /> The bot ({twitch.bot.login})</label>
          <label class="choice-inline"><input type="radio" name="send-as" value="streamer" bind:group={as} /> You ({twitch.streamer.login})</label>
        </fieldset>
      {/if}
      <div class="row-actions">
        <button type="submit" class="sg-button" disabled={sending || !text.trim()}>Send to chat</button>
      </div>
    </form>
  {/if}
  <p class="saved" role="status">{sent}</p>
  {#if error}
    <p class="error" role="alert">{error}</p>
  {/if}
</section>
