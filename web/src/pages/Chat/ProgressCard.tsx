// ProgressCard renders a structured agent-progress payload (one or more typed
// events: thinking, tool_use, tool_result, info, error) as a vertical list of
// collapsible blocks. It replaces the legacy "all events inlined as a single
// markdown blob" rendering and is enabled by registering the Web client with
// `supports_progress_card_payload: true` (see useBridgeSocket.ts).
//
// Wire format: the backend engine builds a ProgressCardPayload and serializes
// it as a single string content with a magic prefix. The bridge layer passes
// it through unchanged on the WebSocket; the chat view detects the prefix
// (via `parseProgressCard`) and renders this component instead of the
// markdown fallback when a payload is present.
//
// Tool-call aggregation: a tool_use entry and its matching tool_result entry
// (same `id`) are merged into a single row, and consecutive invocations of
// the same tool collapse into one group row ("Read ×5") that expands to the
// individual calls. Entries without an `id` (older payloads, adapters that
// don't report tool ids) fall back to plain grouping by tool name.
import { useState, useMemo, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import {
  ChevronDown, ChevronRight, Loader2, Check, X, Wrench, Brain, Info, AlertTriangle,
} from 'lucide-react';
import { cn } from '@/lib/utils';

// ── types — mirror core.ProgressCardEntry / ProgressCardPayload in Go ──

export type ProgressCardEntryKind = 'info' | 'thinking' | 'tool_use' | 'tool_result' | 'error';

export type ProgressCardState = 'running' | 'completed' | 'failed';

export interface ProgressCardEntry {
  kind: ProgressCardEntryKind;
  text: string;
  tool?: string;
  id?: string;
  status?: string;
  exit_code?: number | null;
  success?: boolean | null;
}

export interface ProgressCardPayload {
  version?: number;
  agent?: string;
  lang?: string;
  state?: ProgressCardState;
  items: ProgressCardEntry[];
  truncated?: boolean;
}

// Prefix must stay in sync with core.ProgressCardPayloadPrefix in Go.
export const PROGRESS_CARD_PREFIX = '__heron_connect_progress_card_v1__:';

export function parseProgressCard(content: string): ProgressCardPayload | null {
  if (!content || !content.startsWith(PROGRESS_CARD_PREFIX)) return null;
  try {
    const raw = content.slice(PROGRESS_CARD_PREFIX.length);
    const obj = JSON.parse(raw) as ProgressCardPayload;
    if (!Array.isArray(obj.items) || obj.items.length === 0) return null;
    return obj;
  } catch {
    return null;
  }
}

// ── stable key ──
// We must NOT use the array index as the React key: the backend drops the
// oldest entries from the head of the list when its maxEntries cap is hit
// (see core/progress_compact.go AppendStructured), which shifts subsequent
// items to lower indices. A content-based key survives that churn so the
// per-block expanded/collapsed state stays put when the agent produces more
// blocks. The trailing index breaks ties for two blocks with identical text
// (rare in practice but possible when the same tool is invoked twice with
// the same input).
function stableKey(item: ProgressCardEntry, idx: number): string {
  const snippet = (item.text || '').replace(/\s+/g, ' ').slice(0, 32);
  return `${item.kind}|${item.tool || ''}|${snippet}|${idx}`;
}

// ── tool call pairing & grouping ──

interface ToolCall {
  key: string;
  seq: number; // first-seen order, stable across payload updates modulo head truncation
  tool: string;
  input: string; // tool_use text (input preview)
  result: string; // tool_result text (output)
  status?: string;
  exit_code?: number | null;
  success?: boolean | null;
  running: boolean; // tool_use seen, tool_result not yet
}

interface ToolGroup {
  tool: string;
  calls: ToolCall[];
}

type RenderUnit =
  | { type: 'entry'; entry: ProgressCardEntry; key: string }
  | { type: 'group'; group: ToolGroup; key: string };

function callKey(tool: string, input: string, result: string, idx: number): string {
  const a = input.replace(/\s+/g, ' ').slice(0, 32);
  const b = result.replace(/\s+/g, ' ').slice(0, 32);
  return `${tool}|${a}|${b}|${idx}`;
}

/**
 * Pairs tool_use/tool_result entries by id and groups consecutive same-tool
 * calls. Non-tool entries (thinking/info/error) pass through untouched.
 */
function buildUnits(items: ProgressCardEntry[]): RenderUnit[] {
  // Pass 1 — pair use/result by id, keep first-seen order.
  const calls: ToolCall[] = [];
  const pending = new Map<string, ToolCall>();
  const passthrough: ({ entry: ProgressCardEntry; callSeq: number } | { call: ToolCall; callSeq: number })[] = [];
  let seq = 0;
  for (const item of items) {
    if (item.kind === 'tool_use') {
      const call: ToolCall = {
        key: callKey(item.tool || '', item.text || '', '', seq),
        seq,
        tool: item.tool || '',
        input: item.text || '',
        result: '',
        running: true,
      };
      seq++;
      calls.push(call);
      if (item.id) pending.set(item.id, call);
      passthrough.push({ call, callSeq: seq });
    } else if (item.kind === 'tool_result') {
      const matched = item.id ? pending.get(item.id) : undefined;
      if (matched) {
        matched.result = item.text || '';
        matched.status = item.status;
        matched.exit_code = item.exit_code;
        matched.success = item.success;
        matched.running = false;
        matched.key = callKey(matched.tool, matched.input, matched.result, matched.seq);
        pending.delete(item.id!);
      } else {
        const call: ToolCall = {
          key: callKey(item.tool || '', '', item.text || '', seq),
          seq,
          tool: item.tool || '',
          input: '',
          result: item.text || '',
          status: item.status,
          exit_code: item.exit_code,
          success: item.success,
          running: false,
        };
        seq++;
        calls.push(call);
        passthrough.push({ call, callSeq: seq });
      }
    } else {
      passthrough.push({ entry: item, callSeq: seq });
    }
  }

  // Pass 2 — walk the ordered stream, grouping consecutive same-tool calls.
  const units: RenderUnit[] = [];
  let current: ToolGroup | null = null;
  let entryIdx = 0;
  let callIdx = 0;
  for (const step of passthrough) {
    if ('entry' in step) {
      current = null;
      units.push({ type: 'entry', entry: step.entry, key: stableKey(step.entry, entryIdx) });
      entryIdx++;
    } else {
      const call = step.call;
      if (current && current.tool === call.tool) {
        current.calls.push(call);
      } else {
        current = { tool: call.tool, calls: [call] };
        units.push({ type: 'group', group: current, key: `g|${call.tool}|${call.key}` });
      }
      callIdx++;
    }
  }
  return units;
}

// ── block renderers (component-local) ──

function StatusBadge({ item }: { item: ProgressCardEntry }) {
  const { t } = useTranslation();
  if (item.kind === 'tool_result') {
    if (item.success === false || (typeof item.exit_code === 'number' && item.exit_code !== 0)) {
      return (
        <span className="inline-flex items-center gap-1 text-[10px] font-medium text-red-600 dark:text-red-400 bg-red-50 dark:bg-red-900/20 px-1.5 py-0.5 rounded">
          <X size={9} /> {t('progress.error', 'error')}
        </span>
      );
    }
    if (item.success === true) {
      return (
        <span className="inline-flex items-center gap-1 text-[10px] font-medium text-emerald-600 dark:text-emerald-400 bg-emerald-50 dark:bg-emerald-900/20 px-1.5 py-0.5 rounded">
          <Check size={9} /> {t('progress.ok', 'ok')}
        </span>
      );
    }
  }
  if (item.status) {
    return (
      <span className="text-[10px] font-medium text-gray-500 dark:text-gray-400 bg-gray-100 dark:bg-white/[0.08] px-1.5 py-0.5 rounded">
        {item.status}
      </span>
    );
  }
  return null;
}

function BlockRow({
  item, expanded, onToggle, isLast, isLive,
}: {
  item: ProgressCardEntry;
  expanded: boolean;
  onToggle: () => void;
  isLast: boolean;
  isLive: boolean;
}) {
  const { t } = useTranslation();
  const preview = useMemo(() => {
    const text = (item.text || '').replace(/\n+/g, ' ').trim();
    if (text.length <= 80) return text;
    return text.slice(0, 80) + '…';
  }, [item.text]);

  const headerIcon = (() => {
    switch (item.kind) {
      case 'thinking': return <Brain size={13} className="text-violet-500 dark:text-violet-400 shrink-0" />;
      case 'tool_use': return <Wrench size={13} className="text-amber-500 dark:text-amber-400 shrink-0" />;
      case 'tool_result': return <Wrench size={13} className="text-amber-500/70 dark:text-amber-400/70 shrink-0" />;
      case 'error': return <AlertTriangle size={13} className="text-red-500 dark:text-red-400 shrink-0" />;
      default: return <Info size={13} className="text-gray-500 dark:text-gray-400 shrink-0" />;
    }
  })();

  const headerLabel = (() => {
    switch (item.kind) {
      case 'thinking': return t('progress.thinking', 'Thinking');
      case 'tool_use': return item.tool || t('progress.tool', 'Tool');
      case 'tool_result': {
        const base = item.tool || t('progress.toolResult', 'Result');
        return item.status ? `${base} · ${item.status}` : base;
      }
      case 'error': return t('progress.error', 'Error');
      default: return t('progress.info', 'Info');
    }
  })();

  return (
    <div className={cn(
      'border-b border-gray-200/70 dark:border-white/[0.08] last:border-b-0',
      item.kind === 'error' && 'bg-red-50/40 dark:bg-red-900/[0.05]',
    )}>
      <button
        type="button"
        onClick={onToggle}
        className="w-full flex items-center gap-2 px-3 py-2 text-left hover:bg-gray-50 dark:hover:bg-white/[0.04] transition-colors"
        aria-expanded={expanded}
      >
        {expanded
          ? <ChevronDown size={13} className="text-gray-400 shrink-0" />
          : <ChevronRight size={13} className="text-gray-400 shrink-0" />}
        {headerIcon}
        <span className="text-xs font-medium text-gray-700 dark:text-gray-200 truncate flex-1 min-w-0">
          {headerLabel}
        </span>
        <StatusBadge item={item} />
        {isLast && isLive && item.kind !== 'tool_result' && item.kind !== 'error' && (
          <Loader2 size={11} className="text-accent animate-spin shrink-0" />
        )}
        {!expanded && preview && (
          <span className="hidden sm:inline text-[11px] text-gray-400 dark:text-gray-500 truncate max-w-[40%]">
            {preview}
          </span>
        )}
      </button>
      {expanded && (
        <pre className="mx-3 mb-2 px-3 py-2 rounded-md bg-[#fafafa] dark:bg-[#0d1117] border border-gray-200 dark:border-gray-700/60 text-[12px] leading-[1.55] font-mono whitespace-pre-wrap break-words text-gray-800 dark:text-gray-100 max-h-[60vh] overflow-auto">
          {item.text}
        </pre>
      )}
    </div>
  );
}

/** Badge summarizing a group of tool calls: error if any failed, ok if all done, spinner while running. */
function GroupBadge({ group, isLive }: { group: ToolGroup; isLive: boolean }) {
  const { t } = useTranslation();
  const anyFailed = group.calls.some(
    (c) => c.success === false || (typeof c.exit_code === 'number' && c.exit_code !== 0),
  );
  const last = group.calls[group.calls.length - 1];
  if (anyFailed) {
    return (
      <span className="inline-flex items-center gap-1 text-[10px] font-medium text-red-600 dark:text-red-400 bg-red-50 dark:bg-red-900/20 px-1.5 py-0.5 rounded">
        <X size={9} /> {t('progress.error', 'error')}
      </span>
    );
  }
  if (last?.running) {
    return isLive
      ? <Loader2 size={11} className="text-accent animate-spin shrink-0" />
      : null;
  }
  return (
    <span className="inline-flex items-center gap-1 text-[10px] font-medium text-emerald-600 dark:text-emerald-400 bg-emerald-50 dark:bg-emerald-900/20 px-1.5 py-0.5 rounded">
      <Check size={9} /> {t('progress.ok', 'ok')}
    </span>
  );
}

/** One invocation inside a group (or a standalone single-call group). */
function CallRow({
  call, expanded, onToggle, isLast, isLive,
}: {
  call: ToolCall;
  expanded: boolean;
  onToggle: () => void;
  isLast: boolean;
  isLive: boolean;
}) {
  const previewText = call.input || call.result;
  const preview = useMemo(() => {
    const text = (previewText || '').replace(/\n+/g, ' ').trim();
    if (text.length <= 80) return text;
    return text.slice(0, 80) + '…';
  }, [previewText]);

  const badge = (() => {
    if (call.success === false || (typeof call.exit_code === 'number' && call.exit_code !== 0)) {
      return (
        <span className="inline-flex items-center gap-1 text-[10px] font-medium text-red-600 dark:text-red-400 bg-red-50 dark:bg-red-900/20 px-1.5 py-0.5 rounded">
          <X size={9} />
        </span>
      );
    }
    if (call.running) {
      return isLast && isLive
        ? <Loader2 size={11} className="text-accent animate-spin shrink-0" />
        : null;
    }
    if (call.success === true) {
      return (
        <span className="inline-flex items-center gap-1 text-[10px] font-medium text-emerald-600 dark:text-emerald-400 bg-emerald-50 dark:bg-emerald-900/20 px-1.5 py-0.5 rounded">
          <Check size={9} />
        </span>
      );
    }
    if (call.status) {
      return (
        <span className="text-[10px] font-medium text-gray-500 dark:text-gray-400 bg-gray-100 dark:bg-white/[0.08] px-1.5 py-0.5 rounded">
          {call.status}
        </span>
      );
    }
    return null;
  })();

  const detail = useMemo(() => {
    if (call.input && call.result) {
      return `${call.input}\n${'─'.repeat(8)}\n${call.result}`;
    }
    return call.input || call.result || '';
  }, [call.input, call.result]);

  return (
    <div className="border-b border-gray-200/70 dark:border-white/[0.08] last:border-b-0">
      <button
        type="button"
        onClick={onToggle}
        className="w-full flex items-center gap-2 px-3 py-2 text-left hover:bg-gray-50 dark:hover:bg-white/[0.04] transition-colors"
        aria-expanded={expanded}
      >
        {expanded
          ? <ChevronDown size={13} className="text-gray-400 shrink-0" />
          : <ChevronRight size={13} className="text-gray-400 shrink-0" />}
        <Wrench size={13} className={cn('shrink-0', call.running ? 'text-amber-500 dark:text-amber-400' : 'text-amber-500/70 dark:text-amber-400/70')} />
        <span className="text-xs font-medium text-gray-700 dark:text-gray-200 truncate flex-1 min-w-0">
          {call.tool}
        </span>
        {badge}
        {!expanded && preview && (
          <span className="hidden sm:inline text-[11px] text-gray-400 dark:text-gray-500 truncate max-w-[40%]">
            {preview}
          </span>
        )}
      </button>
      {expanded && (
        <pre className="mx-3 mb-2 px-3 py-2 rounded-md bg-[#fafafa] dark:bg-[#0d1117] border border-gray-200 dark:border-gray-700/60 text-[12px] leading-[1.55] font-mono whitespace-pre-wrap break-words text-gray-800 dark:text-gray-100 max-h-[60vh] overflow-auto">
          {detail}
        </pre>
      )}
    </div>
  );
}

/** Collapsed representation of N consecutive same-tool invocations. */
function GroupRow({
  group, expanded, onToggle, isLastUnit, isLive, detailExpanded, onToggleDetail,
}: {
  group: ToolGroup;
  expanded: boolean;
  onToggle: () => void;
  isLastUnit: boolean;
  isLive: boolean;
  detailExpanded: Set<string>;
  onToggleDetail: (key: string) => void;
}) {
  return (
    <div className="border-b border-gray-200/70 dark:border-white/[0.08] last:border-b-0">
      <button
        type="button"
        onClick={onToggle}
        className="w-full flex items-center gap-2 px-3 py-2 text-left hover:bg-gray-50 dark:hover:bg-white/[0.04] transition-colors"
        aria-expanded={expanded}
      >
        {expanded
          ? <ChevronDown size={13} className="text-gray-400 shrink-0" />
          : <ChevronRight size={13} className="text-gray-400 shrink-0" />}
        <Wrench size={13} className="text-amber-500 dark:text-amber-400 shrink-0" />
        <span className="text-xs font-medium text-gray-700 dark:text-gray-200 truncate flex-1 min-w-0">
          {group.tool}
        </span>
        <span className="text-[10px] font-medium tabular-nums text-gray-500 dark:text-gray-400 bg-gray-100 dark:bg-white/[0.08] px-1.5 py-0.5 rounded shrink-0">
          ×{group.calls.length}
        </span>
        <GroupBadge group={group} isLive={isLive && isLastUnit} />
      </button>
      {expanded && (
        <div className="border-t border-gray-200/60 dark:border-white/[0.06]">
          {group.calls.map((call, i) => (
            <CallRow
              key={call.key}
              call={call}
              expanded={detailExpanded.has(call.key)}
              onToggle={() => onToggleDetail(call.key)}
              isLast={i === group.calls.length - 1}
              isLive={isLive && isLastUnit}
            />
          ))}
        </div>
      )}
    </div>
  );
}

// ── main component ──

export interface ProgressCardProps {
  payload: ProgressCardPayload;
}

export default function ProgressCard({ payload }: ProgressCardProps) {
  const { t } = useTranslation();
  const items = payload.items;
  const isLive = payload.state === 'running' || !payload.state;

  // Pair tool_use/tool_result entries and group consecutive same-tool calls.
  const units = useMemo(() => buildUnits(items), [items]);

  // Sets of stable keys currently expanded: group rows and per-call detail
  // rows share this state. Empty by default so the chat stays compact while
  // the agent is still producing output.
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set());

  const toggle = useCallback((key: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }, []);

  const totalCalls = useMemo(
    () => units.reduce((n, u) => (u.type === 'group' ? n + u.group.calls.length : n), 0),
    [units],
  );

  const headerLabel = payload.agent
    ? t('progress.progressFor', 'Progress · {{agent}}', { agent: payload.agent })
    : t('progress.progress', 'Progress');

  return (
    <div className="rounded-lg border border-gray-200 dark:border-white/[0.08] bg-white/60 dark:bg-white/[0.02] overflow-hidden">
      <div className="flex items-center justify-between gap-2 px-3 py-1.5 border-b border-gray-200/70 dark:border-white/[0.08] bg-gray-50/70 dark:bg-white/[0.03]">
        <div className="flex items-center gap-1.5 min-w-0">
          {isLive
            ? <Loader2 size={12} className="text-accent animate-spin shrink-0" />
            : payload.state === 'failed'
              ? <X size={12} className="text-red-500 shrink-0" />
              : <Check size={12} className="text-emerald-500 shrink-0" />}
          <span className="text-[11px] font-semibold text-gray-600 dark:text-gray-300 uppercase tracking-wide truncate">
            {headerLabel}
          </span>
          <span className="text-[10px] text-gray-400 dark:text-gray-500 tabular-nums">
            {t('progress.entryCount', '{{n}} entries', { n: items.length })}
          </span>
        </div>
        {totalCalls > 0 && (
          <span className="text-[10px] text-gray-400 dark:text-gray-500 tabular-nums shrink-0">
            {t('progress.toolCalls', '{{n}} tool calls', { n: totalCalls })}
          </span>
        )}
      </div>
      {payload.truncated && (
        <div className="px-3 py-1 text-[10px] text-gray-500 dark:text-gray-400 bg-amber-50/60 dark:bg-amber-900/10 border-b border-gray-200/70 dark:border-white/[0.08]">
          {t('progress.truncatedHint', 'Older entries trimmed; showing latest only.')}
        </div>
      )}
      <div>
        {units.map((unit, i) => {
          const isLastUnit = i === units.length - 1;
          if (unit.type === 'entry') {
            return (
              <BlockRow
                key={unit.key}
                item={unit.entry}
                expanded={expanded.has(unit.key)}
                onToggle={() => toggle(unit.key)}
                isLast={isLastUnit}
                isLive={isLive}
              />
            );
          }
          const { group } = unit;
          // Single-call groups render as one directly expandable row.
          if (group.calls.length === 1) {
            return (
              <CallRow
                key={unit.key}
                call={group.calls[0]}
                expanded={expanded.has(group.calls[0].key)}
                onToggle={() => toggle(group.calls[0].key)}
                isLast
                isLive={isLive && isLastUnit}
              />
            );
          }
          return (
            <GroupRow
              key={unit.key}
              group={group}
              expanded={expanded.has(unit.key)}
              onToggle={() => toggle(unit.key)}
              isLastUnit={isLastUnit}
              isLive={isLive}
              detailExpanded={expanded}
              onToggleDetail={toggle}
            />
          );
        })}
      </div>
    </div>
  );
}
