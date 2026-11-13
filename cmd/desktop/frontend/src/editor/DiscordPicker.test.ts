import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it } from 'vitest';
import ParamHarness from '../test/ParamHarness.svelte';
import type { ActionParam } from '../lib/api';
import type { StepDef } from '../lib/editor';

let comp: ReturnType<typeof mount> | undefined;

afterEach(() => {
  if (comp) unmount(comp);
  comp = undefined;
  document.body.innerHTML = '';
});

const param: ActionParam = { name: 'webhook', label: 'Webhook', type: 'text', required: true, literal: true, editor: 'discord_webhook' };
const hook = (name: string) => ({ name, id: '1', discord_name: 'Captain Hook', channel_id: '2', added_at: new Date().toISOString() });

function start(step: StepDef, discord = [hook('announcements'), hook('clips')]) {
  const target = document.createElement('main');
  document.body.appendChild(target);
  const props = { param, holder: { step }, env: { discord } };
  comp = mount(ParamHarness, { target, props });
  flushSync();
  return { target, holder: props.holder };
}

describe('the Discord webhook picker', () => {
  it('offers the saved webhooks and sets the step', () => {
    const { target, holder } = start({ action: 'discord.send', with: {} });
    const select = target.querySelector('select')!;
    expect(Array.from(select.options).map((o) => o.textContent)).toEqual([
      'Choose a webhook…',
      'announcements (Discord’s “Captain Hook”)',
      'clips (Discord’s “Captain Hook”)',
    ]);
    select.value = 'clips';
    select.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();
    expect(holder.step.with?.webhook).toBe('clips');
  });

  it('keeps a name that isn’t saved, marked, and says where to add one', () => {
    const { target } = start({ action: 'discord.send', with: { webhook: 'gone' } }, []);
    expect(target.querySelector('select')!.value).toBe('gone');
    expect(target.textContent).toContain('gone (not found)');
    expect(target.textContent).toContain('No Discord webhooks yet: add one on Connections.');
  });
});
