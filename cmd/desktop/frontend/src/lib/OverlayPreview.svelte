<script lang="ts">
  // A preview of one overlay: the overlay page itself in a frame, marked as
  // a preview (docs/adr/0017-alert-overlays.md). It shows tests and samples,
  // which OBS never does, and is never counted as OBS.
  import { ApiError, getOverlayLink } from './api';

  let {
    name,
    reload = 0,
    loaded = $bindable(false),
  }: {
    name: string;
    /** Changed to fetch the link again (after a new link). */
    reload?: number;
    /** Whether the frame has its link. */
    loaded?: boolean;
  } = $props();

  let url = $state('');
  let problem = $state('');
  let width = $state(0);

  $effect(() => {
    const n = name;
    void reload;
    url = '';
    problem = '';
    loaded = false;
    let current = true;
    getOverlayLink(n)
      .then((l) => {
        if (!current) return;
        url = `${l.url}?preview=1`;
        loaded = true;
      })
      .catch((e) => {
        if (current) problem = e instanceof ApiError ? e.message : String(e);
      });
    return () => {
      current = false;
    };
  });
</script>

{#if url}
  <div class="overlay-preview" bind:clientWidth={width}>
    <iframe
      title={`Preview of ${name}`}
      src={url}
      width="1920"
      height="1080"
      referrerpolicy="no-referrer"
      sandbox="allow-scripts allow-same-origin"
      style:transform={`scale(${width / 1920 || 0.25})`}
    ></iframe>
  </div>
  <p class="sg-muted field-hint">The preview shows tests and samples; OBS never does.</p>
{:else if problem}
  <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> No preview of {name}: {problem}</p>
{:else}
  <p class="sg-muted">Loading the preview…</p>
{/if}
