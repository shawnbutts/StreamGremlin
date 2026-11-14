import { describe, expect, it } from 'vitest';
import type { CommandEntry } from './api';
import { describeTrigger, forWhom, onOffWords, overlaysIn, takenBy, testFields, testSource, whoPreset } from './workflows';

const table: CommandEntry[] = [
  {
    command: 'so',
    conflict: false,
    owners: [
      { owner: 'mods', name: 'For mods', enabled: true, roles: ['moderator', 'broadcaster'] },
      { owner: 'tw', name: 'Twitch only', enabled: true, sources: ['twitch'] },
      { owner: 'off', name: 'Off one', enabled: false },
    ],
  },
];

describe('commands', () => {
  it('names what already answers a command for chatters and chats that meet', () => {
    expect(takenBy('SO', undefined, undefined, table, 'new')).toEqual(['For mods', 'Twitch only']);
    expect(takenBy('so', ['viewer'], undefined, table, 'new')).toEqual(['Twitch only']);
    expect(takenBy('so', ['viewer'], ['youtube'], table, 'new')).toEqual([]);
    expect(takenBy('so', 'moderator', ['youtube'], table, 'new')).toEqual(['For mods']);
    expect(takenBy('so', undefined, undefined, table, 'mods')).toEqual(['Twitch only']); // never itself
    expect(takenBy('hug', undefined, undefined, table, 'new')).toEqual([]);
  });

  it('says who in words', () => {
    expect(forWhom(['moderator', 'broadcaster'])).toBe(' for mods and you');
    expect(forWhom(['vip'])).toBe(' for vip');
    expect(forWhom(undefined)).toBe('');
    expect(whoPreset(undefined)).toBe('everyone');
    expect(whoPreset(['broadcaster', 'moderator'])).toBe('mods');
    expect(whoPreset(['vip', 'viewer', 'subscriber'])).toBe('not-mods');
    expect(forWhom(['viewer', 'subscriber', 'vip'])).toBe(' for everyone except mods and you');
  });

  it('describes a trigger and builds a matching test event', () => {
    const t = { type: 'chat.command.received', where: { command: 'hi', 'chatter.role': ['moderator', 'broadcaster'] } };
    expect(describeTrigger(t, [])).toBe('chat.command.received !hi (mods and you)');
    const kind = {
      type: 'chat.command.received', label: 'Chat command', description: '', sources: [], sensitive: true,
      fields: [
        { name: 'command', label: 'Command', kind: 'text' as const, default: '!hello', max: 50 },
        { name: 'role', label: 'Role', kind: 'choice' as const, default: 'viewer', max: 11, path: 'chatter.role' },
      ],
    };
    expect(testFields(t, kind)).toEqual({ command: '!hi', role: 'moderator' });
    expect(testFields({ type: t.type, where: { 'chatter.badges': 'x' } }, kind)).toBeUndefined();
  });

  it('makes a test event from the chat a trigger hears (D-098)', () => {
    const kind = { type: 'chat.command.received', label: 'Chat command', description: '', sources: ['twitch', 'youtube'], sensitive: true, fields: [] };
    expect(testSource({ type: kind.type }, kind)).toBeUndefined();
    expect(testSource({ type: kind.type, sources: ['twitch'] }, kind)).toBeUndefined();
    expect(testSource({ type: kind.type, sources: ['youtube'] }, kind)).toBe('youtube');
    expect(testFields({ type: kind.type, sources: ['youtube'] }, kind)).toEqual({});
    expect(testFields({ type: kind.type, sources: ['kick'] }, kind)).toBeUndefined();
  });
});

describe('overlaysIn', () => {
  it('finds the overlays a workflow shows alerts on, anywhere, but not computed ones', () => {
    const def = {
      steps: [
        { action: 'overlays.alert', with: { overlay: 'alerts' } },
        { if: 'x', do: [{ action: 'overlays.alert', with: { overlay: 'corner' } }], else: [{ action: 'chat.send', with: { overlay: 'no' } }] },
        { choose: [{ weight: 1, steps: [{ action: 'overlays.alert', with: { overlay: 'alerts' } }] }] },
        { action: 'overlays.alert', with: { overlay: '${vars.where}' } },
      ],
    };
    expect(overlaysIn(def)).toEqual(['alerts', 'corner']);
    expect(overlaysIn({ steps: [] })).toEqual([]);
  });
});

describe('onOffWords', () => {
  it('says whether a saved workflow is on, and why not', () => {
    expect(onOffWords({ enabled: true })).toBe('It’s on.');
    expect(onOffWords({ enabled: true, group: 'Fun', group_enabled: false })).toBe('It’s on, but its group “Fun” is off.');
    expect(onOffWords({ enabled: false }, '!hello is already answered by “Hi”.')).toBe('It’s off: !hello is already answered by “Hi”.');
    expect(onOffWords({ enabled: false })).toBe('It’s off until you turn it on.');
  });
});
