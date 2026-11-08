// Saved variables as the Variables screen shows and edits them.

export type ValueKind = 'number' | 'text' | 'bool' | 'json';

/** The kind of editor a value gets. */
export function kindOf(v: unknown): ValueKind {
  if (typeof v === 'number') return 'number';
  if (typeof v === 'string') return 'text';
  if (typeof v === 'boolean') return 'bool';
  return 'json';
}

/** Reads what was typed as the chosen kind. */
export function parseValue(kind: ValueKind, text: string): { ok: true; value: unknown } | { ok: false; error: string } {
  switch (kind) {
    case 'number': {
      const t = text.trim();
      const n = Number(t);
      if (t === '' || !Number.isFinite(n)) return { ok: false, error: 'Type a number, like 3 or -1.5.' };
      return { ok: true, value: n };
    }
    case 'bool':
      if (text !== 'true' && text !== 'false') return { ok: false, error: 'Choose true or false.' };
      return { ok: true, value: text === 'true' };
    case 'json':
      try {
        const v = JSON.parse(text);
        if (v === null) return { ok: false, error: 'An empty value can’t be saved; delete the variable instead.' };
        return { ok: true, value: v };
      } catch {
        return { ok: false, error: 'That isn’t JSON. A list looks like [1, "two"]; a map like {"a": 1}.' };
      }
    default:
      return { ok: true, value: text };
  }
}

/** A value on one short line. */
export function showValue(v: unknown, max = 120): string {
  const s = typeof v === 'string' ? `“${v}”` : JSON.stringify(v);
  return s.length > max ? s.slice(0, max - 1) + '…' : s;
}
