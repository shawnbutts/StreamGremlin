import { describe, expect, it } from 'vitest';
import { buttonId, chatLine, pressWords, startsFrom, startsWords, starterWorkflow } from './live';
import type { EventRecord, WorkflowSummary } from './api';

const ev = (payload: Record<string, unknown>, extra: Partial<EventRecord> = {}): EventRecord => ({
  seq: 7,
  event_id: 'e',
  type: 'chat.message.received',
  schema_version: 1,
  source: 'twitch',
  occurred_at: '2026-11-11T20:00:00Z',
  received_at: '2026-11-11T20:00:00Z',
  actor: { platform: 'twitch', id: '42', login: 'fan', display_name: 'Fan' },
  payload,
  delivery_quality: 'confirmed',
  ...extra,
});

const wf = (id: string, name: string, where: Record<string, unknown> | undefined, running = true): WorkflowSummary => ({
  id, name, enabled: running, running, version: 1, updated_at: '', triggers: [{ type: 'buttons.button.pressed', where }],
});

describe('chatLine', () => {
  it('reads a Twitch chat message', () => {
    const l = chatLine(ev({
      message_id: 'm1', text: 'hello', color: '#FF0000',
      chatter: { id: '42', login: 'fan', display_name: 'Fan', badges: [{ set_id: 'moderator' }, { set_id: 'subscriber' }, { set_id: 'odd' }] },
    }));
    expect(l).toMatchObject({ seq: 7, messageId: 'm1', userId: '42', name: 'Fan', text: 'hello', color: '#FF0000',
      badges: ['Moderator', 'Subscriber'], test: false, shared: false });
  });
  it('marks test and shared-chat messages, and ignores odd colours', () => {
    const l = chatLine(ev({ text: 'x', color: 'red; background: url(x)', source_broadcaster_id: '9' }, { delivery_quality: 'synthetic' }));
    expect(l.test).toBe(true);
    expect(l.shared).toBe(true);
    expect(l.color).toBe('');
    expect(l.name).toBe('Fan'); // from the actor
  });
});

describe('buttons', () => {
  it('makes ids from labels', () => {
    expect(buttonId('Be right back!', [])).toBe('be-right-back');
    expect(buttonId('Be right back', ['be-right-back', 'be-right-back-2'])).toBe('be-right-back-3');
    expect(buttonId('🎉🎉', [])).toBe('button');
    expect(buttonId('x'.repeat(100), []).length).toBeLessThanOrEqual(64);
  });
  it('finds the workflows that start from a button', () => {
    const ws = [wf('a', 'A', { button_id: 'brb' }), wf('b', 'B', { button_id: ['brb', 'hype'] }), wf('c', 'C', undefined), wf('d', 'D', { button_id: 'hype' })];
    expect(startsFrom('brb', ws).map((w) => w.id)).toEqual(['a', 'b', 'c']);
    expect(startsWords('end', [])).toBe('No workflow starts from it yet.');
    expect(startsWords('brb', [wf('a', 'A', { button_id: 'brb' }), wf('b', 'B', { button_id: 'brb' }, false)])).toBe('Starts “A”; “B” is off.');
  });
  it('says what a press did', () => {
    expect(pressWords('BRB', [{ name: 'Be right back' }], false)).toBe('Pressed “BRB”: started “Be right back”.');
    expect(pressWords('BRB', [], false)).toBe('Pressed “BRB”. No workflow that’s on starts from it.');
    expect(pressWords('BRB', [], true)).toContain('nothing ran twice');
  });
  it('starts a workflow for a button', () => {
    const s = starterWorkflow({ id: 'brb', label: 'BRB' });
    expect(s.def.triggers).toEqual([{ type: 'buttons.button.pressed', where: { button_id: 'brb' } }]);
    expect(s.blanks[0].path).toBe('$.steps[0].with.message');
  });
});
