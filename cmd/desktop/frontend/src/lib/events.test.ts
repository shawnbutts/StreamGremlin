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
  it('says YouTube’s events in words (D-099)', () => {
    const yt = (type: string, payload: Record<string, unknown>) => ({
      ...ev(payload, { platform: 'youtube', id: 'UC1', display_name: 'Fan' }),
      type,
      source: 'youtube',
    });
    expect(eventSummary(yt('monetization.super_chat.received', { amount_display: '$5.00', text: 'gg', from: 'Fan' }))).toBe('Fan sent a $5.00 Super Chat: gg');
    expect(eventSummary(yt('monetization.super_sticker.received', { amount_display: '€2.00', sticker: 'A cat', from: 'Fan' }))).toBe('Fan sent a €2.00 Super Sticker: A cat');
    expect(eventSummary(yt('monetization.jewels.gifted', { gift: 'Rose', jewels: 10, count: 3, from: 'Fan' }))).toBe('Fan gifted 3 × Rose (10 Jewels)');
    expect(eventSummary(yt('monetization.subscription.started', { level: 'Gold', is_gift: false, tier: 0 }))).toBe('Fan became a member (Gold)');
    expect(eventSummary(yt('monetization.subscription.started', { level: '', is_gift: true, tier: 0 }))).toBe('Fan became a member, a gift');
    expect(eventSummary(yt('monetization.subscription.renewed', { months: 12, text: 'yay' }))).toBe('Fan: 12 months a member. “yay”');
    expect(eventSummary(yt('monetization.subscription.gifted', { total: 1, from: 'Fan' }))).toBe('Fan gifted 1 membership');
    expect(eventSummary(yt('chat.members_only.changed', { enabled: true }))).toBe('YouTube chat is members-only');
    expect(eventSummary(yt('moderation.user.timed_out', { duration_seconds: 300 }))).toBe('Fan was timed out on YouTube for 300 seconds');
    expect(eventSummary(yt('broadcast.channel.online', { title: 'Friday' }))).toBe('Live on YouTube: “Friday”');
    expect(eventSummary({ ...ev({}), type: 'broadcast.channel.online' })).toBe('Live on Twitch');
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
    expect(of('moderation.suspicious_user.message', { text: 'hi' }, fan)).toBe('Suspicious user Fan: hi');
    expect(of('moderation.suspicious_user.updated', { low_trust_status: 'restricted' }, fan)).toBe('Fan is now restricted');
    expect(of('monetization.subscription.started', { tier: 2, is_gift: true }, fan)).toBe('Fan subscribed (a gift), tier 2');
    expect(of('monetization.subscription.renewed', { months: 14, text: 'yay' }, fan)).toBe('Fan resubscribed: 14 months. “yay”');
    expect(of('monetization.subscription.gifted', { total: 1, from: 'Anonymous' })).toBe('Anonymous gifted 1 sub');
    expect(of('monetization.bits.cheered', { bits: 500, text: 'Cheer500', from: 'Fan' }, fan)).toBe('Fan cheered 500 bits: Cheer500');
    expect(of('audience.raid.received', { viewers: 42 }, fan)).toBe('Fan raided with 42 viewers');
    expect(of('rewards.channel_points.redeemed', { reward: { title: 'Hydrate' }, user_input: '' }, fan)).toBe('Fan redeemed Hydrate');
    expect(of('broadcast.channel.online', {})).toBe('Live on Twitch');
    expect(of('monetization.hype_train.started', { level: 1 })).toBe('Hype Train started: level 1');
    expect(of('chat.poll.ended', { leader: 'Hades', votes: 8 })).toBe('Poll ended: Hades won (8 votes)');
    expect(of('giveaways.giveaway.closed', { title: 'a key', entries: 1 })).toBe('Giveaway entries closed: a key, 1 entry');
    expect(of('ads.break.started', { duration_seconds: 90, is_automatic: true })).toBe('Ad break began: 90 seconds (automatic)');
    expect(of('ads.break.end_estimated', { duration_seconds: 90 })).toBe('Ad break should be over (estimated)');
    expect(of('ads.request.refused', { action: 'snooze', message: 'none left' })).toBe('Ad snooze refused: none left');
    expect(of('chat.poll.ended', { leader: '', tied: true, votes: 2 })).toBe('Poll ended: a tie (2 votes)');
    expect(of('chat.prediction.ended', { status: 'resolved', winner: 'Yes' })).toBe('Prediction resolved: Yes won');
    expect(of('chat.prediction.ended', { status: 'canceled', title: 'Win?' })).toBe('Prediction canceled and refunded: Win?');
    expect(of('monetization.hype_train.updated', { level: 2, progress: 300, goal: 2000 })).toBe('Hype Train level 2: 300 of 2000 to the next');
    expect(of('monetization.hype_train.ended', { level: 3, top_contributor: { name: 'BitsFan' } })).toBe('Hype Train ended at level 3; top contributor BitsFan');
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
