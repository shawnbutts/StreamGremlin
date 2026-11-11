import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import ParamHarness from '../test/ParamHarness.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { ActionParam } from '../lib/api';
import type { StepDef } from '../lib/editor';

let comp: ReturnType<typeof mount> | undefined;

afterEach(() => {
  if (comp) unmount(comp);
  comp = undefined;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
});

const param: ActionParam = { name: 'script', label: 'Script', type: 'text', required: true, literal: true, editor: 'code', max_length: 65536 };

function start(step: StepDef) {
  const target = document.createElement('main');
  document.body.appendChild(target);
  const props = { param, holder: { step } };
  comp = mount(ParamHarness, { target, props });
  flushSync();
  return { target, props: props.holder };
}

const button = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === text);

function type(el: HTMLTextAreaElement, v: string) {
  el.value = v;
  el.dispatchEvent(new Event('input', { bubbles: true }));
}

describe('a Lua script step', () => {
  it('keeps what is typed, and tries it with the step’s values', async () => {
    const calls = mockFetch({
      '/v1/lua/try': () => ({ status: 200, body: { ok: true, result: { roll: 4 }, log: ['rolled 4'], took_ms: 2 } }),
    });
    const step: StepDef = { action: 'lua.run', with: { args: { sides: '20', who: '${event.actor.display_name}' }, time_limit: 2 } };
    const { target, props } = start(step);
    const box = target.querySelector<HTMLTextAreaElement>('textarea')!;
    expect(box.labels?.[0]?.textContent).toBe('Script');
    expect(button(target, 'Try it')!.disabled).toBe(true);
    type(box, 'return {roll = math.random(sg.args.sides)}');
    flushSync();
    expect(props.step.with?.script).toBe('return {roll = math.random(sg.args.sides)}');
    button(target, 'Try it')!.click();
    await settle();
    const sent = calls.find((c) => c.path === '/v1/lua/try')!.body as { script: string; args: Record<string, unknown>; time_limit: number };
    expect(sent.script).toBe('return {roll = math.random(sg.args.sides)}');
    expect(sent.args.sides).toBe('20');
    expect(sent.time_limit).toBe(2);
    expect(String(sent.args.who)).not.toContain('${');
    expect(target.textContent).toContain('"roll": 4');
    expect(target.textContent).toContain('rolled 4');
    expect(target.textContent).toContain('sent as written');
    const results = await axe.run(target);
    expect(results.violations).toEqual([]);
  });

  it('says why a script failed, with what it logged', async () => {
    mockFetch({
      '/v1/lua/try': () => ({ status: 200, body: { ok: false, error: 'the script ran longer than its 1s time limit', kind: 'timeout', log: ['start'], took_ms: 1000 } }),
    });
    const { target } = start({ action: 'lua.run', with: { script: 'sg.log("start") while true do end' } });
    button(target, 'Try it')!.click();
    await settle();
    expect(target.querySelector('.error')?.textContent).toContain('ran longer than its 1s time limit');
    expect(target.textContent).toContain('start');
    expect(target.textContent).not.toContain('sent as written');
  });

  it('refuses a computed script before it is saved', () => {
    mockFetch({});
    const { target } = start({ action: 'lua.run', with: { script: 'return "${event.type}"' } });
    expect(target.textContent).toContain("can't contain");
    expect(button(target, 'Try it')!.disabled).toBe(true);
    expect(target.querySelector('textarea')!.getAttribute('aria-invalid')).toBe('true');
  });

  it('shows how close a long script is to the limit', () => {
    mockFetch({});
    const { target } = start({ action: 'lua.run', with: { script: '-'.repeat(66000) } });
    expect(target.textContent).toContain('65 of 64 KiB');
    expect(button(target, 'Try it')!.disabled).toBe(true);
  });
});
