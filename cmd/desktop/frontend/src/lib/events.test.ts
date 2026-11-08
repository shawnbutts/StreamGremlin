import { describe, expect, it } from 'vitest';
import type { EventRecord } from './api';
import { eventSummary, eventTime } from './events';

const ev = (payload: Record<string, unknown>, actor?: EventRecord['actor']): EventRecord => ({
  seq: 1,
  event_id: 'e',
  type: 'x.y.z',
  schema_version: 1,
  source: 'twitch',
  occurred_at: '2026-11-08T20:00:00.000Z',
  received_at: '2026-11-08T20:00:00.000Z',
  payload,
  actor,
  delivery_quality: 'confirmed',
});

describe('eventSummary', () => {
  it('says who said what', () => {
    expect(eventSummary(ev({ text: 'hi' }, { platform: 'twitch', id: '1', display_name: 'Fan' }))).toBe('Fan: hi');
  });
  it('describes a scene change', () => {
    expect(eventSummary(ev({ scene_name: 'BRB', previous_scene_name: 'Game' }))).toBe('Now on BRB (was Game)');
  });
  it('falls back to the actor, or nothing', () => {
    expect(eventSummary(ev({}, { platform: 'twitch', id: '1', login: 'fan' }))).toBe('fan');
    expect(eventSummary(ev({}))).toBe('');
  });
});

describe('eventTime', () => {
  it('shows only the time today, and the date otherwise', () => {
    const now = new Date('2026-11-08T21:00:00Z');
    expect(eventTime('2026-11-08T20:00:00.000Z', now)).not.toMatch(/Nov/);
    expect(eventTime('2026-11-01T20:00:00.000Z', now)).toMatch(/Nov/);
  });
});
