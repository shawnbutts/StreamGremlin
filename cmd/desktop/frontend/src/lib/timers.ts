// Timers in words, and the form that edits one (docs/adr/0013-timers.md).
import { SCHEMA, type Def } from './editor';
import type { TimerInput, TimerStatus, WorkflowSummary } from './api';

export const TIMER_FIRED = 'timers.timer.fired';

export type Unit = 'seconds' | 'minutes' | 'hours';
const unitSeconds: Record<Unit, number> = { seconds: 1, minutes: 60, hours: 3600 };

/** 600 → "10 min"; 90 → "90 s"; 7200 → "2 hours". */
export function duration(s: number): string {
  if (s % 3600 === 0) return s === 3600 ? '1 hour' : `${s / 3600} hours`;
  if (s % 60 === 0) return `${s / 60} min`;
  return `${s} s`;
}

/** When a timer goes off, in words. */
export function scheduleWords(t: Pick<TimerInput, 'every_seconds' | 'up_to_seconds' | 'cron'>): string {
  if (t.cron) return `On the schedule ${t.cron}`;
  const every = t.every_seconds ?? 0;
  if (t.up_to_seconds) return `Every ${duration(every)} to ${duration(t.up_to_seconds)}, at random`;
  return `Every ${duration(every)}`;
}

/** What else it waits for. */
export function conditionWords(t: TimerInput): string[] {
  const out: string[] = [];
  if (t.chat_lines > 0) out.push(`after ${t.chat_lines} chat ${t.chat_lines === 1 ? 'line' : 'lines'} from viewers`);
  if (t.live_only) out.push('only while you’re live');
  if (t.once) out.push('once, then it turns itself off');
  return out;
}

/** Where its schedule stands. */
export function nextWords(t: TimerStatus, now: Date, live?: boolean): string {
  switch (t.waiting) {
    case 'off':
      return 'Off';
    case 'never':
      return 'Never: the schedule matches no date';
    case 'chat':
      return `Waiting for chat: ${t.lines ?? 0} of ${t.chat_lines} lines`;
  }
  if (!t.next_at) return 'Unknown';
  const at = new Date(t.next_at);
  const secs = Math.max(0, Math.round((at.getTime() - now.getTime()) / 1000));
  const clock = at.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: secs < 3600 ? '2-digit' : undefined });
  const inWords = secs < 60 ? `${secs} s` : secs < 3600 ? `${Math.round(secs / 60)} min` : `${Math.round(secs / 360) / 10} hours`;
  const skip = t.live_only && live !== true ? ' (its turn passes unless you’re live)' : '';
  return `Next in ${inWords}, at ${clock}${skip}`;
}

/** The workflows that start from a timer. */
export function usedBy(id: string, workflows: WorkflowSummary[]): WorkflowSummary[] {
  return workflows.filter((w) =>
    w.triggers.some((t) => {
      if (t.type !== TIMER_FIRED) return false;
      const v = t.where?.timer_id;
      return v === id || (Array.isArray(v) && v.includes(id));
    }),
  );
}

/** The form's fields, in units people use. */
export interface TimerForm {
  name: string;
  enabled: boolean;
  mode: 'every' | 'cron';
  every: number;
  unit: Unit;
  random: boolean;
  upTo: number;
  cron: string;
  lines: number;
  liveOnly: boolean;
  once: boolean;
}

export function newForm(): TimerForm {
  return { name: '', enabled: true, mode: 'every', every: 15, unit: 'minutes', random: false, upTo: 20, cron: '0 20 * * *', lines: 0, liveOnly: true, once: false };
}

const gcd = (a: number, b: number): number => (b === 0 ? a : gcd(b, a % b));

function bestUnit(s: number): Unit {
  if (s % 3600 === 0) return 'hours';
  if (s % 60 === 0) return 'minutes';
  return 'seconds';
}

export function formOf(t: TimerInput): TimerForm {
  const f = newForm();
  f.name = t.name;
  f.enabled = t.enabled;
  f.lines = t.chat_lines;
  f.liveOnly = t.live_only;
  f.once = t.once;
  if (t.cron) {
    f.mode = 'cron';
    f.cron = t.cron;
    return f;
  }
  const every = t.every_seconds ?? 900;
  // One unit for both numbers, so the range reads naturally.
  f.unit = bestUnit(t.up_to_seconds ? gcd(every, t.up_to_seconds) : every);
  f.every = every / unitSeconds[f.unit];
  f.random = !!t.up_to_seconds;
  f.upTo = t.up_to_seconds ? t.up_to_seconds / unitSeconds[f.unit] : f.every + 5;
  f.mode = 'every';
  return f;
}

/** The timer a form describes, or what's wrong with it. */
export function timerOf(id: string, f: TimerForm): { timer?: TimerInput; problem?: string } {
  const base = { id, name: f.name.trim(), enabled: f.enabled, chat_lines: Math.round(f.lines || 0), live_only: f.liveOnly, once: f.once };
  if (!base.name) return { problem: 'Give it a name.' };
  if (base.chat_lines < 0 || base.chat_lines > 1000) return { problem: 'Chat lines are 0 to 1000.' };
  if (f.mode === 'cron') {
    if (f.cron.trim().split(/\s+/).length !== 5 && !f.cron.trim().startsWith('@')) {
      return { problem: 'A schedule has five parts: minute, hour, day of the month, month and day of the week.' };
    }
    return { timer: { ...base, cron: f.cron.trim() } };
  }
  const every = Math.round(f.every * unitSeconds[f.unit]);
  if (!(every >= 10 && every <= 86400)) return { problem: 'A timer goes off every 10 seconds to 24 hours.' };
  if (!f.random) return { timer: { ...base, every_seconds: every } };
  const upTo = Math.round(f.upTo * unitSeconds[f.unit]);
  if (!(upTo > every && upTo <= 86400)) return { problem: 'The random range must end after it starts, and within 24 hours.' };
  return { timer: { ...base, every_seconds: every, up_to_seconds: upTo } };
}

/** A new workflow that starts from the timer and says something in chat. */
export function starterWorkflow(t: { id: string; name: string }): { def: Def; blanks: { path: string; hint: string }[] } {
  return {
    def: {
      schema: SCHEMA,
      id: '',
      name: `${t.name} message`,
      triggers: [{ type: TIMER_FIRED, where: { timer_id: t.id } }],
      steps: [{ action: 'chat.send', with: { message: '' } }],
    },
    blanks: [{ path: '$.steps[0].with.message', hint: 'what to say in chat' }],
  };
}
