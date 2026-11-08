import { describe, expect, it } from 'vitest';
import { kindOf, parseValue, showValue } from './variables';

describe('variable values', () => {
  it('picks an editor from the value', () => {
    expect([3, 'hi', true, [1], { a: 1 }].map(kindOf)).toEqual(['number', 'text', 'bool', 'json', 'json']);
  });

  it('reads what was typed as the chosen kind', () => {
    expect(parseValue('number', ' -1.5 ')).toEqual({ ok: true, value: -1.5 });
    expect(parseValue('number', '').ok).toBe(false);
    expect(parseValue('number', '12abc').ok).toBe(false);
    expect(parseValue('text', '42')).toEqual({ ok: true, value: '42' }); // text stays text
    expect(parseValue('bool', 'false')).toEqual({ ok: true, value: false });
    expect(parseValue('json', '[1,"two"]')).toEqual({ ok: true, value: [1, 'two'] });
    expect(parseValue('json', 'null').ok).toBe(false);
    expect(parseValue('json', '{oops').ok).toBe(false);
  });

  it('shows a value on one short line', () => {
    expect(showValue('hi')).toBe('“hi”');
    expect(showValue({ a: 1 })).toBe('{"a":1}');
    expect(showValue('x'.repeat(200))).toHaveLength(120);
  });
});
