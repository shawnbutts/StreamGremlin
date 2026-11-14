<script lang="ts">
  // One When card: the event, chosen from the palette in words, and what
  // it's limited to. Matches the palette doesn't describe are kept and
  // listed under "Also must match".
  import type { Trigger } from '../lib/api';
  import { ownProblems } from '../lib/editor';
  import { editorEnv } from '../lib/editorEnv';
  import { PLATFORM_TRIGGERS, takenBy, whoPreset, whoPresets } from '../lib/workflows';

  let {
    trigger = $bindable(),
    index,
    removable,
    onremove,
  }: { trigger: Trigger; index: number; removable: boolean; onremove: () => void } = $props();

  const env = editorEnv();
  const entry = $derived(env.triggers.find((t) => t.type === trigger.type));
  // StreamGremlin's own events first, then each add-on's, marked as one (D-073).
  const categories = $derived([
    ...new Set([...env.triggers.filter((t) => !t.add_on), ...env.triggers.filter((t) => t.add_on)].map((t) => t.category)),
  ]);
  const addOnCategory = (c: string) => env.triggers.some((t) => t.category === c && t.add_on);
  const known = $derived(new Set(entry?.match.map((m) => m.path) ?? []));
  const other = $derived(Object.entries(trigger.where ?? {}).filter(([k]) => !known.has(k)));
  const path = $derived(`$.triggers[${index}]`);

  function setWhere(key: string, v: unknown) {
    const w = { ...(trigger.where ?? {}) };
    if (v === undefined || v === '') delete w[key];
    else w[key] = v;
    trigger.where = w;
  }

  function changeType(type: string) {
    // Keep only the matches the new event also has.
    const next = env.triggers.find((t) => t.type === type);
    const keep = Object.fromEntries(Object.entries(trigger.where ?? {}).filter(([k]) => next?.match.some((m) => m.path === k)));
    trigger.type = type;
    trigger.where = keep;
  }

  // Which chat it hears (D-098): any, Twitch or YouTube; kept as the trigger's sources.
  const platform = $derived(
    !trigger.sources?.length ? 'any' : trigger.sources.length === 1 && ['twitch', 'youtube'].includes(trigger.sources[0]) ? trigger.sources[0] : 'custom',
  );
  function setPlatform(v: string) {
    if (v === 'custom') return;
    trigger.sources = v === 'any' ? undefined : [v];
  }

  const notForTests = $derived(!!trigger.delivery_quality?.length && !trigger.delivery_quality.includes('synthetic'));
</script>

