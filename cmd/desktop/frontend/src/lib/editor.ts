// The plain-words workflow editor's conversions (docs/adr/0010-editor-run-delete.md).
// The editor works on the definition itself, so anything it can't show is
// kept as it is. These functions turn stored forms into words and back.

import type { PaletteValue } from './api';

/** A workflow definition as the editor holds it. Unknown fields are kept. */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type Def = Record<string, any>;
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type StepDef = Record<string, any>;

export const SCHEMA = 'streamgremlin.workflow/v1';

/** A new, empty workflow: a chat command with no steps yet. */
export function emptyDefinition(): Def {
  return { schema: SCHEMA, id: '', name: '', triggers: [{ type: 'chat.command.received', where: {} }], steps: [] };
}

const ID = /^[a-z0-9][a-z0-9_-]*(\.[a-z0-9][a-z0-9_-]*)*$/;

/** An id from a name: "Say hello!" → "say-hello", unique among taken. */
export function idFromName(name: string, taken: string[]): string {
  let base =
    name
      .toLowerCase()
      .normalize('NFKD')
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '')
      .slice(0, 56) || 'workflow';
  if (!ID.test(base)) base = 'workflow-' + base;
  let id = base;
  for (let n = 2; taken.includes(id); n++) id = `${base}-${n}`;
  return id;
}

export const validId = (id: string) => ID.test(id) && id.length <= 64;

// --- Values in text: {viewer} on screen, ${event.actor.display_name} stored.

const PATH = /^(event|vars|steps|args)(\.[A-Za-z_][A-Za-z0-9_]*)+$/;
const SAVED = /^saved\.(global|viewer|workflow)\.([a-z][a-z0-9_]*)$/;
const SAVED_EXPR = /^saved\.(global|viewer|workflow)\.\?([a-z][a-z0-9_]*)\.orValue\((""|0)\)$/;

/** The expression a {token} stands for, or undefined when it names nothing. */
export function tokenExpr(token: string, named: PaletteValue[], numeric = false): string | undefined {
  const n = named.find((v) => v.token === token);
  if (n?.expr) return n.expr; // saved variables offered by name have no expr of their own
  const s = SAVED.exec(token);
  if (s) return `saved.${s[1]}.?${s[2]}.orValue(${numeric ? '0' : '""'})`;
  if (PATH.test(token)) return token;
  return undefined;
}

/** The {token} an expression is shown as, or undefined. */
export function exprToken(expr: string, named: PaletteValue[]): string | undefined {
  const e = expr.trim();
  const n = named.find((v) => v.expr === e);
  if (n) return n.token;
  const s = SAVED_EXPR.exec(e);
  if (s) return `saved.${s[1]}.${s[2]}`;
  if (PATH.test(e)) return e;
  return undefined;
}

/** Where a "${" that starts at i ends (the index of its "}"), skipping quotes and nested braces. */
function exprEnd(s: string, from: number): number {
  let depth = 0;
  let quote = '';
  for (let i = from; i < s.length; i++) {
    const c = s[i];
    if (quote) {
      if (c === '\\') i++;
      else if (c === quote) quote = '';
      continue;
    }
    if (c === '"' || c === "'") quote = c;
    else if (c === '{') depth++;
    else if (c === '}') {
      if (depth === 0) return i;
      depth--;
    }
  }
  return -1;
}

/** A stored with-value as the editor shows it: known values become {tokens}. */
export function toDisplay(template: string, named: PaletteValue[]): string {
  let out = '';
  for (let i = 0; i < template.length; ) {
    if (template.startsWith('$${', i)) {
      out += '$${';
      i += 3;
    } else if (template.startsWith('${', i)) {
      const end = exprEnd(template, i + 2);
      if (end < 0) return out + template.slice(i);
      const expr = template.slice(i + 2, end);
      const tok = exprToken(expr, named);
      out += tok ? `{${tok}}` : template.slice(i, end + 1);
      i = end + 1;
    } else {
      out += template[i++];
    }
  }
  return out;
}

/** What the editor shows, stored: {tokens} become ${expressions}; other braces stay as typed. */
export function toTemplate(display: string, named: PaletteValue[]): string {
  let out = '';
  for (let i = 0; i < display.length; ) {
    if (display.startsWith('$${', i)) {
      out += '$${';
      i += 3;
    } else if (display.startsWith('${', i)) {
      const end = exprEnd(display, i + 2);
      if (end < 0) return out + display.slice(i);
      out += display.slice(i, end + 1);
      i = end + 1;
    } else if (display[i] === '{') {
      const end = display.indexOf('}', i);
      const expr = end > 0 ? tokenExpr(display.slice(i + 1, end), named) : undefined;
      if (expr) {
        out += '${' + expr + '}';
        i = end + 1;
      } else {
        out += display[i++];
      }
    } else {
      out += display[i++];
    }
  }
  return out;
}

// --- "Remember a value": text with values, stored as a CEL string expression.

/** Text with {tokens} as a CEL expression: "Hi " + string(event.actor.display_name). */
export function textToExpr(display: string, named: PaletteValue[]): string {
  const parts: string[] = [];
  let lit = '';
  for (let i = 0; i < display.length; ) {
    if (display[i] === '{') {
      const end = display.indexOf('}', i);
      const expr = end > 0 ? tokenExpr(display.slice(i + 1, end), named) : undefined;
      if (expr) {
        if (lit) parts.push(JSON.stringify(lit));
        lit = '';
        parts.push(`string(${expr})`);
        i = end + 1;
        continue;
      }
    }
    lit += display[i++];
  }
  if (lit || parts.length === 0) parts.push(JSON.stringify(lit));
  return parts.join(' + ');
}

