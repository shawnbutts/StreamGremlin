// What every part of the workflow editor needs to know, shared through
// Svelte context so nested step lists don't pass it down by hand.
import { getContext, setContext } from 'svelte';
import type { ActionInfo, CommandEntry, DiscordWebhook, LiveButton, Overlay, PaletteValue, Problem, Reward, Sound, TimerStatus, TriggerEntry, WorkflowSummary } from './api';

export interface EditorEnv {
  triggers: TriggerEntry[];
  actions: ActionInfo[];
  /** Values the workflow's triggers offer, as {tokens}. */
  values: PaletteValue[];
  /** Saved variables seen so far, offered as {saved.<scope>.<name>}. */
  saved: PaletteValue[];
  workflows: WorkflowSummary[];
  /** Timers, for "A timer goes off" and "Turn a timer on or off". */
  timers: TimerStatus[];
  /** Buttons, for "You press a button". */
  buttons: LiveButton[];
  /** The sound library, for "Play a sound". */
  sounds: Sound[];
  /** Overlays, for "Show an alert". */
  overlays: Overlay[];
  /** Discord webhooks, for "Post to Discord". */
  discord: DiscordWebhook[];
  /** Channel point rewards, for "Change a reward". */
  rewards: Reward[];
  selfId: string;
  scenes: string[];
  sources: string[];
  problems: Problem[];
  /** Chat commands and what answers them, to warn before one is taken. */
  commands: CommandEntry[];
  /** Show rarely used triggers and steps. */
  more: boolean;
}

const key = Symbol('editor-env');

export const setEditorEnv = (env: EditorEnv) => setContext(key, env);
export const editorEnv = () => getContext<EditorEnv>(key);

/** Every value a message can include: the trigger's, then saved variables. */
export const allValues = (env: EditorEnv) => [...env.values, ...env.saved];
