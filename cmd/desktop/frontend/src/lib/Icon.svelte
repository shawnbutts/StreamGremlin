<script lang="ts">
  // One icon, drawn in the text colour: an SVG (Tabler) or a character of
  // the bundled Nerd Fonts symbols font. Decorative: whatever it's on
  // carries the words.
  import type { IconDef } from './icons';

  let { icon, small = false }: { icon: IconDef; small?: boolean } = $props();
</script>

{#if icon.kind === 'svg'}
  <svg
    xmlns="http://www.w3.org/2000/svg"
    width={small ? 18 : 20}
    height={small ? 18 : 20}
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="2"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
    focusable="false"
    >{#each icon.nodes as [tag, attrs], i (i)}<svelte:element this={tag} {...attrs} />{/each}</svg
  >
{:else}
  <span class="nf-glyph" class:nf-small={small} aria-hidden="true">{icon.char}</span>
{/if}

<style>
  @font-face {
    font-family: 'StreamGremlin Nerd Symbols';
    src: url('../assets/nerd/symbols.woff2') format('woff2');
    font-display: block;
  }
  .nf-glyph {
    display: inline-block;
    width: 20px;
    font-family: 'StreamGremlin Nerd Symbols';
    font-size: 20px;
    font-weight: normal;
    font-style: normal;
    line-height: 1;
    text-align: center;
  }
  .nf-small {
    width: 18px;
    font-size: 18px;
  }
</style>
