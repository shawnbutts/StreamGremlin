import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import OBSCard from './OBSCard.svelte';
import { mockFetch, obsOff, settle } from '../test/helpers';
import type { OBSStatus } from '../lib/api';

let comp: ReturnType<typeof mount> | undefined;

afterEach(() => {
  if (comp) unmount(comp);
  comp = undefined;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
});

const button = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === text);

function start(status: OBSStatus, onchange = () => {}) {
  const target = document.createElement('main');
  document.body.appendChild(target);
  comp = mount(OBSCard, { target, props: { status, onchange } });
  return target;
}

async function noViolations(label: string) {
  for (const theme of ['dark', 'light', 'midnight']) {
    document.documentElement.dataset.theme = theme;
    const r = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
    expect(r.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.html).join(', ')}`), `${label}, ${theme}`).toEqual([]);
  }
}

/** A fake OBS behind the API, with scenes, sources and the app's snapshot. */
function obsEngine() {
  const st = { current: 'Starting Soon', snapshot: undefined as undefined | { target: string; previous: string; manual: boolean } };
  const visible: Record<string, boolean> = { Webcam: true, 'Chat Box': true };
  const calls = mockFetch({
    '/v1/connections/obs': (_m, body) => ({ status: 200, body: { ...obsOff(), ...(body as object), has_password: true, state: 'connecting' } }),
    '/v1/obs/scenes': () => ({ status: 200, body: { current: st.current, scenes: ['Starting Soon', 'Just Chatting', 'Be Right Back'] } }),
    '/v1/obs/scene-items': () => ({
      status: 200,
      body: { items: Object.entries(visible).map(([source, v], i) => ({ id: i + 1, source, visible: v, is_group: false })) },
    }),
    '/v1/obs/scene': (_m, body) => {
      st.current = (body as { scene: string }).scene;
      return { status: 200, body: { scene: st.current } };
    },
    '/v1/obs/scene-items/visibility': (_m, body) => {
      const b = body as { source: string; visible: boolean };
      visible[b.source] = b.visible;
      return { status: 200, body: { visible: b.visible } };
    },
    '/v1/obs/snapshots': (method, body) => {
      if (method === 'POST') {
        const b = body as { scene: string };
        st.snapshot = { target: b.scene, previous: st.current, manual: false };
        st.current = b.scene;
        return { status: 200, body: {} };
      }
      const s = st.snapshot;
      return {
        status: 200,
        body: {
          snapshots: s
            ? [{ id: 's1', key: 'app', previous_scene: s.previous, target_scene: s.target, switched: true, status: 'open', would_restore: !s.manual, message: s.manual ? 'The scene was changed in OBS after the switch, so it was left as it is.' : undefined }]
            : [],
        },
      };
    },
    '/v1/obs/snapshots/app/restore': () => {
      const s = st.snapshot!;
      st.snapshot = undefined;
      if (s.manual) return { status: 200, body: { key: 'app', status: 'skipped', previous_scene: s.previous, message: 'The scene was changed in OBS after the switch, so it was left as it is.' } };
      st.current = s.previous;
      return { status: 200, body: { key: 'app', status: 'restored', previous_scene: s.previous } };
    },
    '/v1/obs/snapshots/app/release': () => {
      st.snapshot = undefined;
      return { status: 200, body: { status: 'released' } };
    },
  });
  return { st, calls, visible };
}

const connected = (scene = 'Starting Soon'): OBSStatus =>
  ({ ...obsOff(), enabled: true, state: 'connected', obs_version: '31.0.0', websocket_version: '5.5.0', scene }) as OBSStatus;

describe('OBS', () => {
  it('explains how to turn on OBS’s server, and connects with a write-only password', async () => {
    const e = obsEngine();
    const changed = vi.fn();
    const el = start(obsOff() as OBSStatus, changed);
    expect(el.textContent).toContain('Tools → WebSocket Server Settings');
    await noViolations('off');
    const pw = el.querySelector<HTMLInputElement>('input[type="password"]')!;
    pw.value = 'hunter2';
    pw.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    button(el, 'Connect')!.click();
    await settle(30);
    const put = e.calls.find((c) => c.path === '/v1/connections/obs');
    expect(put?.body).toEqual({ url: 'ws://127.0.0.1:4455', enabled: true, password: 'hunter2' });
    expect(changed).toHaveBeenCalled();
    expect(pw.value).toBe('');
  });

  it('says what needs doing when OBS refuses', async () => {
    obsEngine();
    const el = start({ ...connected(), state: 'needs_action', detail: 'OBS refused the password. Check it…' } as OBSStatus);
    expect(el.textContent).toContain('Needs your attention.');
    expect(el.textContent).toContain('OBS refused the password');
    expect(el.textContent).not.toContain('Connected to OBS');
  });

  it('lists scenes, switches, and shows or hides sources', async () => {
    const e = obsEngine();
    const el = start(connected());
    await settle(30);
    expect(el.textContent).toContain('Connected to OBS 31.0.0');
    const rows = Array.from(el.querySelectorAll('[aria-label="OBS scenes"] > li'));
    expect(rows.map((r) => r.querySelector('strong')?.textContent)).toEqual(['Starting Soon', 'Just Chatting', 'Be Right Back']);
    expect(rows[0].textContent).toContain('Live');
    await noViolations('connected');
    button(rows[1], 'Switch')!.click();
    await settle(30);
    expect(e.st.current).toBe('Just Chatting');
    el.querySelector<HTMLButtonElement>('button[aria-label="Sources in Just Chatting"]')!.click();
    await settle(30);
    el.querySelector<HTMLButtonElement>('button[aria-label="Hide Webcam in Just Chatting"]')!.click();
    await settle(30);
    expect(e.visible.Webcam).toBe(false);
    expect(el.querySelector('button[aria-label="Show Webcam in Just Chatting"]')).not.toBeNull();
  });

  it('switches for now and back, unless the scene was changed in OBS', async () => {
    const e = obsEngine();
    const el = start(connected());
    await settle(30);
    const brb = Array.from(el.querySelectorAll('[aria-label="OBS scenes"] > li'))[2];
    button(brb, 'Switch for now')!.click();
    await settle(40);
    expect(el.textContent).toContain('it can go back to Starting Soon');
    button(el, 'Switch back to Starting Soon')!.click();
    await settle(40);
    expect(e.st.current).toBe('Starting Soon');
    expect(el.textContent).toContain('Switched back to Starting Soon.');

    // Again, but the streamer changes the scene in OBS meanwhile.
    button(Array.from(el.querySelectorAll('[aria-label="OBS scenes"] > li'))[2], 'Switch for now')!.click();
    await settle(40);
    e.st.snapshot!.manual = true; // the streamer clicks another scene in OBS
    e.st.current = 'Just Chatting';
    button(el, 'Switch back to Starting Soon')!.click();
    await settle(40);
    expect(el.textContent).toContain('Stayed: The scene was changed in OBS');
    expect(e.st.current).toBe('Just Chatting');
  });
});
