import React, { useState, useMemo, memo } from 'react';
import { Copy, Check, X, FileText } from 'lucide-react';
import api from '@/api/client';
import { cn, copyText } from '@/lib/utils';
import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import rehypeHighlight from 'rehype-highlight';
import SelectList from './SelectList';
import type { HistoryAttachment } from './chatMessage';
import { parseListItemText } from './chatHelpers';

// ── Memoized markdown leaf renderers ─────────────────────────
//
// Every renderer here is wrapped in React.memo and defined at module scope.
// The transcript renders one row per message; while a turn streams, only the
// row whose `msg` object actually changed re-renders. Without this, every
// reply_stream delta re-parsed the markdown of the ENTIRE transcript
// (react-markdown + remark-gfm + rehype-highlight), which is what made long
// conversations progressively laggy.
//
// The plugin arrays are module-level for the same reason: a fresh `[remarkGfm]`
// literal per render defeats react-markdown's internal memoization and re-runs
// the whole unified pipeline on every render.
const REMARK_PLUGINS = [remarkGfm];
const REHYPE_PLUGINS = [rehypeHighlight];

function CopyButtonInner({ code }: { code: string }) {
  // null = idle, 'ok' = copied, 'fail' = copy attempt failed
  const [state, setState] = useState<'ok' | 'fail' | null>(null);
  const handleCopy = () => {
    copyText(code).then((ok) => {
      setState(ok ? 'ok' : 'fail');
      setTimeout(() => setState(null), 2000);
    });
  };
  return (
    <button
      onClick={handleCopy}
      className="absolute top-2 right-2 p-1.5 rounded-md bg-gray-200/80 dark:bg-gray-700/80 hover:bg-gray-300 dark:hover:bg-gray-600 text-gray-500 dark:text-gray-400 opacity-0 group-hover:opacity-100 transition-opacity z-10"
    >
      {state === 'ok' ? <Check size={12} /> : state === 'fail' ? <X size={12} className="text-red-500" /> : <Copy size={12} />}
    </button>
  );
}
export const CopyButton = memo(CopyButtonInner);

// react-markdown (via hast-util-to-jsx-runtime) hands every custom component a
// `node` prop holding the hast node. Spreading the rest props straight onto a
// DOM element therefore leaks `node="[object Object]"` into the markup. React
// tolerates the unknown attribute but warns, and the stringified object is
// pure noise in the DOM. Destructuring it out here keeps the spread clean.
function PreBlockInner({ children, node: _node, ...props }: React.HTMLAttributes<HTMLPreElement> & { node?: unknown }) {
  const codeEl = (children as any)?.props;
  const lang = codeEl?.className?.replace(/^language-/, '') || '';
  const code = typeof codeEl?.children === 'string' ? codeEl.children.replace(/\n$/, '') : '';
  return (
    <div className="not-prose relative group my-4">
      {lang && (
        <div className="absolute top-0 left-0 px-2.5 py-1 text-[10px] font-medium uppercase tracking-wider text-gray-400 dark:text-gray-500 bg-gray-100 dark:bg-gray-800 rounded-tl-lg rounded-br-lg border-b border-r border-gray-200 dark:border-gray-700 font-mono">
          {lang}
        </div>
      )}
      <CopyButton code={code} />
      <pre className="overflow-x-auto rounded-lg bg-[#fafafa] dark:bg-[#0d1117] border border-gray-200 dark:border-gray-700/60 p-4 pt-8 text-[13px] leading-[1.6] font-mono" {...props}>
        {children}
      </pre>
    </div>
  );
}
const PreBlock = memo(PreBlockInner);

