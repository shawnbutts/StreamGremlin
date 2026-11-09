import type { EventRecord } from './api';

/** One line saying who did what, from the parts every reader understands. */
export function eventSummary(ev: EventRecord): string {
  const p = ev.payload ?? {};
  const who = ev.actor?.display_name || ev.actor?.login || '';
  const from = typeof p.from === 'string' ? p.from : who;
  const num = (k: string) => (typeof p[k] === 'number' ? (p[k] as number) : 0);
  // Twitch channel events, in words (docs/adr/0015-twitch-channel-events.md).
  switch (ev.type) {
    case 'audience.follow.created':
      return `${who} followed`;
    case 'monetization.subscription.started':
      return `${who} subscribed${p.is_gift ? ' (a gift)' : ''}, tier ${num('tier')}`;
    case 'monetization.subscription.renewed':
      return `${who} resubscribed: ${num('months')} months${typeof p.text === 'string' && p.text ? `. “${p.text}”` : ''}`;
    case 'monetization.subscription.gifted':
      return `${from} gifted ${num('total')} ${num('total') === 1 ? 'sub' : 'subs'}`;
    case 'monetization.bits.cheered':
      return `${from} cheered ${num('bits')} bits${typeof p.text === 'string' && p.text ? `: ${p.text}` : ''}`;
    case 'audience.raid.received':
      return `${who} raided with ${num('viewers')} viewers`;
    case 'rewards.channel_points.redeemed': {
      const r = (p.reward ?? {}) as { title?: string };
      return `${who} redeemed ${r.title ?? 'a reward'}${typeof p.user_input === 'string' && p.user_input ? `: ${p.user_input}` : ''}`;
    }
    case 'chat.poll.started':
      return `Poll started: ${p.title ?? ''}`;
    case 'chat.poll.updated':
      return `Poll: ${num('votes')} ${num('votes') === 1 ? 'vote' : 'votes'}${p.leader ? `, ${p.leader} leads` : ''}`;
    case 'chat.poll.ended':
      return `Poll ended: ${p.leader ? `${p.leader} won` : p.tied ? 'a tie' : 'no votes'} (${num('votes')} ${num('votes') === 1 ? 'vote' : 'votes'})`;
    case 'chat.prediction.started':
      return `Prediction started: ${p.title ?? ''}`;
    case 'chat.prediction.updated':
      return `Prediction: bets on ${p.title ?? ''}`;
    case 'chat.prediction.locked':
      return `Prediction locked: ${p.title ?? ''}`;
    case 'chat.prediction.ended':
      return p.status === 'canceled' ? `Prediction canceled and refunded: ${p.title ?? ''}` : `Prediction resolved: ${p.winner || 'no winner'} won`;
    case 'monetization.hype_train.started':
      return `Hype Train started: level ${num('level')}`;
    case 'monetization.hype_train.updated':
      return `Hype Train level ${num('level')}: ${num('progress')} of ${num('goal')} to the next`;
    case 'monetization.hype_train.ended': {
      const top = (p.top_contributor ?? {}) as { name?: string };
      return `Hype Train ended at level ${num('level')}${top.name ? `; top contributor ${top.name}` : ''}`;
    }
    case 'ads.break.started':
      return `Ad break began: ${num('duration_seconds')} seconds${p.is_automatic ? ' (automatic)' : ''}`;
    case 'ads.break.end_estimated':
      return 'Ad break should be over (estimated)';
    case 'ads.break.requested':
      return `Twitch accepted an ad break request: ${num('length')} seconds`;
    case 'ads.request.refused':
      return `Ad ${p.action === 'snooze' ? 'snooze' : 'request'} refused${typeof p.message === 'string' && p.message ? `: ${p.message}` : ''}`;
    case 'ads.request.unresolved':
      return 'Ad request unclear: it may or may not have started';
    case 'ads.snooze.applied':
      return `Next ad snoozed; ${num('snooze_count')} ${num('snooze_count') === 1 ? 'snooze' : 'snoozes'} left`;
    case 'chat.first_words.received':
      return `${who}’s first words this stream${typeof p.text === 'string' && p.text ? `: ${p.text}` : ''}`;
    case 'broadcast.channel.online':
      return 'Live on Twitch';
    case 'broadcast.channel.offline':
      return 'Offline on Twitch';
    case 'broadcast.channel.updated':
      return `“${p.title ?? ''}”, in ${p.category || 'no category'}`;
  }
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