<li class="sg-panel step-card">
  <div class="step-head">
    <label class="field trigger-pick">
      <span>When</span>
      <select class="sg-input" value={trigger.type} onchange={(e) => changeType(e.currentTarget.value)}>
        {#each categories as c (c)}
          <optgroup label={addOnCategory(c) ? `${c} (add-on)` : c}>
            {#each env.triggers.filter((t) => t.category === c && (env.more || !t.more || t.type === trigger.type)) as t (t.type)}
              <option value={t.type}>{t.add_on ? `${t.label} (add-on)` : t.label}</option>
            {/each}
          </optgroup>
        {/each}
        {#if !entry}<option value={trigger.type}>{trigger.type}</option>{/if}
      </select>
    </label>
    {#if removable}
      <button type="button" class="button button-danger" aria-label={`Remove trigger ${index + 1}`} onclick={onremove}>Remove</button>
    {/if}
  </div>
  {#if entry}
    <p class="sg-muted field-hint">{entry.help}</p>
    {#if PLATFORM_TRIGGERS.includes(trigger.type) || trigger.sources?.length}
      <label class="field">
        <span>Which chat</span>
        <select class="sg-input" value={platform} onchange={(e) => setPlatform(e.currentTarget.value)}>
          <option value="any">Twitch and YouTube</option>
          <option value="twitch">Twitch only</option>
          <option value="youtube">YouTube only</option>
          {#if platform === 'custom'}<option value="custom">Only: {trigger.sources?.join(', ')}</option>{/if}
        </select>
      </label>
    {/if}
    {#each entry.match as m (m.path)}
      {@const v = trigger.where?.[m.path]}
      {#if m.kind === 'command'}
        <label class="field">
          <span>{m.label}</span>
          <span class="command-input">
            <span aria-hidden="true">!</span>
            <input
              class="sg-input"
              value={typeof v === 'string' ? v : ''}
              spellcheck="false"
              onchange={(e) => setWhere(m.path, e.currentTarget.value.trim().replace(/^!+/, '').toLowerCase() || undefined)}
            />
          </span>
          {#if m.help}<span class="sg-muted field-hint">{m.help}</span>{/if}
          {#if typeof v === 'string' && v}
            {@const by = takenBy(v, trigger.where?.['chatter.role'], trigger.sources, env.commands, env.selfId)}
            {#if by.length}
              <span class="field-hint taken" role="status">
                !{v} is already answered by {by.map((n) => `“${n}”`).join(' and ')}. You can save this, but it can only be turned on
                once that’s off, or when “Who can use it” never overlaps: one answer per command.
              </span>
            {/if}
          {/if}
        </label>
      {:else if m.kind === 'who'}
        {@const preset = whoPreset(v)}
        <label class="field">
          <span>{m.label}</span>
          <select
            class="sg-input"
            value={preset ?? 'custom'}
            onchange={(e) => {
              const id = e.currentTarget.value;
              if (id === 'custom') return;
              setWhere(m.path, id === 'everyone' ? undefined : whoPresets.find((p) => p.id === id)?.roles);
            }}
          >
            <option value="everyone">Everyone</option>
            {#each whoPresets as p (p.id)}<option value={p.id}>{p.label}</option>{/each}
            {#if !preset}<option value="custom">Only: {(Array.isArray(v) ? v : [v]).join(', ')}</option>{/if}
          </select>
        </label>
      {:else if m.kind === 'timer'}
        <label class="field">
          <span>{m.label}</span>
          <select class="sg-input" value={typeof v === 'string' ? v : ''} onchange={(e) => setWhere(m.path, e.currentTarget.value || undefined)}>
            <option value="">Any timer</option>
            {#each env.timers as t (t.id)}<option value={t.id}>{t.name}{t.enabled ? '' : ' (off)'}</option>{/each}
            {#if typeof v === 'string' && v && !env.timers.some((t) => t.id === v)}<option value={v}>{v} (not found)</option>{/if}
          </select>
          {#if !env.timers.length}<span class="sg-muted field-hint">No timers yet: make one on the Timers screen.</span>{/if}
        </label>
      {:else if m.kind === 'button'}
        <label class="field">
          <span>{m.label}</span>
          <select class="sg-input" value={typeof v === 'string' ? v : ''} onchange={(e) => setWhere(m.path, e.currentTarget.value || undefined)}>
            <option value="">Any button</option>
            {#each env.buttons as b (b.id)}<option value={b.id}>{b.label}</option>{/each}
            {#if typeof v === 'string' && v && !env.buttons.some((b) => b.id === v)}<option value={v}>{v} (not found)</option>{/if}
          </select>
          {#if !env.buttons.length}<span class="sg-muted field-hint">No buttons yet: make one on the Live screen.</span>{/if}
        </label>
      {:else if m.kind === 'on_off'}
        <label class="field">
          <span>{m.label}</span>
          <select class="sg-input" value={v === true ? 'on' : v === false ? 'off' : ''}
            onchange={(e) => setWhere(m.path, e.currentTarget.value === '' ? undefined : e.currentTarget.value === 'on')}>
            <option value="">Either</option>
            <option value="on">On</option>
            <option value="off">Off</option>
          </select>
          {#if m.help}<span class="sg-muted field-hint">{m.help}</span>{/if}
        </label>
      {:else}
        <label class="field">
          <span>{m.label}</span>
          <input
            class="sg-input"
            list={m.kind === 'scene' ? 'editor-scenes' : m.kind === 'source' ? 'editor-sources' : undefined}
            value={typeof v === 'string' ? v : v === undefined ? '' : JSON.stringify(v)}
            onchange={(e) => setWhere(m.path, e.currentTarget.value || undefined)}
          />
          {#if m.help}<span class="sg-muted field-hint">{m.help}</span>{/if}
        </label>
      {/if}
    {/each}
  {:else}
    <p class="sg-muted">An event type the editor doesn’t describe: <code>{trigger.type}</code>. Change it on the JSON tab.</p>
  {/if}
  {#if other.length}
    <div class="also">
      <span>Also must match:</span>
      <ul>
        {#each other as [k, v] (k)}
          <li>
            <code>{k}</code> = <code>{JSON.stringify(v)}</code>
            <button type="button" class="button" aria-label={`Remove the match on ${k}`} onclick={() => setWhere(k, undefined)}>Remove</button>
          </li>
        {/each}
      </ul>
    </div>
  {/if}
  {#if notForTests}<p class="sg-muted">Test events don’t start it.</p>{/if}
  {#each ownProblems(env.problems, path) as pr (pr.path + pr.message)}
    <p class="error">{pr.message} <span class="sg-muted">({pr.path})</span></p>
  {/each}
</li>
