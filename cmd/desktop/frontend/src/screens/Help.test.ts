import { mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Help from './Help.svelte';
import { mockFetch, runningStatus, settle } from '../test/helpers';
import { screens } from '../lib/screens';
import guide from '../help/guide.html?raw';

let comp: ReturnType<typeof mount> | undefined;

afterEach(() => {
  if (comp) unmount(comp);
  comp = undefined;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
});

async function start(dataDir = '/tmp/sg') {
  mockFetch({
    '/v1/status': () => ({ status: 200, body: { ...runningStatus().body, version: '0.1.0-beta.1', data_dir: dataDir } }),
  });
  const target = document.createElement('main');
  document.body.appendChild(target);
  comp = mount(Help, { target });
  await settle();
  return target;
}

describe('Help', () => {
  // A beta tester learns every screen from Help: each one in the sidebar,
  // after Home (which has Getting started), has its own entry.
  it('explains every screen', async () => {
    const el = await start();
    const terms = Array.from(el.querySelectorAll('#help-how ~ dl dt, dl.help-terms dt')).map((d) => d.textContent?.trim());
    for (const s of screens.filter((s) => s.id !== 'home')) {
      expect(terms, s.label).toContain(s.label);
    }
    for (const t of ['Ads', 'Backups', 'The window']) expect(terms).toContain(t);
  });

  // What a tester's report needs: the version, and where the logs are.
  it('says the version and where the logs are, and how to report a problem', async () => {
    const el = await start();
    const text = el.textContent ?? '';
    expect(text).toContain('0.1.0-beta.1');
    expect(text).toMatch(/\/tmp\/sg[/\\]logs/);
    const report = el.querySelector('#help-report')?.closest('section')?.textContent ?? '';
    expect(report).toContain('what you expected');
    expect(report).toContain('logs');
    expect(report).toMatch(/never contain your passwords/);
  });

  // Reports go to GitHub (D-115): issues, or a private security report.
  it('opens GitHub to report a problem or a security problem', async () => {
    const opened: string[] = [];
    mockFetch({
      '/v1/status': () => ({ status: 200, body: { ...runningStatus().body, version: '0.1.0-beta.1', data_dir: '/tmp/sg' } }),
      '/desktop/open-url': (_m, body) => {
        opened.push((body as { url: string }).url);
        return { status: 204, body: null };
      },
    });
    const target = document.createElement('main');
    document.body.appendChild(target);
    comp = mount(Help, { target });
    await settle();
    const buttons = Array.from(target.querySelectorAll('button'));
    buttons.find((b) => b.textContent?.startsWith('Report a problem'))!.click();
    buttons.find((b) => b.textContent?.startsWith('Report a security problem'))!.click();
    await settle();
    expect(opened).toEqual([
      'https://github.com/shawnbutts/StreamGremlin/issues/new/choose',
      'https://github.com/shawnbutts/StreamGremlin/security/advisories/new',
    ]);
  });

  it('writes the logs folder as Windows does', async () => {
    const el = await start('C:\\Users\\sam\\AppData\\Local\\StreamGremlin');
    expect(el.textContent).toContain('C:\\Users\\sam\\AppData\\Local\\StreamGremlin\\logs');
  });

  // The same text is the website's Help (D-112), shown as it is: plain
  // HTML, and every term has an id the site links to.
  it('keeps the shared Help text plain', () => {
    expect(guide).not.toMatch(/<(script|style|iframe|img|object|embed|form)\b/i);
    expect(guide).not.toMatch(/\son[a-z]+\s*=/i);
    expect(guide).not.toMatch(/javascript:/i);
    const dts = guide.match(/<dt\b[^>]*>/g) ?? [];
    expect(dts.length).toBeGreaterThan(20);
    for (const dt of dts) expect(dt).toMatch(/^<dt id="help-[a-z0-9-]+">$/);
  });
});
