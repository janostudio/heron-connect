import { useCallback, useEffect, useRef, useState } from 'react';
import { Loader2 } from 'lucide-react';
import { cn } from '@/lib/utils';
import { PREVIEW_LIMITS, formatBytes } from '@/lib/filePreview';

/**
 * Windowed text viewer shared by the admin preview and the public share page.
 *
 * It never holds a rendered copy of the whole file: slices are appended to a
 * line array, and only the lines inside the viewport (plus a small overscan)
 * are mounted. That is what keeps a 16 MB log at ~100 DOM nodes instead of
 * hundreds of thousands.
 *
 * Lines are rendered `white-space: pre` so every row is exactly LINE_HEIGHT
 * tall — virtualization requires fixed heights, and a single wrapped line would
 * desynchronize the offset math. A pathologically long line (minified JSON, a
 * one-line binary dump) is cut at maxLineChars instead of being laid out.
 */

export interface PreviewChunk {
  bytes: Uint8Array;
  totalSize: number;
  offset: number;
  returned: number;
  truncated: boolean;
}

export interface TextPreviewLabels {
  loading: string;
  loadMore: string;
  download: string;
  failed: string;
}

interface Props {
  totalSize: number;
  /** Reads one slice. The caller owns authentication (admin token / share token). */
  readChunk: (offset: number, limit: number, signal?: AbortSignal) => Promise<PreviewChunk>;
  labels: TextPreviewLabels;
  onDownload?: () => void;
  className?: string;
}

const LINE_HEIGHT = 20;
const OVERSCAN = 20;