function InlineCodeInner({ children, className, node: _node, ...props }: React.HTMLAttributes<HTMLElement> & { node?: unknown }) {
  if (className) return <code className={className} {...props}>{children}</code>;
  // The bubble is `bg-white` / `dark:bg-gray-800/80`; pick inline-code tones
  // that read against both: light = subtle slate, dark = distinctly darker
  // than the bubble (so the box is obvious) with brighter pink text.
  return (
    <code className="px-1.5 py-0.5 rounded-md bg-slate-100 dark:bg-black/35 text-pink-600 dark:text-pink-300 text-[0.875em] font-mono border border-slate-200/70 dark:border-white/10" {...props}>
      {children}
    </code>
  );
}
const InlineCode = memo(InlineCodeInner);

// Wrap tables in a horizontal-scroll container so wide markdown tables don't
// overflow the narrow chat bubble on mobile.
function TableBlockInner({ children }: React.HTMLAttributes<HTMLTableElement>) {
  return (
    <div className="overflow-x-auto -mx-1 px-1">
      <table className="w-full">{children}</table>
    </div>
  );
}
const TableBlock = memo(TableBlockInner);

const MARKDOWN_CLASS = cn(
  'prose max-w-none dark:prose-invert',
  'prose-headings:font-semibold prose-headings:tracking-tight',
  'prose-h1:text-xl prose-h1:mt-5 prose-h1:mb-3 prose-h1:pb-1.5 prose-h1:border-b prose-h1:border-gray-200 dark:prose-h1:border-gray-700',
  'prose-h2:text-lg prose-h2:mt-5 prose-h2:mb-2',
  'prose-h3:text-base prose-h3:mt-4 prose-h3:mb-2',
  'prose-p:my-2.5 prose-p:leading-relaxed',
  'prose-li:my-0.5', 'prose-ul:my-2 prose-ol:my-2',
  'prose-a:text-accent prose-a:no-underline hover:prose-a:underline hover:prose-a:decoration-2 hover:prose-a:underline-offset-2 prose-a:break-all',
  'prose-strong:text-gray-900 dark:prose-strong:text-white prose-strong:font-semibold',
  'prose-blockquote:border-l-[3px] prose-blockquote:border-accent/40 prose-blockquote:bg-accent/[0.03] prose-blockquote:rounded-r-lg prose-blockquote:py-0.5 prose-blockquote:px-4 prose-blockquote:my-3 prose-blockquote:not-italic prose-blockquote:text-gray-600 dark:prose-blockquote:text-gray-300',
  'prose-hr:my-5 prose-hr:border-gray-200 dark:prose-hr:border-gray-700',
  'prose-table:text-sm prose-th:bg-gray-50 dark:prose-th:bg-gray-800 prose-th:px-3 prose-th:py-2 prose-td:px-3 prose-td:py-2',
  'prose-img:rounded-lg prose-img:shadow-sm',
);

// Component map for callers WITHOUT a file-open handler (e.g. a markdown file
// preview). `a` must be the string `'a'`, never `undefined`: hast-util-to-jsx-
// runtime reads explicit undefined values out of the components map and
// forwards them straight to React.createElement, which then throws #130
// ("Element type is invalid: ... got: undefined") and unmounts the whole tree
// as soon as the markdown contains a link.
const MD_COMPONENTS_PLAIN = {
  pre: PreBlock as any,
  code: InlineCode as any,
  table: TableBlock as any,
  a: 'a',
} as any;

// Builds a components map that routes /api/v1/files/ links through `onOpenFile`.
//
// react-markdown resolves custom components by tag name only — arbitrary props
// passed to <Markdown> are NOT forwarded to them (hast-util-to-jsx-runtime
// builds props from the markdown node's own properties). So the handler has to
// be captured in a closure here. The result is memoized per handler identity:
// the parent passes a useCallback-stable handler, so this map is built once and
// react-markdown is not handed a fresh `components` object on every render.
function buildFileComponents(onOpenFile: (path: string, fileName: string) => void) {
  return {
    pre: PreBlock as any,
    code: InlineCode as any,
    table: TableBlock as any,
    a: ({ href, children, ...props }: any) => (
      <a
        {...props}
        href={href}
        onClick={(e: React.MouseEvent) => {
          if (href && href.startsWith('/api/v1/files/')) {
            e.preventDefault();
            const parts = href.split('/');
            const fileName = parts[parts.length - 1] || 'file';
            onOpenFile(href, decodeURIComponent(fileName));
          }
        }}
      >
        {children}
      </a>
    ),
  } as any;
}

