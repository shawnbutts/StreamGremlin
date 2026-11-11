<script lang="ts">
  // A Lua script, written in its step, with Try it (docs/adr/0037-lua-scripts.md).
  // The script is literal: what's typed is what runs. Values reach it
  // through "Values to pass", as sg.args.
  import { ApiError, tryLua, type ActionParam, type LuaTried } from '../lib/api';
  import { toDisplay, type StepDef } from '../lib/editor';
  import { allValues, editorEnv } from '../lib/editorEnv';

  let { param, step = $bindable() }: { param: ActionParam; step: StepDef } = $props();

  const env = editorEnv();
  const named = $derived(allValues(env));
  const id = `lua-${Math.random().toString(36).slice(2)}`;
  const script = $derived(typeof step.with?.[param.name] === 'string' ? (step.with[param.name] as string) : '');
  const max = $derived(param.max_length ?? 64 * 1024);
  const bytes = $derived(new TextEncoder().encode(script).length);
  const computed = $derived(script.includes('${'));

  let tried = $state<LuaTried | undefined>();
  let failed = $state('');
  let trying = $state(false);

  function set(v: string) {
    step.with ??= {};
    if (v === '') delete step.with[param.name];
    else step.with[param.name] = v;
  }

  // The step's values as the script would get them; one filled in from the
  // event ({viewer}) is sent as written, as there's no event when trying.
  function argsForTry(): { args?: Record<string, unknown>; asWritten: boolean } {
    const a = step.with?.args;
    if (!a || typeof a !== 'object' || Array.isArray(a)) return { asWritten: false };
    let asWritten = false;
    const args: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(a as Record<string, unknown>)) {
      if (typeof v === 'string' && v.includes('${')) {
        asWritten = true;
        args[k] = toDisplay(v, named);
      } else {
        args[k] = v;
      }
    }
    return { args, asWritten };
  }

  let asWritten = $state(false);

  async function tryIt() {
    trying = true;
    failed = '';
    tried = undefined;
    const { args, asWritten: aw } = argsForTry();
    asWritten = aw;
    const limit = step.with?.time_limit;
    try {
      tried = await tryLua(script, args, typeof limit === 'number' ? limit : undefined);
    } catch (e) {
      failed = e instanceof ApiError ? e.message : String(e);
    } finally {
      trying = false;
    }
  }

  const shown = (v: unknown) => (v === null || v === undefined ? 'nothing' : JSON.stringify(v, null, 2));
</script>

<div class="lua">
  <label class="lua-label" for={id}>{param.label}</label>
  <textarea
    {id}
    class="sg-input code-input lua-script"
    rows="12"
    spellcheck="false"
    autocapitalize="off"
    autocomplete="off"
    aria-describedby={`${id}-hint`}
    aria-invalid={computed || bytes > max}
    value={script}
    oninput={(e) => set(e.currentTarget.value)}
  ></textarea>
  <p id={`${id}-hint`} class="sg-muted field-hint">
    Read values with <code>sg.args.name</code> and the event with <code>sg.event</code> (like <code>sg.event.actor.display_name</code>).
    Write a line to the run's trace with <code>sg.log(…)</code> or <code>print(…)</code>. What you <code>return</code> is this step's result: later steps
    read it as <code>{'{steps.<name>.output.result}'}</code> (name this step under its More options). The script can't reach chat, OBS, files or the internet.
  </p>
  {#if computed}
    <p class="error">A script is written out: it can't contain <code>${'{'}</code>. Pass values with “Values to pass” instead.</p>
  {/if}
  {#if bytes > max * 0.9}
    <p class={bytes > max ? 'error' : 'sg-muted field-hint'}>{Math.ceil(bytes / 1024)} of {Math.floor(max / 1024)} KiB</p>
  {/if}
  <div class="lua-try">
    <button type="button" class="button" disabled={!script.trim() || trying || computed || bytes > max} onclick={tryIt}>
      {trying ? 'Trying…' : 'Try it'}
    </button>
    <span class="sg-muted field-hint">Runs it once now, with a made-up test chat message from TestViewer. Nothing reaches your chat.</span>
  </div>
  <div class="lua-tried" aria-live="polite">
    {#if failed}
      <p class="error">Couldn't try it: {failed}</p>
    {:else if tried}
      {#if tried.ok}
        <p><strong>Result</strong> <span class="sg-muted">({tried.took_ms} ms)</span></p>
        <pre class="lua-out">{shown(tried.result)}</pre>
      {:else}
        <p class="error"><strong>The script failed:</strong> {tried.error}</p>
      {/if}
      {#if asWritten}
        <p class="sg-muted field-hint">Values filled in from the event were sent as written; in a run they're filled in.</p>
      {/if}
      {#if tried.log.length}
        <p><strong>Log</strong></p>
        <pre class="lua-out">{tried.log.join('\n')}{tried.log_dropped ? `\n… ${tried.log_dropped} more lines not kept` : ''}</pre>
      {/if}
    {/if}
  </div>
</div>

<style>
  .lua {
    display: grid;
    gap: 0.4rem;
  }
  .lua-label {
    font-weight: 600;
  }
  .lua-script {
    width: 100%;
    min-height: 12em;
    tab-size: 2;
    white-space: pre;
  }
  .lua-try {
    display: flex;
    gap: 0.75rem;
    align-items: center;
    flex-wrap: wrap;
  }
  .lua-out {
    margin: 0;
    padding: 0.5rem 0.75rem;
    max-height: 16em;
    overflow: auto;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    background: var(--sg-surface-raised);
    border: 1px solid var(--sg-border);
    border-radius: 6px;
  }
</style>
