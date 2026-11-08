import { describe, expect, it } from 'vitest';
import { accountHealth, chatHealth, obsHealth } from './health';
import type { AccountStatus, OBSStatus } from './api';

const a = (over: Partial<AccountStatus>): AccountStatus => ({ role: 'streamer', state: 'signed_out', scopes: [], missing_scopes: [], since: '', ...over });
const o = (over: Partial<OBSStatus>): OBSStatus => ({ url: '', enabled: true, has_password: false, state: 'off', since: '', ...over });

describe('health', () => {
  it('never shows unknown as connected or off', () => {
    expect(accountHealth(a({ state: 'unknown', login: 'me' }))).toEqual({ tone: 'unknown', text: 'Unknown (signed in as me)' });
    expect(accountHealth(a({ state: 'connected', login: 'me' })).tone).toBe('ok');
    expect(accountHealth(a({ state: 'needs_sign_in', detail: 'Ended.' })).text).toBe('Needs signing in again: Ended');
    expect(chatHealth({ state: 'disconnected', since: '', detail: 'dropped' })).toEqual({ tone: 'problem', text: 'Lost: dropped. Trying again' });
    expect(obsHealth(o({ state: 'disconnected' })).tone).toBe('unknown');
    expect(obsHealth(o({ state: 'connected', scene: 'Live' })).text).toBe('Connected, on Live');
    expect(obsHealth(o({ state: 'needs_action', detail: 'Kicked.' })).tone).toBe('problem');
  });
});
