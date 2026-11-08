import { describe, expect, it } from 'vitest';
import {
  buildCondition,
  exprToText,
  idFromName,
  parseCondition,
  problemsAt,
  stepKind,
  textToExpr,
  toDisplay,
  toTemplate,
  typedValue,
  waitSeconds,
} from './editor';

const named = [
  { token: 'viewer', label: 'Their name', expr: 'event.actor.display_name' },
  { token: 'first_word', label: 'First word', expr: 'event.payload.arg_list[?0].orValue("")' },
];

describe('values in text', () => {
  it('shows known values as {tokens} and keeps everything else', () => {
    expect(toDisplay('Hi ${event.actor.display_name}!', named)).toBe('Hi {viewer}!');
    expect(toDisplay('${event.payload.arg_list[?0].orValue("")}', named)).toBe('{first_word}');
    expect(toDisplay('n=${steps.n.output.value}', named)).toBe('n={steps.n.output.value}');
    expect(toDisplay('${saved.global.?deaths.orValue("")}', named)).toBe('{saved.global.deaths}');
    expect(toDisplay('${1 + 2} and $${literal} and {braces}', named)).toBe('${1 + 2} and $${literal} and {braces}');
    expect(toDisplay('${"}" + event.actor.login}', named)).toBe('${"}" + event.actor.login}');
  });

  it('stores {tokens} as expressions and round-trips', () => {
    expect(toTemplate('Hi {viewer}!', named)).toBe('Hi ${event.actor.display_name}!');
    expect(toTemplate('{saved.viewer.lurks} lurks', named)).toBe('${saved.viewer.?lurks.orValue("")} lurks');
    expect(toTemplate('{args.who} {nothing} {} ${1 + 2} $${x}', named)).toBe('${args.who} {nothing} {} ${1 + 2} $${x}');
    for (const t of [
      'Hi ${event.actor.display_name}, ${steps.n.output.value}!',
      'plain',
      '${1 + 2} $${x} {not a token}',
      '${saved.workflow.?last.orValue("")}',
    ]) {
      expect(toTemplate(toDisplay(t, named), named)).toBe(t);
    }
  });

  it('turns text with values into a string expression and back', () => {
    const e = textToExpr('Hi {viewer} "you"', named);
    expect(e).toBe('"Hi " + string(event.actor.display_name) + " \\"you\\""');
    expect(exprToText(e, named)).toBe('Hi {viewer} "you"');
    expect(textToExpr('', named)).toBe('""');
    expect(exprToText('"1"', named)).toBe('1');
    expect(exprToText('vars.x * 2', named)).toBeUndefined();
  });

  it('keeps numbers and true/false typed for "any" values', () => {
    expect(typedValue('5', named)).toBe(5);
    expect(typedValue('-1.5', named)).toBe(-1.5);
    expect(typedValue('true', named)).toBe(true);
    expect(typedValue('Hi {viewer}', named)).toBe('Hi ${event.actor.display_name}');
    expect(typedValue('5 apples', named)).toBe('5 apples');
  });
});

describe('conditions', () => {
  it('builds the forms the engine checks and reads them back', () => {
    const cases = [
      { c: { value: '{viewer}', op: 'is' as const, text: 'Fan' }, src: 'string(event.actor.display_name) == "Fan"' },
      { c: { value: '{viewer}', op: 'is_not' as const, text: 'a "b"' }, src: 'string(event.actor.display_name) != "a \\"b\\""' },
      {
        c: { value: '{first_word}', op: 'contains' as const, text: 'hi' },
        src: 'string(event.payload.arg_list[?0].orValue("")).lowerAscii().contains("hi")',
      },
      { c: { value: '{saved.global.deaths}', op: 'more' as const, text: '3' }, src: 'double(saved.global.?deaths.orValue(0)) > 3' },
      { c: { value: '{vars.n}', op: 'less' as const, text: '-1.5' }, src: 'double(vars.n) < -1.5' },
    ];
    for (const { c, src } of cases) {
      expect(buildCondition(c, named)).toBe(src);
      expect(parseCondition(src, named)).toEqual(c);
    }
  });

  it('refuses what it can not store, and leaves other expressions alone', () => {
    expect(buildCondition({ value: '{nothing}', op: 'is', text: 'x' }, named)).toBeUndefined();
    expect(buildCondition({ value: '{viewer}', op: 'more', text: 'lots' }, named)).toBeUndefined();
    expect(parseCondition('event.payload.chatter.is_moderator || vars.x > 2', named)).toBeUndefined();
  });
});

describe('ids, steps and problems', () => {
  it('makes a unique id from a name', () => {
    expect(idFromName('Say hello!', [])).toBe('say-hello');
    expect(idFromName('Say hello', ['say-hello', 'say-hello-2'])).toBe('say-hello-3');
    expect(idFromName('!!!', [])).toBe('workflow');
    expect(idFromName('_x', [])).toBe('x');
  });

  it('knows which steps it can show', () => {
    expect(stepKind({ action: 'chat.send' })).toBe('action');
    expect(stepKind({ if: 'true', do: [] })).toBe('if');
    expect(stepKind({ if: '', do: [] })).toBe('if'); // a new If… before its condition is set
    expect(stepKind({ do: [] })).toBe('other');
    expect(stepKind({ foreach: 'vars.list', steps: [] })).toBe('other');
    expect(waitSeconds('5s')).toBe(5);
    expect(waitSeconds('1m')).toBeUndefined();
  });

  it('finds the problems inside a path, not its neighbours', () => {
    const ps = [{ path: '$.steps[1].with.message' }, { path: '$.steps[10]' }, { path: '$.steps[1]' }];
    expect(problemsAt(ps, '$.steps[1]')).toHaveLength(2);
  });
});
