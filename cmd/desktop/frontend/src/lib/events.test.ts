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

describe('Twitch channel events in words', () => {
  const fan = { platform: 'twitch', id: '1', display_name: 'Fan' };
  const of = (type: string, payload: Record<string, unknown>, actor?: EventRecord['actor']) => eventSummary({ ...ev(payload, actor), type });
  it('says who did what', () => {
    expect(of('audience.follow.created', {}, fan)).toBe('Fan followed');
    expect(of('monetization.subscription.started', { tier: 2, is_gift: true }, fan)).toBe('Fan subscribed (a gift), tier 2');
    expect(of('monetization.subscription.renewed', { months: 14, text: 'yay' }, fan)).toBe('Fan resubscribed: 14 months. “yay”');
    expect(of('monetization.subscription.gifted', { total: 1, from: 'Anonymous' })).toBe('Anonymous gifted 1 sub');
    expect(of('monetization.bits.cheered', { bits: 500, text: 'Cheer500', from: 'Fan' }, fan)).toBe('Fan cheered 500 bits: Cheer500');
    expect(of('audience.raid.received', { viewers: 42 }, fan)).toBe('Fan raided with 42 viewers');
    expect(of('rewards.channel_points.redeemed', { reward: { title: 'Hydrate' }, user_input: '' }, fan)).toBe('Fan redeemed Hydrate');
    expect(of('broadcast.channel.online', {})).toBe('Live on Twitch');
    expect(of('chat.first_words.received', { text: 'hi all' }, fan)).toBe('Fan’s first words this stream: hi all');
    expect(of('broadcast.channel.updated', { title: 'Night', category: 'Celeste' })).toBe('“Night”, in Celeste');
  });
});

describe('eventTime', () => {
  it('shows only the time today, and the date otherwise', () => {
    const now = new Date('2026-11-08T21:00:00Z');
    expect(eventTime('2026-11-08T20:00:00.000Z', now)).not.toMatch(/Nov/);
    expect(eventTime('2026-11-01T20:00:00.000Z', now)).toMatch(/Nov/);
  });
});
