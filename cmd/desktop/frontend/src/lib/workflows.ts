import type { CommandEntry, EventKind, Job, Trigger } from './api';

/** Who a chatter.role match lets in, in words. */
export function whoWords(v: unknown): string {
  const roles = (Array.isArray(v) ? v : [v]).map(String);
  const preset = whoPresets.find((p) => sameSet(p.roles, roles));
  return preset ? `(${preset.label.toLowerCase()})` : `(${roles.join(', ')})`;
}

const sameSet = (a: string[], b: string[]) => a.length === b.length && a.every((x) => b.includes(x));

/** "Who can use it", as stored in where["chatter.role"]. Everyone is no match at all. */
export const whoPresets: { id: string; label: string; roles: string[] }[] = [
  { id: 'subs', label: 'Subscribers, VIPs, mods and you', roles: ['subscriber', 'vip', 'moderator', 'broadcaster'] },
  { id: 'vips', label: 'VIPs, mods and you', roles: ['vip', 'moderator', 'broadcaster'] },
  { id: 'mods', label: 'Mods and you', roles: ['moderator', 'broadcaster'] },
  { id: 'you', label: 'Only you', roles: ['broadcaster'] },
  // Pairs with "Mods and you": one !command can answer mods one way and
  // everyone else another, since the two never overlap.
  { id: 'not-mods', label: 'Everyone except mods and you', roles: ['viewer', 'subscriber', 'vip'] },
];

/** The preset a stored role match is, or undefined when it's another set. */
export function whoPreset(v: unknown): string | undefined {
  if (v === undefined) return 'everyone';
  const roles = (Array.isArray(v) ? v : [v]).map(String);
  return whoPresets.find((p) => sameSet(p.roles, roles))?.id;
}

/** "Chat command !hello", from the trigger and the event catalog's labels. */
export function describeTrigger(t: Trigger, kinds: EventKind[]): string {
  const label = kinds.find((k) => k.type === t.type)?.label ?? t.type;
  const entries = Object.entries(t.where ?? {});
  const where = entries
    .filter(([k]) => k !== 'chatter.role')
    .map(([k, v]) => (k === 'command' ? `!${v}` : `${k} = ${Array.isArray(v) ? v.map((x) => JSON.stringify(x)).join(' or ') : JSON.stringify(v)}`))
    .join(', ');
  const role = entries.find(([k]) => k === 'chatter.role');
  const parts = [label + (where ? ` ${where}` : '') + (role ? ` ${whoWords(role[1])}` : '')];
  if (t.sources?.length) parts.push(`from ${t.sources.join(' or ')}`);
  if (t.delivery_quality?.length && !t.delivery_quality.includes('synthetic')) parts.push('(not for test events)');
  return parts.join(' ');
}

/** The fields of a test event that would match the trigger, where they can be told. */
export function testFields(t: Trigger, kind: EventKind | undefined): Record<string, string> | undefined {
  if (!kind) return undefined;
  if (t.delivery_quality?.length && !t.delivery_quality.includes('synthetic')) return undefined;
  if (t.sources?.length && !t.sources.some((s) => kind.sources.includes(s))) return undefined;
  const fields: Record<string, string> = {};
  for (const [k, raw] of Object.entries(t.where ?? {})) {
    const f = kind.fields.find((f) => (f.path ?? f.name) === k);
    if (!f) return undefined; // can't be set from the test form
    const v = Array.isArray(raw) ? raw[0] : raw; // any one of a list matches
    fields[f.name] = k === 'command' ? `!${v}` : String(v);
  }
  return fields;
}

/** The platform a trigger's test event comes from: the trigger's own when it names one, else the event's first. */
export function testSource(t: Trigger, kind: EventKind | undefined): string | undefined {
  const s = t.sources?.find((s) => kind?.sources.includes(s));
  return s && s !== kind?.sources[0] ? s : undefined;
}

/** The chat triggers that can hear Twitch, YouTube or both (D-098). */
export const CHAT_TRIGGERS = ['chat.message.received', 'chat.command.received', 'chat.first_words.received'];

/** Every trigger that can come from Twitch, YouTube or both (D-098, D-099). */
export const PLATFORM_TRIGGERS = [
  ...CHAT_TRIGGERS,
  'monetization.subscription.started',
  'monetization.subscription.renewed',
  'monetization.subscription.gifted',
  'broadcast.channel.online',
  'broadcast.channel.offline',
];

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

const meet = (a?: string[], b?: string[]) => !a || !b || a.some((x) => b.includes(x));

/**
 * The workflows that are on and already answer this command for chatters
 * and chats that meet these: the engine refuses turning on a second answer
 * (one answer per command).
 */
export function takenBy(command: string, roles: unknown, sources: string[] | undefined, table: CommandEntry[], selfId: string): string[] {
  const r = roles === undefined ? undefined : (Array.isArray(roles) ? roles : [roles]).map(String);
  const e = table.find((x) => x.command === command.toLowerCase());
  return (e?.owners ?? []).filter((o) => o.enabled && o.owner !== selfId && meet(o.roles, r) && meet(o.sources, sources)).map((o) => o.name);
}

/** "for mods and you", "for vip", or "" for everyone. */
export const forWhom = (roles?: string[]) => (roles?.length ? ` ${whoWords(roles).replace(/^\(/, 'for ').replace(/\)$/, '')}` : '');

/**
 * The overlays a workflow shows alerts on, by name, from its "Show an
 * alert" steps anywhere in it (inside ifs, loops and choices too). A name
 * computed with ${…} can't be known before it runs, so it's left out.
 */
export function overlaysIn(def: unknown): string[] {
  const found = new Set<string>();
  const walk = (v: unknown) => {
    if (Array.isArray(v)) {
      v.forEach(walk);
      return;
    }
    if (!v || typeof v !== 'object') return;
    const o = v as Record<string, unknown>;
    const name = (o.with as Record<string, unknown> | undefined)?.overlay;
    if (o.action === 'overlays.alert' && typeof name === 'string' && name && !name.includes('${')) found.add(name);
    Object.values(o).forEach(walk);
  };
  walk(def);
  return [...found];
}

/** Whether a saved workflow is on, in words: new ones start on (D-077). */
export function onOffWords(w: { enabled: boolean; group?: string; group_enabled?: boolean }, offBecause?: string): string {
  if (!w.enabled) return offBecause ? `It’s off: ${offBecause}` : 'It’s off until you turn it on.';
  if (w.group && w.group_enabled === false) return `It’s on, but its group “${w.group}” is off.`;
  return 'It’s on.';
}
