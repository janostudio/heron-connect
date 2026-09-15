import { describe, it, expect } from 'vitest';
import {
  parseProgressCard, buildUnits, stableKey, callKey, PROGRESS_CARD_PREFIX,
  type ProgressCardEntry, type RenderUnit,
} from './ProgressCard';

// ── parseProgressCard ────────────────────────────────────────

describe('parseProgressCard', () => {
  const wrap = (payload: unknown) => PROGRESS_CARD_PREFIX + JSON.stringify(payload);

  it('parses a well-formed payload', () => {
    const p = parseProgressCard(wrap({ items: [{ kind: 'thinking', text: 'hmm' }], state: 'running' }));
    expect(p?.items).toHaveLength(1);
    expect(p?.state).toBe('running');
  });

  it('returns null for content without the prefix', () => {
    expect(parseProgressCard('just a normal answer')).toBeNull();
    expect(parseProgressCard('')).toBeNull();
  });

  it('returns null for malformed JSON after the prefix', () => {
    expect(parseProgressCard(PROGRESS_CARD_PREFIX + '{not json')).toBeNull();
  });

  it('returns null when items is missing or empty', () => {
    expect(parseProgressCard(wrap({}))).toBeNull();
    expect(parseProgressCard(wrap({ items: [] }))).toBeNull();
    expect(parseProgressCard(wrap({ items: 'nope' }))).toBeNull();
  });

  it('accepts a payload with no explicit state (still live)', () => {
    expect(parseProgressCard(wrap({ items: [{ kind: 'info', text: 'x' }] }))?.items).toHaveLength(1);
  });
});

// ── stableKey ────────────────────────────────────────────────
//
// The key must survive the backend trimming the OLDEST entries from the head
// (core/progress_compact.go AppendStructured), otherwise every expand/collapse
// toggle would jump to a different block. It is content-based, not index-based.

describe('stableKey', () => {
  const entry = (over: Partial<ProgressCardEntry> = {}): ProgressCardEntry =>
    ({ kind: 'thinking', text: 'pondering', ...over });

  it('is deterministic for the same entry and index', () => {
    expect(stableKey(entry(), 0)).toBe(stableKey(entry(), 0));
  });

  it('includes kind, tool and a text snippet', () => {
    const k = stableKey(entry({ kind: 'tool_use', tool: 'Read', text: 'file contents' }), 0);
    expect(k).toContain('tool_use');
    expect(k).toContain('Read');
    expect(k).toContain('file contents');
  });

  it('differs for a different index (tie-break for identical blocks)', () => {
    expect(stableKey(entry(), 0)).not.toBe(stableKey(entry(), 1));
  });

  it('normalises whitespace so a reflow does not change the key', () => {
    expect(stableKey(entry({ text: 'a   b\n\nc' }), 0)).toBe(stableKey(entry({ text: 'a b c' }), 0));
  });

  it('truncates long text to a bounded snippet', () => {
    const long = 'x'.repeat(500);
    expect(stableKey(entry({ text: long }), 0).length).toBeLessThan(120);
  });

  it('tolerates a missing tool field (empty segment, not the string "undefined")', () => {
    const k = stableKey({ kind: 'info', text: 'hi' }, 0);
    expect(k).toBe('info||hi|0');
  });
});

// ── callKey ──────────────────────────────────────────────────

describe('callKey', () => {
  it('is deterministic', () => {
    expect(callKey('Read', 'a', 'b', 0)).toBe(callKey('Read', 'a', 'b', 0));
  });

  it('distinguishes different tools/inputs/results', () => {
    const base = callKey('Read', 'a', 'b', 0);
    expect(callKey('Bash', 'a', 'b', 0)).not.toBe(base);
    expect(callKey('Read', 'z', 'b', 0)).not.toBe(base);
    expect(callKey('Read', 'a', 'z', 0)).not.toBe(base);
    expect(callKey('Read', 'a', 'b', 1)).not.toBe(base);
  });

  it('normalises whitespace and truncates', () => {
    expect(callKey('Read', 'a   b', 'c', 0)).toBe(callKey('Read', 'a b', 'c', 0));
    expect(callKey('Read', 'x'.repeat(200), '', 0).length).toBeLessThan(120);
  });
});

// ── buildUnits ───────────────────────────────────────────────
//
// This is the tool-call pairing / grouping that produces what the user sees as
// the tool log. Mis-pairing here shows a tool as permanently "running".

