import type { EventRecord } from './api';

/** One line saying who did what, from the parts every reader understands. */
export function eventSummary(ev: EventRecord): string {
  const p = ev.payload ?? {};
  const who = ev.actor?.display_name || ev.actor?.login || '';
  const text = typeof p.text === 'string' ? p.text : '';
  if (text) return who ? `${who}: ${text}` : text;
  if (typeof p.scene_name === 'string') {
    const prev = typeof p.previous_scene_name === 'string' && p.previous_scene_name ? ` (was ${p.previous_scene_name})` : '';
    return `Now on ${p.scene_name}${prev}`;
  }
  return who;
}

/** "20:04:05" today, or "Nov 7, 20:04" on another day. */
export function eventTime(iso: string, now = new Date()): string {
  const d = new Date(iso);
  const time = d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' });
  if (d.toDateString() === now.toDateString()) return time;
  return `${d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })}, ${time}`;
}
