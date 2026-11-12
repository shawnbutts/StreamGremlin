<script lang="ts">
  // One icon from the bundled pack, drawn in the text colour. Decorative:
  // whatever it's on carries the words.
  import type { IconNode } from './icons';

  let { nodes, size = 20 }: { nodes: IconNode[]; size?: number } = $props();
</script>

<svg
  xmlns="http://www.w3.org/2000/svg"
  width={size}
  height={size}
  viewBox="0 0 24 24"
  fill="none"
  stroke="currentColor"
  stroke-width="2"
  stroke-linecap="round"
  stroke-linejoin="round"
  aria-hidden="true"
  focusable="false"
  >{#each nodes as [tag, attrs], i (i)}<svelte:element this={tag} {...attrs} />{/each}</svg
>
