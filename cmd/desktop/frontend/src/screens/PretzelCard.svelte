<script lang="ts">
  // Pretzel, the music app (D-090): what it's playing on your stream, read
  // by your Twitch account, for a Now playing overlay and !song. There's
  // nothing to set up, and Pretzel is asked only when something needs it.
  import { onMount } from 'svelte';
  import { eventTime } from '../lib/events';
  import { ApiError, checkPretzel, getPretzel, type PretzelSong } from '../lib/api';

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));

  let song = $state<PretzelSong | undefined>();
  let problem = $state('');
  let busy = $state(false);

  async function load() {
    try {
      song = (await getPretzel()).song;
      problem = '';
    } catch (e) {
      problem = message(e);
    }
  }

  async function check() {
    busy = true;
    try {
      song = await checkPretzel();
      problem = '';
    } catch (e) {
      problem = message(e);
    } finally {
      busy = false;
    }
  }

  const checked = $derived(song && !song.checked_at.startsWith('0001') ? eventTime(song.checked_at) : '');

  onMount(load);
</script>

<section class="sg-panel card" aria-labelledby="pretzel-heading" aria-busy={busy}>
  <h2 id="pretzel-heading">Pretzel</h2>
  <p class="sg-muted">
    Show the song Pretzel is playing on a Now playing overlay (add one on Overlays), and answer !song in chat (the Song template).
    StreamGremlin finds Pretzel by your Twitch account, so there's nothing to set up. It asks Pretzel only while a Now playing overlay is
    open, when !song is used, or when you check here.
  </p>

  <div aria-live="polite">
    {#if problem}
      <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {problem}</p>
    {:else if song?.state === 'playing'}
      <p class="state">
        <span class="state-ok"><span aria-hidden="true">●</span></span> Playing <strong>{song.title}</strong>{#if song.artist}
          by <strong>{song.artist}</strong>{/if}.
        {#if checked}<span class="sg-muted">Checked {checked}.</span>{/if}
      </p>
    {:else if song?.state === 'other'}
      <p class="state"><span class="state-unknown"><span aria-hidden="true">○</span></span> Pretzel says: “{song.text}”</p>
    {:else if song && checked}
      <p class="state">
        <span class="state-unknown"><span aria-hidden="true">?</span></span> Unknown: {song.why}.
        <span class="sg-muted">Checked {checked}.</span>
      </p>
    {:else if song}
      <p class="sg-muted">Not checked yet.</p>
    {/if}
  </div>

  <p class="sg-muted">
    Pretzel requires its own chat messages naming each song unless you have Premium or Pro. StreamGremlin's !song and overlay don't
    replace them, and it never hides them.
  </p>
  <div class="row-actions">
    <button type="button" class="sg-button" disabled={busy} onclick={check}>Check what's playing</button>
  </div>
</section>
