<script lang="ts">
  // Mounts one parameter's control inside an editor context, for tests.
  // The step lives here as state; holder.step follows its changes.
  import type { ActionParam } from '../lib/api';
  import type { StepDef } from '../lib/editor';
  import { setEditorEnv, type EditorEnv } from '../lib/editorEnv';
  import ParamField from '../editor/ParamField.svelte';

  let { param, holder, env = {} }: { param: ActionParam; holder: { step: StepDef }; env?: Partial<EditorEnv> } = $props();
  // svelte-ignore state_referenced_locally
  setEditorEnv({
    triggers: [], actions: [], values: [], saved: [], workflows: [], timers: [], buttons: [], sounds: [], overlays: [], discord: [], rewards: [],
    selfId: 'wf', scenes: [], sources: [], problems: [], commands: [], more: false, ...env,
  });
  // svelte-ignore state_referenced_locally
  let step = $state(holder.step);
  $effect(() => {
    holder.step = $state.snapshot(step) as StepDef;
  });
</script>

<ParamField {param} bind:step />
