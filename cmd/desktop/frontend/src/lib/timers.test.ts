import { describe, expect, it } from 'vitest';
import type { TimerStatus, WorkflowSummary } from './api';
import { conditionWords, duration, formOf, newForm, nextWords, scheduleWords, starterWorkflow, timerOf, usedBy } from './timers';

const base: TimerStatus = {
  id: 'socials', name: 'Socials', enabled: true, every_seconds: 900, chat_lines: 0, live_only: false, once: false,
  counter: 0, created_at: '', updated_at: '',
};

describe('timers in words', () => {
  it('says durations and schedules', () => {
    expect(duration(600)).toBe('10 min');
    expect(duration(90)).toBe('90 s');
    expect(duration(3600)).toBe('1 hour');
    expect(duration(7200)).toBe('2 hours');
    expect(scheduleWords({ every_seconds: 600 })).toBe('Every 10 min');
    expect(scheduleWords({ every_seconds: 600, up_to_seconds: 900 })).toBe('Every 10 min to 15 min, at random');
    expect(scheduleWords({ cron: '0 20 * * *' })).toBe('On the schedule 0 20 * * *');
    expect(conditionWords({ ...base, chat_lines: 1, live_only: true, once: true })).toEqual([
      'after 1 chat line from viewers', 'only while you’re live', 'once, then it turns itself off',
    ]);
  });

  it('says where the schedule stands', () => {
    const now = new Date('2026-11-08T10:00:00Z');
    expect(nextWords({ ...base, waiting: 'off' }, now)).toBe('Off');
    expect(nextWords({ ...base, waiting: 'chat', lines: 2, chat_lines: 5 }, now)).toBe('Waiting for chat: 2 of 5 lines');
    expect(nextWords({ ...base, waiting: 'never' }, now)).toContain('Never');
    expect(nextWords({ ...base, next_at: '2026-11-08T10:00:42Z' }, now)).toMatch(/^Next in 42 s, at /);
    expect(nextWords({ ...base, next_at: '2026-11-08T10:15:00Z' }, now)).toMatch(/^Next in 15 min/);
    expect(nextWords({ ...base, live_only: true, next_at: '2026-11-08T10:15:00Z' }, now, false)).toContain('unless you’re live');
    expect(nextWords({ ...base, live_only: true, next_at: '2026-11-08T10:15:00Z' }, now, true)).not.toContain('unless');
  });

  it('finds the workflows a timer starts', () => {
    const wf = (id: string, where: Record<string, unknown>, type = 'timers.timer.fired') =>
      ({ id, name: id, enabled: true, version: 1, updated_at: '', triggers: [{ type, where }] }) as WorkflowSummary;
    const list = [wf('a', { timer_id: 'socials' }), wf('b', { timer_id: ['x', 'socials'] }), wf('c', { timer_id: 'other' }),
      wf('d', { timer_id: 'socials' }, 'chat.message.received')];
    expect(usedBy('socials', list).map((w) => w.id)).toEqual(['a', 'b']);
  });
});

describe('the timer form', () => {
  it('round-trips intervals in a unit that reads naturally', () => {
    expect(formOf({ ...base, every_seconds: 600, up_to_seconds: 900 })).toMatchObject({ mode: 'every', every: 10, upTo: 15, unit: 'minutes', random: true });
    expect(formOf({ ...base, every_seconds: 7200 })).toMatchObject({ every: 2, unit: 'hours', random: false });
    expect(formOf({ ...base, every_seconds: 90, up_to_seconds: 120 })).toMatchObject({ every: 90, upTo: 120, unit: 'seconds' });
    expect(formOf({ ...base, every_seconds: undefined, cron: '@daily' })).toMatchObject({ mode: 'cron', cron: '@daily' });
    for (const t of [{ ...base, every_seconds: 600, up_to_seconds: 900 }, { ...base, every_seconds: 45 }]) {
      const got = timerOf('socials', formOf(t)).timer!;
      expect([got.every_seconds, got.up_to_seconds]).toEqual([t.every_seconds, t.up_to_seconds]);
    }
  });

  it('says what is wrong', () => {
    const f = { ...newForm(), name: 'X' };
    expect(timerOf('x', { ...f, name: ' ' }).problem).toContain('name');
    expect(timerOf('x', { ...f, every: 5, unit: 'seconds' }).problem).toContain('10 seconds');
    expect(timerOf('x', { ...f, every: 25, unit: 'hours' }).problem).toContain('24 hours');
    expect(timerOf('x', { ...f, random: true, every: 10, upTo: 10 }).problem).toContain('random');
    expect(timerOf('x', { ...f, mode: 'cron', cron: '0 20 *' }).problem).toContain('five parts');
    expect(timerOf('x', { ...f, lines: 1001 }).problem).toContain('1000');
    expect(timerOf('x', { ...f, mode: 'cron', cron: '@hourly' }).timer).toMatchObject({ cron: '@hourly' });
    expect(timerOf('x', { ...f, random: true, every: 10, upTo: 15 }).timer).toMatchObject({ every_seconds: 600, up_to_seconds: 900 });
  });

  it('starts a workflow for a timer, with its message to fill in', () => {
    const s = starterWorkflow({ id: 'socials', name: 'Socials' });
    expect(s.def.triggers).toEqual([{ type: 'timers.timer.fired', where: { timer_id: 'socials' } }]);
    expect(s.blanks[0].path).toBe('$.steps[0].with.message');
  });
});
