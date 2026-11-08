import type { EventKind, Job, Trigger } from './api';

/** "Chat command !hello", from the trigger and the event catalog's labels. */
export function describeTrigger(t: Trigger, kinds: EventKind[]): string {
  const label = kinds.find((k) => k.type === t.type)?.label ?? t.type;
  const where = Object.entries(t.where ?? {})
    .map(([k, v]) => (k === 'command' ? `!${v}` : `${k} = ${JSON.stringify(v)}`))
    .join(', ');
  const parts = [label + (where ? ` ${where}` : '')];
  if (t.sources?.length) parts.push(`from ${t.sources.join(' or ')}`);
  if (t.delivery_quality?.length && !t.delivery_quality.includes('synthetic')) parts.push('(not for test events)');
  return parts.join(' ');
}

/** The fields of a test event that would match the trigger, where they can be told. */
export function testFields(t: Trigger, kind: EventKind | undefined): Record<string, string> | undefined {
  if (!kind) return undefined;
  if (t.delivery_quality?.length && !t.delivery_quality.includes('synthetic')) return undefined;
  const fields: Record<string, string> = {};
  for (const [k, v] of Object.entries(t.where ?? {})) {
    if (!kind.fields.some((f) => f.name === k)) return undefined; // can't be set from the test form
    fields[k] = k === 'command' ? `!${v}` : String(v);
  }
  return fields;
}

const words: Record<string, string> = {
  pending: 'Waiting',
  running: 'Running',
  succeeded: 'Done',
  failed: 'Failed',
  cancelled: 'Cancelled',
  interrupted: 'Interrupted',
  rejected: 'Not run: queue full',
  dropped: 'Not run: dropped',
  superseded: 'Not run: replaced',
};

export const jobWords = (j: Job) => words[j.status] ?? j.status;

export const jobOk = (j: Job) => j.status === 'succeeded' || j.status === 'running' || j.status === 'pending';

export const example = JSON.stringify(
  {
    schema: 'streamgremlin.workflow/v1',
    id: 'chat.hello',
    name: 'Say hello back',
    triggers: [{ type: 'chat.command.received', where: { command: 'hello' } }],
    steps: [{ action: 'chat.send', with: { message: 'Hi ${event.actor.display_name}!' } }],
  },
  null,
  2,
);
