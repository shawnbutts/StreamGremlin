// Connection states in a few words for Home. Unknown is said as unknown,
// never as connected or as off.
import type { AccountStatus, ChatStatus, OBSStatus } from './api';

export type Tone = 'ok' | 'unknown' | 'problem' | 'off';

export interface Health {
  tone: Tone;
  text: string;
}

const icon: Record<Tone, string> = { ok: '●', unknown: '?', problem: '!', off: '○' };
export const toneIcon = (t: Tone) => icon[t];

const why = (d?: string) => (d ? `: ${d.replace(/\.$/, '')}` : '');

export function accountHealth(a: AccountStatus): Health {
  switch (a.state) {
    case 'connected':
      return { tone: 'ok', text: `Signed in as ${a.login}` };
    case 'unknown':
      return { tone: 'unknown', text: `Unknown${a.login ? ` (signed in as ${a.login})` : ''}${why(a.detail)}` };
    case 'needs_sign_in':
      return { tone: 'problem', text: `Needs signing in again${why(a.detail)}` };
    case 'signing_in':
      return { tone: 'unknown', text: 'Signing in…' };
    default:
      return { tone: 'off', text: 'Not signed in' };
  }
}

export function chatHealth(c: ChatStatus): Health {
  switch (c.state) {
    case 'connected':
      return { tone: 'ok', text: 'Reading your chat' };
    case 'connecting':
      return { tone: 'unknown', text: 'Connecting…' };
    case 'disconnected':
      return { tone: 'problem', text: `Lost${why(c.detail)}. Trying again` };
    default:
      return { tone: 'off', text: `Off${why(c.detail)}` };
  }
}

export function obsHealth(o: OBSStatus): Health {
  switch (o.state) {
    case 'connected':
      return { tone: 'ok', text: `Connected${o.scene ? `, on ${o.scene}` : ''}` };
    case 'connecting':
      return { tone: 'unknown', text: 'Connecting…' };
    case 'disconnected':
      return { tone: 'unknown', text: `Not connected${why(o.detail)}. Trying again` };
    case 'needs_action':
      return { tone: 'problem', text: `Needs you${why(o.detail)}` };
    default:
      return { tone: 'off', text: 'Off' };
  }
}
