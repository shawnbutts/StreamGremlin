import { describe, expect, it } from 'vitest';
import type { CommandEntry } from './api';
import { describeTrigger, forWhom, takenBy, testFields, whoPreset } from './workflows';

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
});
