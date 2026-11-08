<script lang="ts">
  // OBS's scenes, live from OBS: switch, switch for a moment and come back,
  // and show or hide each scene's sources. "Come back" switches back only if
  // nobody changed the scene in OBS meanwhile; the streamer's choice wins.
  import {
    ApiError,
    getOBSScenes,
    getSceneItems,
    getSnapshots,
    releaseSnapshot,
    restoreSnapshot,
    setSourceVisible,
    switchScene,
    takeSnapshot,
    type SceneItem,
    type Snapshot,
  } from '../lib/api';

  let { current }: { current: string } = $props();

  const key = 'app'; // the app's own "come back" snapshot

  let scenes = $state<string[]>([]);
  let open = $state<Snapshot[]>([]);
  let expanded = $state('');
  let items = $state<SceneItem[]>([]);
  let busy = $state(false);
  let error = $state('');
  let notice = $state('');

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));

  async function load() {
    try {
      scenes = (await getOBSScenes()).scenes;
      open = (await getSnapshots()).snapshots;
      if (expanded) items = (await getSceneItems(expanded)).items;
    } catch (e) {
      error = message(e);
    }
  }

  // Reload when OBS's scene changes, whoever changed it.
  $effect(() => {
    void current;
    load();
  });

  async function act(f: () => Promise<unknown>, done = '') {
    busy = true;
    error = '';
    notice = '';
    try {
      await f();
      if (done) notice = done; // f may have set its own
    } catch (e) {
      error = message(e);
    } finally {
      busy = false;
      await load();
    }
  }

  async function toggleSources(scene: string) {
    if (expanded === scene) {
      expanded = '';
      return;
    }
    expanded = scene;
    items = [];
    await load();
  }

  const restore = (s: Snapshot) =>
    act(async () => {
      const r = await restoreSnapshot(s.key);
      notice = r.status === 'restored' ? `Switched back to ${r.previous_scene}.` : `Stayed: ${r.message}`;
    });
</script>

<section class="account" aria-labelledby="obs-scenes-heading">
  <h3 id="obs-scenes-heading">Scenes</h3>
  {#each open as s (s.id)}
    <div class="code-box">
      <p>On <strong>{s.target_scene}</strong> for now; it can go back to <strong>{s.previous_scene}</strong>.</p>
      {#if s.would_restore === false}
        <p class="sg-muted">{s.message}</p>
      {/if}
      <div class="row-actions">
        <button type="button" class="sg-button" disabled={busy} onclick={() => restore(s)}
          >Switch back to {s.previous_scene}</button
        >
        <button type="button" class="button" disabled={busy} onclick={() => act(() => releaseSnapshot(s.key), 'Staying here.')}
          >Stay here</button
        >
      </div>
    </div>
  {/each}

  <ul class="secret-list" aria-label="OBS scenes">
    {#each scenes as scene (scene)}
      <li class="secret-row">
        <span class="secret-info">
          <strong>{scene}</strong>
          {#if scene === current}<span class="tag">Live</span>{/if}
        </span>
        {#if scene !== current}
          <button type="button" class="button" disabled={busy} onclick={() => act(() => switchScene(scene))}>Switch</button>
          {#if !open.some((s) => s.key === key)}
            <button
              type="button"
              class="button"
              disabled={busy}
              title="Switch now, and offer to come back to the current scene"
              onclick={() => act(() => takeSnapshot(key, scene))}>Switch for now</button
            >
          {/if}
        {/if}
        <button
          type="button"
          class="button"
          aria-expanded={expanded === scene}
          aria-label={`Sources in ${scene}`}
          onclick={() => toggleSources(scene)}>Sources</button
        >
        {#if expanded === scene}
          <ul class="source-list">
            {#each items as it (it.id)}
              <li>
                <span>{it.source}</span>
                <span class="sg-muted">{it.visible ? 'shown' : 'hidden'}</span>
                <button
                  type="button"
                  class="button"
                  disabled={busy}
                  aria-label={`${it.visible ? 'Hide' : 'Show'} ${it.source} in ${scene}`}
                  onclick={() => act(() => setSourceVisible(scene, it.source, !it.visible))}
                  >{it.visible ? 'Hide' : 'Show'}</button
                >
              </li>
            {/each}
          </ul>
        {/if}
      </li>
    {/each}
  </ul>
  <p class="saved" role="status">{notice}</p>
  {#if error}
    <p class="error" role="alert">{error}</p>
  {/if}
</section>