export default function TextPreview({ totalSize, readChunk, labels, onDownload, className }: Props) {
  const [lines, setLines] = useState<string[]>([]);
  const [loaded, setLoaded] = useState(0);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [done, setDone] = useState(false);
  const [scrollTop, setScrollTop] = useState(0);
  const [viewport, setViewport] = useState(480);

  const scrollRef = useRef<HTMLDivElement | null>(null);
  // Bytes already consumed, kept in a ref so concurrent load calls cannot read
  // a stale offset from a previous render.
  const loadedRef = useRef(0);
  // Trailing partial line from the previous slice: a slice boundary is a byte
  // offset and may land mid-line, so the tail is carried over rather than shown.
  const pendingRef = useRef('');
  // Streaming decoder: a slice boundary may also land mid-character, and only a
  // stream decoder carries the incomplete byte sequence into the next slice.
  const decoderRef = useRef(new TextDecoder('utf-8'));
  const inFlightRef = useRef(false);
  // Unmount guard: a slice that lands after the file was switched away must not
  // write to state, and its request is aborted rather than left to finish.
  const aliveRef = useRef(true);
  const abortRef = useRef<AbortController | null>(null);

  const load = useCallback(async () => {
    if (inFlightRef.current || done) return;
    inFlightRef.current = true;
    setLoading(true);
    setError('');
    const offset = loadedRef.current;
    const controller = new AbortController();
    abortRef.current = controller;
    try {
      const chunk = await readChunk(
        offset,
        offset === 0 ? PREVIEW_LIMITS.firstChunk : PREVIEW_LIMITS.nextChunk,
        controller.signal,
      );
      if (!aliveRef.current) return;
      const text = decoderRef.current.decode(chunk.bytes, { stream: true });
      const parts = (pendingRef.current + text).split('\n');
      pendingRef.current = parts.pop() ?? '';
      if (!chunk.truncated && pendingRef.current) {
        // The file ended without a trailing newline: the tail is a real line.
        parts.push(pendingRef.current);
        pendingRef.current = '';
      }
      if (parts.length > 0) setLines(prev => prev.concat(parts));
      loadedRef.current = chunk.offset + chunk.returned;
      setLoaded(loadedRef.current);
      setDone(!chunk.truncated);
    } catch (e) {
      if (!aliveRef.current || controller.signal.aborted) return;
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      inFlightRef.current = false;
      abortRef.current = null;
      if (aliveRef.current) setLoading(false);
    }
  }, [done, readChunk]);

  useEffect(() => {
    aliveRef.current = true;
    void load();
    return () => {
      aliveRef.current = false;
      abortRef.current?.abort();
    };
    // Intentionally once per mounted file: readChunk is stabilised by the
    // caller, and the component is keyed by file path so switching files
    // remounts it with a fresh decoder.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const el = scrollRef.current;
    if (!el || typeof ResizeObserver === 'undefined') return;
    const ro = new ResizeObserver(() => setViewport(el.clientHeight));
    ro.observe(el);
    setViewport(el.clientHeight);
    return () => ro.disconnect();
  }, []);

  const handleScroll = useCallback((e: React.UIEvent<HTMLDivElement>) => {
    const el = e.currentTarget;
    setScrollTop(el.scrollTop);
    // Pull the next slice before the user reaches the end, so scrolling through
    // a large file does not stall on a spinner every screenful.
    if (el.scrollHeight - el.scrollTop - el.clientHeight < 400) void load();
  }, [load]);

  const total = lines.length;
  const start = Math.max(0, Math.floor(scrollTop / LINE_HEIGHT) - OVERSCAN);
  const visibleCount = Math.ceil(viewport / LINE_HEIGHT) + OVERSCAN * 2;
  const end = Math.min(total, start + visibleCount);
  const pct = totalSize > 0 ? Math.min(100, Math.round((loaded / totalSize) * 100)) : 0;

  if (lines.length === 0 && loading) {
    return (
      <div className={cn('flex items-center justify-center gap-2 py-10 text-sm text-gray-500 dark:text-gray-400', className)}>
        <Loader2 size={18} className="animate-spin" />
        {labels.loading}
      </div>
    );
  }

  return (
    <div className={cn('flex h-full min-h-0 flex-col', className)}>
      <div
        ref={scrollRef}
        onScroll={handleScroll}
        className="min-h-0 flex-1 overflow-auto rounded-lg border border-gray-200 bg-[#fafafa] font-mono text-[12px] leading-5 dark:border-gray-700/60 dark:bg-[#0d1117]"
      >
        <div style={{ height: total * LINE_HEIGHT, position: 'relative' }}>
          <div style={{ transform: `translateY(${start * LINE_HEIGHT}px)` }}>
            {lines.slice(start, end).map((line, i) => {
              const number = start + i + 1;
              const text = line.length > PREVIEW_LIMITS.maxLineChars
                ? `${line.slice(0, PREVIEW_LIMITS.maxLineChars)}…`
                : line;
              return (
                <div key={number} data-testid="preview-line" className="flex px-2" style={{ height: LINE_HEIGHT }}>
                  <span className="w-12 shrink-0 select-none pr-3 text-right text-gray-400 dark:text-gray-600">
                    {number}
                  </span>
                  <span className="whitespace-pre text-gray-800 dark:text-gray-100">{text}</span>
                </div>
              );
            })}
          </div>
        </div>
      </div>

      <div className="mt-2 flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs text-gray-500 dark:text-gray-400">
        <span>
          {formatBytes(loaded)} / {formatBytes(totalSize)} · {pct}%
        </span>
        {!done && (
          <button
            type="button"
            onClick={() => void load()}
            disabled={loading}
            className="rounded-lg border border-gray-200 px-2.5 py-1 text-gray-600 transition-colors hover:bg-gray-100 disabled:opacity-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-white/[0.06]"
          >
            {loading ? labels.loading : labels.loadMore}
          </button>
        )}
        {onDownload && (
          <button
            type="button"
            onClick={onDownload}
            className="rounded-lg border border-gray-200 px-2.5 py-1 text-gray-600 transition-colors hover:bg-gray-100 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-white/[0.06]"
          >
            {labels.download}
          </button>
        )}
        {error && <span className="text-red-500">{labels.failed}: {error}</span>}
      </div>
    </div>
  );
}