function RenderMarkdownInner({ content, onOpenFile }: { content: string; onOpenFile?: (path: string, fileName: string) => void }) {
  const components = useMemo(
    () => (onOpenFile ? buildFileComponents(onOpenFile) : MD_COMPONENTS_PLAIN),
    [onOpenFile],
  );
  return (
    <div className={MARKDOWN_CLASS}>
      <Markdown remarkPlugins={REMARK_PLUGINS} rehypePlugins={REHYPE_PLUGINS} components={components}>
        {content}
      </Markdown>
    </div>
  );
}

export const RenderMarkdown = memo(RenderMarkdownInner);

// ── Inline-bold renderer (no full markdown parse) ────────────

export function InlineMd({ text }: { text: string }) {
  const parts = text.split(/(\*\*[^*]+\*\*)/g);
  return (
    <>
      {parts.map((p, i) =>
        p.startsWith('**') && p.endsWith('**')
          ? <strong key={i} className="font-semibold text-gray-900 dark:text-white">{p.slice(2, -2)}</strong>
          : <span key={i}>{p}</span>
      )}
    </>
  );
}

// ── Card / buttons / file / image blocks ─────────────────────

function CardElementInner({ el, onAction }: { el: any; onAction: (v: string) => void }) {
  if (el.type === 'markdown') return <RenderMarkdown content={el.content} />;
  if (el.type === 'divider') return <div className="border-t border-gray-200/60 dark:border-gray-700/40" />;
  if (el.type === 'note') return <p className="text-[11px] text-gray-400 dark:text-gray-500">{el.text}</p>;
  if (el.type === 'actions') {
    // `layout` mirrors core.CardActionLayout: "equal_columns" means the row's
    // buttons should split the available width evenly (used for the
    // allow/deny pair so neither looks more inviting than the other),
    // "row" leaves them at their natural size. Unknown/absent values fall
    // back to the wrapping row, which is what every card did before layout
    // was honoured at all.
    const equalColumns = el.layout === 'equal_columns';
    return (
      <div className={cn('gap-2', equalColumns ? 'flex' : 'flex flex-wrap')}>
        {el.buttons?.map((btn: any, j: number) => (
          <button key={j} onClick={() => onAction(btn.value)} className={cn(
            'px-3 py-1.5 rounded-lg text-xs font-medium transition-all duration-150',
            equalColumns && 'flex-1 min-w-0 truncate',
            btn.btn_type === 'primary' ? 'bg-accent text-black hover:bg-accent-dim shadow-sm' :
            btn.btn_type === 'danger' ? 'bg-red-500/10 text-red-600 dark:text-red-400 hover:bg-red-500/20' :
            'bg-gray-100 dark:bg-gray-800 text-gray-600 dark:text-gray-300 hover:bg-gray-200 dark:hover:bg-gray-700',
          )}>
            {btn.text}
          </button>
        ))}
      </div>
    );
  }
  if (el.type === 'list_item') {
    const parsed = parseListItemText(el.text);
    const isCommand = parsed.cmd.startsWith('/');
    return (
      <button
        onClick={() => onAction(el.btn_value)}
        className="w-full flex items-center gap-3 py-2 text-left group"
      >
        {isCommand ? (
          <>
            <code className="shrink-0 w-20 text-xs font-mono font-medium text-accent">{parsed.cmd}</code>
            <span className="flex-1 text-sm text-gray-500 dark:text-gray-400 truncate">{parsed.desc}</span>
          </>
        ) : (
          <span className="flex-1 text-sm text-gray-700 dark:text-gray-300 truncate min-w-0">
            <InlineMd text={el.text} />
          </span>
        )}
        <span className={cn(
          'shrink-0 px-2 py-0.5 rounded-md text-[11px] font-medium transition-all',
          el.btn_type === 'primary'
            ? 'bg-accent/15 text-accent group-hover:bg-accent/25'
            : 'text-gray-400 dark:text-gray-500 bg-gray-100 dark:bg-gray-800 group-hover:bg-accent/15 group-hover:text-accent',
        )}>
          {el.btn_text}
        </span>
      </button>
    );
  }
  if (el.type === 'select') {
    return (
      <SelectList
        options={(el.options || []).map((opt: any) => ({ value: String(opt.value), text: opt.text }))}
        value={el.init_value != null ? String(el.init_value) : undefined}
        onChange={(v) => onAction(v)}
      />
    );
  }
  return null;
}
const CardElement = memo(CardElementInner);