describe('buildUnits', () => {
  const use = (id: string, tool: string, text = 'input'): ProgressCardEntry =>
    ({ kind: 'tool_use', id, tool, text });
  const result = (id: string, tool: string, text = 'output'): ProgressCardEntry =>
    ({ kind: 'tool_result', id, tool, text, success: true });

  it('returns no units for an empty list', () => {
    expect(buildUnits([])).toEqual([]);
  });

  it('passes thinking/info entries through as-is', () => {
    const units = buildUnits([
      { kind: 'thinking', text: 'pondering' },
      { kind: 'info', text: 'note' },
    ]);
    expect(units.map(u => u.type)).toEqual(['entry', 'entry']);
    expect((units[0] as Extract<RenderUnit, { type: 'entry' }>).entry.text).toBe('pondering');
  });

  it('pairs tool_use with tool_result and marks the call finished', () => {
    const units = buildUnits([use('1', 'Read'), result('1', 'Read')]);
    const group = units.find(u => u.type === 'group') as Extract<RenderUnit, { type: 'group' }>;
    expect(group.group.calls).toHaveLength(1);
    expect(group.group.calls[0].running).toBe(false);
    expect(group.group.calls[0].input).toBe('input');
    expect(group.group.calls[0].result).toBe('output');
  });

  it('leaves an unpaired tool_use marked as running', () => {
    const units = buildUnits([use('1', 'Read')]);
    const group = units[0] as Extract<RenderUnit, { type: 'group' }>;
    expect(group.group.calls[0].running).toBe(true);
    expect(group.group.calls[0].result).toBe('');
  });

  it('groups consecutive calls to the same tool', () => {
    const units = buildUnits([use('1', 'Read'), use('2', 'Read'), use('3', 'Read')]);
    const groups = units.filter(u => u.type === 'group');
    expect(groups).toHaveLength(1);
    expect((groups[0] as Extract<RenderUnit, { type: 'group' }>).group.calls).toHaveLength(3);
  });

  it('splits groups when the tool changes', () => {
    const units = buildUnits([use('1', 'Read'), use('2', 'Bash'), use('3', 'Read')]);
    const groups = units.filter(u => u.type === 'group') as Extract<RenderUnit, { type: 'group' }>[];
    expect(groups.map(g => g.group.tool)).toEqual(['Read', 'Bash', 'Read']);
  });

  it('breaks a group when a non-tool entry intervenes', () => {
    const units = buildUnits([use('1', 'Read'), { kind: 'thinking', text: 'hmm' }, use('2', 'Read')]);
    expect(units.map(u => u.type)).toEqual(['group', 'entry', 'group']);
  });

  it('creates a standalone call for an orphan tool_result', () => {
    const units = buildUnits([result('99', 'Bash', 'output only')]);
    const group = units[0] as Extract<RenderUnit, { type: 'group' }>;
    expect(group.group.tool).toBe('Bash');
    expect(group.group.calls[0].running).toBe(false);
    expect(group.group.calls[0].input).toBe('');
    expect(group.group.calls[0].result).toBe('output only');
  });

  it('pairs by id even when the result arrives out of order', () => {
    // Two Reads interleaved with an unrelated thinking block.
    const units = buildUnits([
      use('1', 'Read', 'first'),
      { kind: 'thinking', text: 'between' },
      result('1', 'Read', 'first-out'),
    ]);
    const groups = units.filter(u => u.type === 'group') as Extract<RenderUnit, { type: 'group' }>[];
    expect(groups[0].group.calls[0].result).toBe('first-out');
    expect(groups[0].group.calls[0].running).toBe(false);
  });

  it('handles tool_use without an id (cannot be paired, both rows are kept)', () => {
    const units = buildUnits([{ kind: 'tool_use', tool: 'Read', text: 'x' }, { kind: 'tool_result', tool: 'Read', text: 'y' }]);
    const groups = units.filter(u => u.type === 'group') as Extract<RenderUnit, { type: 'group' }>[];
    expect(groups[0].group.calls).toHaveLength(2);
    // The id-less tool_use can never be matched, so it stays "running"…
    expect(groups[0].group.calls[0].running).toBe(true);
    // …and the result becomes its own standalone call rather than being lost.
    expect(groups[0].group.calls[1].running).toBe(false);
    expect(groups[0].group.calls[1].result).toBe('y');
  });

  it('carries the result status/exit_code/success onto the pair', () => {
    const units = buildUnits([
      use('1', 'Bash'),
      { kind: 'tool_result', id: '1', tool: 'Bash', text: 'boom', success: false, exit_code: 1, status: 'error' },
    ]);
    const call = (units[0] as Extract<RenderUnit, { type: 'group' }>).group.calls[0];
    expect(call.success).toBe(false);
    expect(call.exit_code).toBe(1);
    expect(call.status).toBe('error');
  });

  it('produces unique unit keys so React does not reuse rows', () => {
    const units = buildUnits([use('1', 'Read'), use('2', 'Bash'), { kind: 'info', text: 'x' }]);
    const keys = units.map(u => u.key);
    expect(new Set(keys).size).toBe(keys.length);
  });

  it('keeps call order stable when the payload grows', () => {
    // Simulates the backend appending a new tool call to an existing card.
    const first = buildUnits([use('1', 'Read'), use('2', 'Read')]);
    const grown = buildUnits([use('1', 'Read'), use('2', 'Read'), use('3', 'Read')]);
    const keysOf = (us: RenderUnit[]) =>
      (us.filter(u => u.type === 'group') as Extract<RenderUnit, { type: 'group' }>[])
        .flatMap(g => g.group.calls.map(c => c.key));
    expect(keysOf(grown).slice(0, 2)).toEqual(keysOf(first));
  });
});