/** The reverse of textToExpr, or undefined for any other expression. */
export function exprToText(expr: string, named: PaletteValue[]): string | undefined {
  let out = '';
  let i = 0;
  const s = expr.trim();
  while (i < s.length) {
    if (s[i] === '"') {
      let j = i + 1;
      for (; j < s.length && s[j] !== '"'; j++) if (s[j] === '\\') j++;
      try {
        out += JSON.parse(s.slice(i, j + 1));
      } catch {
        return undefined;
      }
      i = j + 1;
    } else if (s.startsWith('string(', i)) {
      const end = s.indexOf(')', i);
      const tok = end > 0 ? exprToken(s.slice(i + 7, end), named) : undefined;
      if (!tok) return undefined;
      out += `{${tok}}`;
      i = end + 1;
    } else {
      return undefined;
    }
    if (i < s.length) {
      if (!s.startsWith(' + ', i)) return undefined;
      i += 3;
    }
  }
  return out;
}

// --- Conditions: a value, a comparison and a text.

export type Op = 'is' | 'is_not' | 'contains' | 'more' | 'less';

export const ops: { id: Op; label: string }[] = [
  { id: 'is', label: 'is' },
  { id: 'is_not', label: 'is not' },
  { id: 'contains', label: 'contains' },
  { id: 'more', label: 'is more than' },
  { id: 'less', label: 'is less than' },
];

export interface Condition {
  value: string; // a {token}
  op: Op;
  text: string;
}

const STR = '("(?:[^"\\\\]|\\\\.)*")';
const NUM = '(-?\\d+(?:\\.\\d+)?)';
const forms: { op: Op; re: RegExp }[] = [
  { op: 'is', re: new RegExp(`^string\\((.+)\\) == ${STR}$`) },
  { op: 'is_not', re: new RegExp(`^string\\((.+)\\) != ${STR}$`) },
  { op: 'contains', re: new RegExp(`^string\\((.+)\\)\\.lowerAscii\\(\\)\\.contains\\(${STR}\\)$`) },
  { op: 'more', re: new RegExp(`^double\\((.+)\\) > ${NUM}$`) },
  { op: 'less', re: new RegExp(`^double\\((.+)\\) < ${NUM}$`) },
];

/** A stored condition in words, or undefined when it's another expression. */
export function parseCondition(src: string, named: PaletteValue[]): Condition | undefined {
  for (const f of forms) {
    const m = f.re.exec(src.trim());
    if (!m) continue;
    const tok = exprToken(m[1], named);
    if (!tok) return undefined;
    const text = f.op === 'more' || f.op === 'less' ? m[2] : (JSON.parse(m[2]) as string);
    return { value: `{${tok}}`, op: f.op, text };
  }
  return undefined;
}

/** A condition in words, stored; undefined when its value or number isn't usable. */
export function buildCondition(c: Condition, named: PaletteValue[]): string | undefined {
  const tok = /^\{(.+)\}$/.exec(c.value.trim())?.[1];
  const numeric = c.op === 'more' || c.op === 'less';
  const expr = tok ? tokenExpr(tok, named, numeric) : undefined;
  if (!expr) return undefined;
  switch (c.op) {
    case 'is':
      return `string(${expr}) == ${JSON.stringify(c.text)}`;
    case 'is_not':
      return `string(${expr}) != ${JSON.stringify(c.text)}`;
    case 'contains':
      return `string(${expr}).lowerAscii().contains(${JSON.stringify(c.text.toLowerCase())})`;
    default: {
      const n = Number(c.text.trim());
      if (c.text.trim() === '' || !Number.isFinite(n)) return undefined;
      return `double(${expr}) ${c.op === 'more' ? '>' : '<'} ${n}`;
    }
  }
}

// --- Step kinds and their words.

export type StepKind = 'action' | 'set' | 'if' | 'choose' | 'wait' | 'other';

/** What the editor can show a step as. Anything else is kept and shown as "other". */
export function stepKind(s: StepDef): StepKind {
  if (s.action) return 'action';
  if (s.set) return 'set';
  if (s.do && typeof s.if === 'string') return 'if';
  if (s.choose) return 'choose';
  if (s.wait) return 'wait';
  return 'other';
}

/** "5s" → 5; anything else → undefined (shown as typed). */
export function waitSeconds(w: string): number | undefined {
  const m = /^(\d+(?:\.\d+)?)s$/.exec(w);
  return m ? Number(m[1]) : undefined;
}

/** A value for an "any" parameter: numbers and true/false keep their type. */
export function typedValue(text: string, named: PaletteValue[]): unknown {
  const t = text.trim();
  if (t !== '' && Number.isFinite(Number(t)) && /^-?\d/.test(t)) return Number(t);
  if (t === 'true' || t === 'false') return t === 'true';
  return toTemplate(text, named);
}

/** Problems at a JSON path or inside it. */
export function problemsAt<T extends { path: string }>(problems: T[], path: string): T[] {
  return problems.filter((p) => p.path === path || p.path.startsWith(path + '.') || p.path.startsWith(path + '['));
}

/** Problems that belong to the card at path, not to steps nested inside it. */
export function ownProblems<T extends { path: string }>(problems: T[], path: string): T[] {
  return problemsAt(problems, path).filter((p) => !/\.(do|else|steps)\[/.test(p.path.slice(path.length)));
}