function CardBlockInner({ card, onAction }: { card: any; onAction: (v: string) => void }) {
  if (!card) return null;
  return (
    <div className="space-y-3">
      {card.header?.title && (
        <div className="text-sm font-semibold text-gray-900 dark:text-white">{card.header.title}</div>
      )}
      {card.elements?.map((el: any, i: number) => (
        <CardElement key={i} el={el} onAction={onAction} />
      ))}
    </div>
  );
}
export const CardBlock = memo(CardBlockInner);

function ButtonsBlockInner({ content, buttons, onAction }: { content: string; buttons: { text: string; data: string }[][]; onAction: (v: string) => void }) {
  return (
    <div className="space-y-3">
      <RenderMarkdown content={content} />
      {buttons.map((row, i) => (
        <div key={i} className="flex flex-wrap gap-2">
          {row.map((btn, j) => (
            <button key={j} onClick={() => onAction(btn.data)} className="px-3 py-1.5 rounded-lg text-xs font-medium bg-accent text-black hover:bg-accent-dim transition-colors">
              {btn.text}
            </button>
          ))}
        </div>
      ))}
    </div>
  );
}
export const ButtonsBlock = memo(ButtonsBlockInner);

function FileBlockInner({ name, size }: { name: string; size?: number }) {
  return (
    <div className="flex items-center gap-2 px-3 py-2 rounded-lg bg-gray-50 dark:bg-gray-800 border border-gray-200 dark:border-gray-700">
      <FileText size={16} className="text-gray-400 shrink-0" />
      <div className="min-w-0">
        <div className="text-sm font-medium text-gray-900 dark:text-white truncate">{name}</div>
        {size !== undefined && <div className="text-xs text-gray-400">{(size / 1024).toFixed(1)} KB</div>}
      </div>
    </div>
  );
}
export const FileBlock = memo(FileBlockInner);

function ImageBlockInner({ url }: { url: string }) {
  return <img src={url} alt="" className="max-w-sm rounded-lg border border-gray-200 dark:border-gray-700 shadow-sm" />;
}
export const ImageBlock = memo(ImageBlockInner);

// HistoryImage loads a persisted history attachment over the authenticated
// /files endpoint and renders it as a thumbnail. Uses api.file (Bearer token)
// rather than a bare <img src> URL, since same-origin <img> tags do not send
// the Authorization header.
//
// Two safeguards against the history-attachment heap growth that made long
// conversations heavy:
//  - the bytes are exposed as a blob object URL (revoked on unmount/change)
//    instead of a base64 data: URL, which avoids ~33% inflation and lets the
//    browser keep the bitmap off-heap;
//  - the fetch is deferred until the thumbnail scrolls into view, so a
//    200-message history does not download every image up front.
// Returns null silently if the file is gone (e.g. removed from disk).
function HistoryImageInner({ project, att }: { project: string; att: HistoryAttachment }) {
  const [src, setSrc] = useState('');
  const [visible, setVisible] = useState(false);
  const holderRef = React.useRef<HTMLDivElement | null>(null);

  // Defer the fetch until the holder enters the viewport.
  React.useEffect(() => {
    const el = holderRef.current;
    if (!el || visible) return;
    if (typeof IntersectionObserver === 'undefined') {
      setVisible(true);
      return;
    }
    const io = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) {
        setVisible(true);
        io.disconnect();
      }
    }, { rootMargin: '200px' });
    io.observe(el);
    return () => io.disconnect();
  }, [visible]);

  React.useEffect(() => {
    if (!visible) return;
    let alive = true;
    let objectUrl = '';
    const filePath = `/files/${project}/${att.path}`;
    api.file(filePath).then((res) => {
      if (!alive) return;
      if (!res.ok) {
        setSrc('');
        return;
      }
      objectUrl = URL.createObjectURL(res.blob);
      setSrc(objectUrl);
    }).catch(() => { if (alive) setSrc(''); });
    return () => {
      alive = false;
      if (objectUrl) URL.revokeObjectURL(objectUrl);
    };
  }, [project, att.path, visible]);

  if (!src) {
    return <div ref={holderRef} className="w-[120px] h-[80px] rounded-lg bg-gray-100 dark:bg-gray-800/60" />;
  }
  return <img ref={holderRef as any} src={src} alt={att.name} className="max-w-[220px] max-h-[220px] rounded-lg border border-gray-200 dark:border-gray-700 object-cover" />;
}
export const HistoryImage = memo(HistoryImageInner);

// stripReplyFooter removes the trailing `*model · usage · path*` runtime footer
// that heron-connect appends to assistant replies, so copying the message does
// not carry runtime metadata into the clipboard.
//
// Conservative by design: this is a heuristic on the last line, and a false
// positive silently deletes real content. In particular a reply whose ONLY line
// is italic (e.g. "*emphasis*") must not be stripped down to an empty string.
export function stripReplyFooter(text: string): string {
  if (!text) return text;
  const lines = text.split('\n');
  // Footer is the last non-empty line, wrapped in a single pair of asterisks.
  let lastIdx = lines.length - 1;
  while (lastIdx >= 0 && lines[lastIdx].trim() === '') lastIdx--;
  if (lastIdx <= 0) return text;   // nothing above it → not a footer
  const footer = lines[lastIdx].trim();
  if (footer.startsWith('*') && footer.endsWith('*') && footer.length > 2 && !footer.slice(1, -1).includes('*')) {
    lines.splice(lastIdx, 1);
    while (lines.length > 0 && lines[lines.length - 1].trim() === '') lines.pop();
    return lines.join('\n');
  }
  return text;
}

function MsgCopyButtonInner({ text, stripFooter }: { text: string; stripFooter?: boolean }) {
  // null = idle, 'ok' = copied, 'fail' = copy attempt failed
  const [state, setState] = useState<'ok' | 'fail' | null>(null);
  const handleCopy = () => {
    copyText(stripFooter ? stripReplyFooter(text) : text).then((ok) => {
      setState(ok ? 'ok' : 'fail');
      setTimeout(() => setState(null), 2000);
    });
  };
  return (
    <button
      onClick={handleCopy}
      // Always faintly visible (not hover-only) so the copy affordance is
      // discoverable; full opacity on hover. Also works on touch devices.
      className="absolute -bottom-3 right-2 p-1 rounded-md bg-gray-100/90 dark:bg-gray-700/90 hover:bg-gray-200 dark:hover:bg-gray-600 text-gray-400 hover:text-gray-600 dark:hover:text-gray-200 opacity-60 group-hover/msg:opacity-100 transition-opacity shadow-sm"
      title="Copy"
    >
      {state === 'ok' ? <Check size={12} /> : state === 'fail' ? <X size={12} className="text-red-500" /> : <Copy size={12} />}
    </button>
  );
}
export const MsgCopyButton = memo(MsgCopyButtonInner);
