import { useEffect, useState, useRef, useCallback, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { useParams, useNavigate, useLocation, Link } from 'react-router-dom';
import {
  ArrowLeft, Send, User, Bot, Circle, WifiOff,
  Copy, Check, FileText, Image as ImageIcon, Loader2, Download, FileDown,
  Slash, ChevronDown, Square, Clock, X, Folder, FolderOpen, ChevronLeft, ChevronRight, ArrowUp,
  Pin, PinOff, Pencil, Paperclip,
} from 'lucide-react';
import { Badge, Button } from '@/components/ui';
import { listSessions, getSession, sessionTitle, updateSession, sortSessions, type Session, type SessionDetail } from '@/api/sessions';
import api from '@/api/client';
import { newConvKey } from '@/lib/webSessionKey';
import {
  useBridgeSocket, fetchBridgeConfig,
  type BridgeConfig, type BridgeIncoming, type BridgeStatus,
} from '@/hooks/useBridgeSocket';
import CommandPalette, { type SlashCommand, slashCommands } from './CommandPalette';
import SessionDrawer from './SessionDrawer';
import CommandResultPanel, { type CommandResult } from './CommandResultPanel';
import RenameSessionModal from './RenameSessionModal';
import ProgressCard, { parseProgressCard, type ProgressCardPayload } from './ProgressCard';
import SelectList from './SelectList';
import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import rehypeHighlight from 'rehype-highlight';
import { cn, copyText, loadLS, saveLS } from '@/lib/utils';

// ── Markdown renderers ───────────────────────────────────────

function CopyButton({ code }: { code: string }) {
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

function PreBlock({ children, ...props }: React.HTMLAttributes<HTMLPreElement>) {
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

function InlineCode({ children, className, ...props }: React.HTMLAttributes<HTMLElement>) {
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

function RenderMarkdown({ content, onOpenFile }: { content: string; onOpenFile?: (path: string, fileName: string) => void }) {
  return (
    <div className={cn(
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
    )}>
      <Markdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeHighlight]} components={{
        pre: PreBlock as any,
        code: InlineCode as any,
        // When the caller has no file-open handler (e.g. FileContentView
        // previewing a markdown file), fall back to the string `'a'` rather
        // than `undefined`. hast-util-to-jsx-runtime reads explicit undefined
        // values out of the components map and forwards them straight to
        // React.createElement, which then throws #130 ("Element type is
        // invalid: ... got: undefined") and unmounts the whole tree as soon
        // as the markdown contains a link.
        a: onOpenFile ? ({ href, children, ...props }: React.AnchorHTMLAttributes<HTMLAnchorElement>) => (
          <a
            {...props}
            href={href}
            onClick={(e) => {
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
        ) : 'a',
        // Wrap tables in a horizontal-scroll container so wide markdown tables
        // don't overflow the narrow chat bubble on mobile.
        table: ({ children }: React.HTMLAttributes<HTMLTableElement>) => (
          <div className="overflow-x-auto -mx-1 px-1">
            <table className="w-full">{children}</table>
          </div>
        ),
      } as any}>
        {content}
      </Markdown>
    </div>
  );
}

// ── Chat message types ───────────────────────────────────────

// A file the user has picked for upload in the composer. `dataUrl` is the
// base64 data: URL read via FileReader; we strip the prefix when sending.
interface PickItem {
  id: string;
  name: string;
  mime_type: string;
  dataUrl: string;
  size: number;
  kind: 'image' | 'file';
}

interface ChatMsg {
  id: string;
  role: 'user' | 'assistant' | 'system';
  content: string;
  format?: 'text' | 'markdown' | 'card' | 'buttons' | 'image' | 'file';
  card?: any;
  buttons?: { text: string; data: string }[][];
  imageUrl?: string;
  fileName?: string;
  fileSize?: number;
  // Local media attached by the user in this message (rendered before content).
  localMedia?: PickItem[];
  streaming?: boolean;
  previewHandle?: string;
  timestamp?: string;
  // When the message body is a structured agent-progress payload (sent by
  // the engine with the __heron_connect_progress_card_v1__: prefix), the
  // parsed payload lives here and takes precedence over `content` for
  // rendering. `content` is still kept verbatim so the message survives a
  // downgrade (server without the prefix, history replay, etc.).
  progressCard?: ProgressCardPayload;
}

// ── Helpers ──────────────────────────────────────────────────

function parseListItemText(text: string): { cmd: string; desc: string } {
  const m = text.match(/^\*\*(.+?)\*\*\s*(.*)/);
  if (m) return { cmd: m[1], desc: m[2] };
  const sp = text.indexOf(' ');
  if (sp > 0) return { cmd: text.slice(0, sp), desc: text.slice(sp + 1) };
  return { cmd: text, desc: '' };
}

function InlineMd({ text }: { text: string }) {
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

// ── Card renderer (flat, clean style for in-stream cards) ────

function CardBlock({ card, onAction }: { card: any; onAction: (v: string) => void }) {
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

function CardElement({ el, onAction }: { el: any; onAction: (v: string) => void }) {
  if (el.type === 'markdown') return <RenderMarkdown content={el.content} />;
  if (el.type === 'divider') return <div className="border-t border-gray-200/60 dark:border-gray-700/40" />;
  if (el.type === 'note') return <p className="text-[11px] text-gray-400 dark:text-gray-500">{el.text}</p>;
  if (el.type === 'actions') {
    return (
      <div className="flex flex-wrap gap-2">
        {el.buttons?.map((btn: any, j: number) => (
          <button key={j} onClick={() => onAction(btn.value)} className={cn(
            'px-3 py-1.5 rounded-lg text-xs font-medium transition-all duration-150',
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

function ButtonsBlock({ content, buttons, onAction }: { content: string; buttons: { text: string; data: string }[][]; onAction: (v: string) => void }) {
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

function FileBlock({ name, size }: { name: string; size?: number }) {
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

function ImageBlock({ url }: { url: string }) {
  return <img src={url} alt="" className="max-w-sm rounded-lg border border-gray-200 dark:border-gray-700 shadow-sm" />;
}

// ── File preview (local agent-generated files, served over HTTP) ──

// fileIsPreviewable reports whether a file can be shown inline in the web UI
// rather than only offered for download.
function fileIsPreviewable(fileName: string, contentType: string): boolean {
  const ext = (fileName.split('.').pop() || '').toLowerCase();
  const ct = contentType.toLowerCase();
  if (ct.startsWith('image/') || ct.startsWith('audio/') || ct.startsWith('video/') || ct === 'application/pdf') {
    return true;
  }
  if (ct.startsWith('text/') || ct.includes('json') || ct.includes('xml') || ct.includes('svg') || ct.includes('yaml') || ct.includes('javascript') || ct.includes('typescript')) {
    return true;
  }
  const textExts = new Set(['md', 'markdown', 'txt', 'log', 'json', 'yaml', 'yml', 'xml', 'svg', 'csv', 'ts', 'tsx', 'js', 'jsx', 'go', 'py', 'rs', 'c', 'h', 'cpp', 'hpp', 'java', 'rb', 'php', 'sh', 'sql', 'toml', 'ini', 'conf', 'cfg', 'env', 'gitignore']);
  return textExts.has(ext);
}

// isMarkdown reports whether the file should be rendered as markdown in the
// preview rather than as raw text. Matches on both the file extension and the
// runtime Content-Type so a .md served with a non-standard MIME still works.
function isMarkdown(fileName: string, contentType: string): boolean {
  const ext = (fileName.split('.').pop() || '').toLowerCase();
  return ext === 'md' || ext === 'markdown' || contentType.toLowerCase() === 'text/markdown';
}

function FilePreview({ filePath, fileName, onClose, previewWidth, isDesktop, onResizeStart }: {
  filePath: string;
  fileName: string;
  onClose: () => void;
  previewWidth: number;
  isDesktop: boolean;
  onResizeStart: (e: React.MouseEvent) => void;
}) {
  const triggerDownload = useCallback(() => { downloadFile(filePath, fileName); }, [filePath, fileName]);

  return (
    <>
      {/* Backdrop */}
      <div className="fixed inset-0 bg-black/20 dark:bg-black/40 z-40 transition-opacity md:hidden" onClick={onClose} />

      {/* Right-side drawer; on md+ it becomes an inline column that pushes the
          chat left so you can read the file and type at the same time. */}
      <div
        className={cn(
          'fixed top-0 right-0 h-dvh w-full sm:w-[min(44rem,92vw)] z-50 flex flex-col animate-slide-in-right relative',
          'md:relative md:h-full md:w-[var(--preview-w,46rem)] md:z-auto md:animate-none md:rounded-none min-h-0',
          'bg-white/95 backdrop-blur-xl border-l border-gray-200/80 shadow-2xl shadow-black/15',
          'dark:bg-[#1f2228] dark:border-white/[0.12] dark:shadow-black/70',
        )}
        style={isDesktop ? ({ '--preview-w': `${previewWidth}px` } as React.CSSProperties) : undefined}
      >
        {/* Resize handle (desktop only) — must live inside the positioned
            panel so `absolute left-0` anchors to the preview column. */}
        <div
          onMouseDown={onResizeStart}
          className="hidden md:block absolute left-0 top-0 bottom-0 w-1.5 cursor-col-resize z-[60] hover:bg-accent/40 active:bg-accent/60 transition-colors"
          role="separator"
          aria-orientation="vertical"
          aria-label="Resize preview"
        />
        {/* Header */}
        <div className="flex items-center justify-between gap-2 px-4 h-14 border-b border-gray-200/80 dark:border-white/[0.12] shrink-0">
          <div className="flex items-center gap-2 min-w-0">
            <FileText size={16} className="text-gray-500 dark:text-gray-400 shrink-0" />
            <h3 className="text-sm font-semibold text-gray-900 dark:text-white truncate">{fileName}</h3>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="p-1.5 rounded-lg text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-white hover:bg-gray-100 dark:hover:bg-white/[0.08] transition-colors shrink-0"
            aria-label="Close preview"
          >
            <X size={16} />
          </button>
        </div>

        {/* Content */}
        <div className="flex-1 overflow-y-auto p-4 min-h-0">
          <FileContentView filePath={filePath} fileName={fileName} />
        </div>

        {/* Bottom action bar */}
        <div className="flex justify-end px-4 py-3 border-t border-gray-200/80 dark:border-white/[0.12] shrink-0">
          <Button onClick={triggerDownload} className="flex items-center gap-2">
            <Download size={15} /> Download
          </Button>
        </div>
      </div>
    </>
  );
}

// downloadFile fetches a file over the authenticated API and triggers a browser
// download of it.
async function downloadFile(filePath: string, fileName: string) {
  const res = await api.file(filePath);
  if (!res.ok) return;
  const url = URL.createObjectURL(res.blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = fileName;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

// FileContentView fetches a file's bytes and renders them inline when the type
// is previewable (text/code/image/pdf/audio/video); otherwise it shows a
// "can't be previewed" message. The Download button is rendered by the caller.
function FileContentView({ filePath, fileName }: { filePath: string; fileName: string }) {
  const [state, setState] = useState<'loading' | 'ready' | 'error'>('loading');
  const [contentType, setContentType] = useState('');
  const [text, setText] = useState('');
  const [dataUrl, setDataUrl] = useState('');
  const [error, setError] = useState('');

  useEffect(() => {
    let alive = true;
    // Reset all preview state before loading the new file so the previous
    // file's text/dataUrl don't bleed into the UI when switching between file
    // types (e.g. md → image would otherwise show both the old md text and
    // the new image at the same time).
    setState('loading');
    setError('');
    setText('');
    setDataUrl('');
    setContentType('');
    api.file(filePath).then(async (res) => {
      if (!alive) return;
      if (!res.ok) {
        setState('error');
        setError(`Failed to load file (${res.status})`);
        return;
      }
      setContentType(res.contentType);
      if (!fileIsPreviewable(fileName, res.contentType)) {
        setState('ready');
        return;
      }
      if (res.contentType.startsWith('text/') || /(json|xml|yaml|svg|javascript|typescript)/.test(res.contentType)) {
        const t = await res.blob.text();
        if (!alive) return;
        setText(t);
        setState('ready');
      } else {
        const reader = new FileReader();
        reader.onload = () => {
          if (!alive) return;
          setDataUrl(typeof reader.result === 'string' ? reader.result : '');
          setState('ready');
        };
        reader.onerror = () => { if (alive) { setState('error'); setError('Failed to read file'); } };
        reader.readAsDataURL(res.blob);
      }
    }).catch((e) => {
      if (!alive) return;
      setState('error');
      setError(e?.message || 'Failed to load file');
    });
    return () => { alive = false; };
  }, [filePath, fileName]);

  const previewable = fileIsPreviewable(fileName, contentType);

  return (
    <div className="flex flex-col gap-3">
      {state === 'loading' && (
        <div className="flex items-center justify-center gap-2 py-10 text-gray-500 dark:text-gray-400">
          <Loader2 size={18} className="animate-spin" /> Loading…
        </div>
      )}
      {state === 'error' && (
        <div className="py-6 text-center text-sm text-red-500">{error || 'Failed to load file'}</div>
      )}
      {state === 'ready' && (
        <>
          {previewable && text !== '' && (
            isMarkdown(fileName, contentType)
              ? <div className="max-h-[70vh] overflow-auto"><RenderMarkdown content={text} /></div>
              : <pre className="max-h-[70vh] overflow-auto rounded-lg bg-[#fafafa] dark:bg-[#0d1117] border border-gray-200 dark:border-gray-700/60 p-4 text-[13px] leading-[1.6] font-mono whitespace-pre-wrap break-words text-gray-800 dark:text-gray-100">
                  {text}
                </pre>
          )}
          {previewable && dataUrl !== '' && (() => {
            if (contentType.startsWith('image/')) return <img src={dataUrl} alt={fileName} className="max-h-[70vh] max-w-full mx-auto rounded-lg" />;
            if (contentType === 'application/pdf') return <iframe src={dataUrl} title={fileName} className="w-full h-[70vh] rounded-lg border border-gray-200 dark:border-gray-700" />;
            if (contentType.startsWith('audio/')) return <audio controls src={dataUrl} className="w-full" />;
            if (contentType.startsWith('video/')) return <video controls src={dataUrl} className="max-h-[70vh] max-w-full mx-auto rounded-lg" />;
            return null;
          })()}
          {!previewable && (
            <div className="py-6 text-center text-sm text-gray-500 dark:text-gray-400">
              This file type can’t be previewed. Use the download button below.
            </div>
          )}
        </>
      )}
    </div>
  );
}

// ── Project file browser ────────────────────────────────────
// Right-side drawer to browse/preview files under a project's work dir.
// Always shows a file preview in the body; navigation happens through the
// breadcrumb dropdown (current dir's files + parent) and the left/right arrows
// which cycle only through files (never directories).

interface FileEntry { name: string; type: 'dir' | 'file'; size: number; mtime: number }

// encodeRelPath percent-encodes each slash-separated segment (keeps slashes).
function encodeRelPath(rel: string): string {
  return rel.split('/').map((s) => encodeURIComponent(s)).join('/');
}

function ProjectFileBrowser({ open, projectName, onClose, onInsertFile, previewWidth, isDesktop, onResizeStart }: {
  open: boolean;
  projectName: string;
  onClose: () => void;
  onInsertFile?: (relPath: string) => void;
  previewWidth: number;
  isDesktop: boolean;
  onResizeStart: (e: React.MouseEvent) => void;
}) {
  // Remember the last browsed directory + selected file per project so the
  // browser re-opens where the user left off instead of the project root.
  const browseKey = useMemo(() => `cc_file_browser:${projectName}`, [projectName]);
  const remembered = useMemo(() => loadLS<{ path?: string; fileName?: string }>(browseKey), [browseKey]);

  const [currentPath, setCurrentPath] = useState(remembered?.path || '');
  const [rememberedFileName] = useState(remembered?.fileName || '');
  const [entries, setEntries] = useState<FileEntry[]>([]);
  const [currentIndex, setCurrentIndex] = useState(-1);
  // Closed by default: the directory dropdown is opt-in — open the browser
  // and you get the header breadcrumb + current file body, not a long list
  // of every sibling in the directory. Toggling the breadcrumb opens it.
  const [dropdownOpen, setDropdownOpen] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const fileList = useMemo(() => entries.filter((e) => e.type === 'file'), [entries]);
  const subDirs = useMemo(() => entries.filter((e) => e.type === 'dir'), [entries]);

  const parentPath = useMemo(() => {
    if (!currentPath) return null;
    const idx = currentPath.lastIndexOf('/');
    return idx === -1 ? '' : currentPath.slice(0, idx);
  }, [currentPath]);

  const currentFile = currentIndex >= 0 && currentIndex < fileList.length ? fileList[currentIndex] : null;
  const currentFileRel = currentFile
    ? (currentPath ? `${currentPath}/${currentFile.name}` : currentFile.name)
    : '';

  // Load the directory listing whenever the current dir changes. We do NOT
  // force the dropdown open here — openDir/goParent set it true at the call
  // site, and the initial mount intentionally leaves it closed.
  useEffect(() => {
    if (!open) return;
    let alive = true;
    setLoading(true);
    setError('');
    api.get<any>(`/files/${projectName}/${encodeRelPath(currentPath)}`).then((data) => {
      if (!alive) return;
      setEntries(data?.entries || []);
      const files: FileEntry[] = (data?.entries || []).filter((e: FileEntry) => e.type === 'file');
      // Restore the remembered file selection by name; fall back to the
      // first file when the remembered file no longer exists in this dir.
      let idx = 0;
      if (rememberedFileName) {
        const found = files.findIndex((f) => f.name === rememberedFileName);
        if (found >= 0) idx = found;
      }
      setCurrentIndex(files.length > 0 ? idx : -1);
      setLoading(false);
    }).catch((e) => {
      if (!alive) return;
      setError(e?.message || 'Failed to load directory');
      setEntries([]);
      setCurrentIndex(-1);
      setLoading(false);
    });
    return () => { alive = false; };
  }, [open, currentPath, projectName, rememberedFileName]);

  // Persist the current dir + selected file so the browser restores position.
  useEffect(() => {
    if (!open) return;
    if (currentPath === '' && currentIndex < 0) return; // skip initial idle
    saveLS(browseKey, { path: currentPath, fileName: currentFile?.name || '' });
  }, [open, browseKey, currentPath, currentFile?.name, currentIndex]);

  // When the file list changes, keep the selection within bounds.
  useEffect(() => {
    if (currentIndex >= fileList.length) setCurrentIndex(fileList.length - 1);
  }, [fileList, currentIndex]);

  const goPrev = () => setCurrentIndex((i) => Math.max(0, i - 1));
  const goNext = () => setCurrentIndex((i) => Math.min(fileList.length - 1, i + 1));

  const pickFile = (i: number) => { setCurrentIndex(i); setDropdownOpen(false); };
  const openDir = (dir: string) => { setCurrentPath(dir); setDropdownOpen(true); };
  const goParent = () => { if (parentPath !== null) { setCurrentPath(parentPath); setDropdownOpen(true); } };

  const breadcrumbSegments = currentPath ? currentPath.split('/') : [];
  const triggerDownload = useCallback(() => {
    if (currentFileRel) downloadFile(`/files/${projectName}/${encodeRelPath(currentFileRel)}`, currentFile?.name || 'file');
  }, [currentFileRel, currentFile, projectName]);

  if (!open) return null;

  return (
    <>
      {/* Backdrop */}
      <div className="fixed inset-0 bg-black/20 dark:bg-black/40 z-40 transition-opacity md:hidden" onClick={onClose} />

      {/* Right-side drawer; on md+ it becomes an inline column that pushes the
          chat left so you can browse the project and type at the same time. */}
      <div
        className={cn(
          'fixed top-0 right-0 h-dvh w-full sm:w-[min(56rem,96vw)] z-50 flex flex-col animate-slide-in-right relative',
          'md:relative md:h-full md:w-[var(--preview-w,46rem)] md:z-auto md:animate-none md:rounded-none min-h-0',
          'bg-white/95 backdrop-blur-xl border-l border-gray-200/80 shadow-2xl shadow-black/15',
          'dark:bg-[#1f2228] dark:border-white/[0.12] dark:shadow-black/70',
        )}
        style={isDesktop ? ({ '--preview-w': `${previewWidth}px` } as React.CSSProperties) : undefined}
      >
        {/* Resize handle (desktop only) — must live inside the positioned
            panel so `absolute left-0` anchors to the preview column. */}
        <div
          onMouseDown={onResizeStart}
          className="hidden md:block absolute left-0 top-0 bottom-0 w-1.5 cursor-col-resize z-[60] hover:bg-accent/40 active:bg-accent/60 transition-colors"
          role="separator"
          aria-orientation="vertical"
          aria-label="Resize preview"
        />
        {/* Header: breadcrumb + nav */}
        <div className="relative flex items-center justify-between gap-2 px-4 h-14 border-b border-gray-200/80 dark:border-white/[0.12] shrink-0">
          {/* Breadcrumb / current dir — click to open dropdown */}
          <button
            type="button"
            onClick={() => setDropdownOpen((v) => !v)}
            className="flex items-center gap-1 min-w-0 text-left"
          >
            <Folder size={16} className="text-gray-500 dark:text-gray-400 shrink-0" />
            <span className="text-sm font-medium text-gray-900 dark:text-white truncate">
              {projectName}
              {breadcrumbSegments.length > 0 && ` / ${breadcrumbSegments.join(' / ')}`}
            </span>
            <ChevronDown size={14} className={cn('text-gray-500 dark:text-gray-400 shrink-0 transition-transform', dropdownOpen && 'rotate-180')} />
          </button>

          {/* Left/right file navigation (files only) */}
          <div className="flex items-center gap-1 shrink-0">
            <button type="button" onClick={goPrev} disabled={currentIndex <= 0} aria-label="Previous file"
              className="p-1.5 rounded-lg text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-white hover:bg-gray-100 dark:hover:bg-white/[0.08] disabled:opacity-30 disabled:pointer-events-none transition-colors">
              <ChevronLeft size={18} />
            </button>
            <button type="button" onClick={goNext} disabled={currentIndex >= fileList.length - 1 || fileList.length === 0} aria-label="Next file"
              className="p-1.5 rounded-lg text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-white hover:bg-gray-100 dark:hover:bg-white/[0.08] disabled:opacity-30 disabled:pointer-events-none transition-colors">
              <ChevronRight size={18} />
            </button>
            <span className="text-[11px] text-gray-500 dark:text-gray-400 tabular-nums">
              {fileList.length > 0 ? `${currentIndex + 1}/${fileList.length}` : '0/0'}
            </span>
            <button type="button" onClick={onClose} aria-label="Close" className="p-1.5 rounded-lg text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-white hover:bg-gray-100 dark:hover:bg-white/[0.08] transition-colors">
              <X size={16} />
            </button>
          </div>

          {/* Dropdown: current dir files + subdirs + parent */}
          {dropdownOpen && (
            <div className="absolute top-full left-0 right-0 mt-0 z-50 max-h-[45vh] overflow-y-auto border-b border-gray-200/80 dark:border-white/[0.12] bg-white/98 dark:bg-[#2a2d34] shadow-xl">
              {parentPath !== null && (
                <button type="button" onClick={goParent}
                  className="w-full flex items-center gap-2 px-4 py-2 text-left text-sm text-gray-600 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-white/[0.08]">
                  <ArrowUp size={14} className="text-gray-500 dark:text-gray-400" /> <span>上一级</span>
                </button>
              )}
              {subDirs.map((d) => (
                <button key={'d' + d.name} type="button" onClick={() => openDir(currentPath ? `${currentPath}/${d.name}` : d.name)}
                  className="w-full flex items-center gap-2 px-4 py-2 text-left text-sm text-gray-800 dark:text-gray-200 hover:bg-gray-100 dark:hover:bg-white/[0.08]">
                  <Folder size={14} className="text-amber-500 shrink-0" /> <span className="truncate">{d.name}/</span>
                </button>
              ))}
              {subDirs.length > 0 && fileList.length > 0 && (
                <div className="h-px bg-gray-200/70 dark:bg-white/[0.08] mx-3" />
              )}
              {fileList.map((f, i) => (
                <button key={'f' + f.name} type="button" onClick={() => pickFile(i)}
                  className={cn(
                    'w-full flex items-center gap-2 px-4 py-2 text-left text-sm hover:bg-gray-100 dark:hover:bg-white/[0.08]',
                    i === currentIndex ? 'bg-accent/15 text-accent' : 'text-gray-800 dark:text-gray-200',
                  )}>
                  <FileText size={14} className="text-gray-500 dark:text-gray-400 shrink-0" /> <span className="truncate">{f.name}</span>
                </button>
              ))}
              {entries.length === 0 && <div className="px-4 py-4 text-sm text-gray-500 dark:text-gray-400">空目录</div>}
            </div>
          )}
        </div>

        {/* Body: always a file preview */}
        <div className="flex-1 overflow-y-auto p-4 min-h-0">
          {loading ? (
            <div className="flex items-center justify-center gap-2 py-10 text-gray-500">
              <Loader2 size={18} className="animate-spin" /> Loading…
            </div>
          ) : error ? (
            <div className="py-6 text-center text-sm text-red-500">{error}</div>
          ) : currentFile ? (
            <FileContentView filePath={`/files/${projectName}/${encodeRelPath(currentFileRel)}`} fileName={currentFile.name} />
          ) : (
            <div className="py-10 text-center text-sm text-gray-400">该目录没有可预览的文件，点上方目录选择</div>
          )}
        </div>

        {/* Bottom action bar */}
        <div className="flex items-center justify-between px-4 py-3 border-t border-gray-200/80 dark:border-white/[0.12] shrink-0">
          <button
            type="button"
            onClick={() => currentFileRel && onInsertFile?.(currentFileRel)}
            disabled={!currentFile}
            className="flex items-center gap-2 px-3 py-1.5 rounded-lg text-xs font-medium text-gray-600 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-white/[0.08] disabled:opacity-40 disabled:pointer-events-none transition-colors"
            title="插入文件地址到输入框"
          >
            <FileDown size={15} /> 插入地址
          </button>
          <Button onClick={triggerDownload} disabled={!currentFile} className="flex items-center gap-2">
            <Download size={15} /> Download
          </Button>
        </div>
      </div>
    </>
  );
}

function StatusBadge({ status }: { status: BridgeStatus }) {
  const { t } = useTranslation();
  if (status === 'connected') {
    return (
      <span className="flex items-center gap-1 text-[10px] text-emerald-600 dark:text-emerald-400 bg-emerald-50 dark:bg-emerald-900/20 px-1.5 py-0.5 rounded-full">
        <Circle size={5} className="fill-current" /> {t('sessions.bridgeConnected')}
      </span>
    );
  }
  if (status === 'connecting' || status === 'registering') {
    return (
      <span className="flex items-center gap-1 text-[10px] text-yellow-600 dark:text-yellow-400 bg-yellow-50 dark:bg-yellow-900/20 px-1.5 py-0.5 rounded-full">
        <Loader2 size={9} className="animate-spin" /> {t('sessions.bridgeConnecting')}
      </span>
    );
  }
  return (
    <span className="flex items-center gap-1 text-[10px] text-gray-400 bg-gray-100 dark:bg-gray-800 px-1.5 py-0.5 rounded-full">
      <WifiOff size={9} /> {t('sessions.bridgeDisconnected')}
    </span>
  );
}

// stripReplyFooter removes the trailing `*model · usage · path*` runtime footer
// that heron-connect appends to assistant replies, so copying the message does
// not carry runtime metadata into the clipboard.
function stripReplyFooter(text: string): string {
  if (!text) return text;
  const lines = text.split('\n');
  // Footer is the last non-empty line, wrapped in a single pair of asterisks.
  let lastIdx = lines.length - 1;
  while (lastIdx >= 0 && lines[lastIdx].trim() === '') lastIdx--;
  if (lastIdx < 0) return text;
  const footer = lines[lastIdx].trim();
  if (footer.startsWith('*') && footer.endsWith('*') && footer.length > 2 && !footer.slice(1, -1).includes('*')) {
    lines.splice(lastIdx, 1);
    while (lines.length > 0 && lines[lines.length - 1].trim() === '') lines.pop();
    return lines.join('\n');
  }
  return text;
}

function MsgCopyButton({ text, stripFooter }: { text: string; stripFooter?: boolean }) {
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

// ── Main component ───────────────────────────────────────────

export default function ChatView() {
  const { t } = useTranslation();
  const { name: projectName, id: routeSessionId } = useParams<{ name: string; id?: string }>();
  const navigate = useNavigate();
  const location = useLocation();

  // Session state
  const [sessions, setSessions] = useState<Session[]>([]);
  const [currentSession, setCurrentSession] = useState<SessionDetail | null>(null);
  const [messages, setMessages] = useState<ChatMsg[]>([]);
  const [input, setInput] = useState('');
  const [pickedFiles, setPickedFiles] = useState<PickItem[]>([]);
  const [sending, setSending] = useState(false);
  const [loading, setLoading] = useState(true);
  const [typing, setTyping] = useState(false);
  const [bridgeCfg, setBridgeCfg] = useState<BridgeConfig | null>(null);
  // Whether the user explicitly picked a session from the drawer
  const [userPickedSession, setUserPickedSession] = useState(false);

  // UI state
  const [cmdOpen, setCmdOpen] = useState(false);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [cmdResult, setCmdResult] = useState<CommandResult | null>(null);
  // Local file preview (agent-generated files served via /api/v1/files/...)
  const [previewFile, setPreviewFile] = useState<{ path: string; fileName: string } | null>(null);
  // Project file browser drawer
  const [fileBrowserOpen, setFileBrowserOpen] = useState(false);

  // Resizable preview column width (shared by FilePreview + ProjectFileBrowser).
  // Persisted globally — a UI preference that should carry across projects.
  const [previewWidth, setPreviewWidth] = useState<number>(() => loadLS<number>('cc_preview_width') ?? 640);

  // Whether the layout is on the desktop breakpoint (md ≥768px). Dragging the
  // resize handle only applies there; on mobile both previews are full-screen
  // drawers and the width is ignored.
  const [isDesktop, setIsDesktop] = useState<boolean>(() => typeof window !== 'undefined' && window.matchMedia('(min-width: 768px)').matches);
  useEffect(() => {
    const mq = window.matchMedia('(min-width: 768px)');
    const onChange = () => setIsDesktop(mq.matches);
    mq.addEventListener('change', onChange);
    return () => mq.removeEventListener('change', onChange);
  }, []);

  // Begin a column-width drag. `startX`/`startWidth` are captured at mousedown;
  // the width is recomputed on mousemove and persisted on mouseup.
  const beginResize = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    if (!isDesktop) return;
    const startX = e.clientX;
    const startWidth = previewWidth;
    const clamp = (w: number) => Math.min(Math.max(w, 320), Math.floor(window.innerWidth * 0.7));
    const onMove = (ev: MouseEvent) => {
      setPreviewWidth(clamp(startWidth + (startX - ev.clientX)));
    };
    const onUp = () => {
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
    };
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
  }, [isDesktop, previewWidth]);

  // Persist the width when it changes (mouseup lands here via the move handler
  // that runs setPreviewWidth; we save on every settled change).
  useEffect(() => {
    saveLS('cc_preview_width', previewWidth);
  }, [previewWidth]);

  // Rename session modal target (null = closed)
  const [renameTarget, setRenameTarget] = useState<Session | null>(null);

  const messagesEnd = useRef<HTMLDivElement>(null);
  const previewHandleCounter = useRef(0);
  const cmdBtnRef = useRef<HTMLButtonElement>(null);
  const sessionKeyRef = useRef('');
  // Track pending slash command so the next reply can be routed to the panel
  const pendingCmdRef = useRef<string | null>(null);
  // Mirrors cmdResult.command so card-action callbacks can route follow-ups back to the panel
  const cmdPanelRef = useRef<string | null>(null);

  // Routing key = the currently-open conversation's own key. Each Web
  // conversation owns a unique session_key, so sending always targets that
  // conversation's agent session. We never fall back to a shared per-tab key
  // (that was the cause of all conversations collapsing into one session).
  // When no conversation is open yet, mint a fresh per-conversation key.
  const webSessionKey = useMemo(
    () => (currentSession?.session_key || newConvKey(projectName || '')),
    [currentSession, projectName]
  );
  const sessionKey = currentSession?.session_key || webSessionKey;
  sessionKeyRef.current = sessionKey;

  // Load project sessions and auto-select latest (or the one specified in the URL)
  const fetchData = useCallback(async () => {
    if (!projectName) return;
    setLoading(true);
    try {
      const [{ sessions: allSessions }, cfg] = await Promise.all([
        listSessions(projectName),
        fetchBridgeConfig(),
      ]);
      setBridgeCfg(cfg);
      const sorted = sortSessions(allSessions || []);
      setSessions(sorted);

      const target = routeSessionId
        ? sorted.find(s => s.id === routeSessionId) || null
        : sorted[0];

      if (target) {
        setUserPickedSession(!!routeSessionId);
        const detail = await getSession(projectName, target.id, 200);
        setCurrentSession(detail);
        if (detail.history) {
          setMessages(detail.history.map((h, i) => ({
            id: `hist-${i}`,
            role: h.role as 'user' | 'assistant',
            content: h.content,
            format: 'markdown',
            timestamp: h.timestamp,
          })));
        }
      } else {
        setCurrentSession(null);
        setMessages([]);
      }
    } finally {
      setLoading(false);
    }
  }, [projectName, routeSessionId]);

  useEffect(() => { fetchData(); }, [fetchData]);

  // Periodically refresh the session list so execution-status badges (running
  // / waiting permission) stay current while other sessions run in parallel.
  // Only refreshes the list — never touches the open conversation.
  const refreshSessions = useCallback(async () => {
    if (!projectName) return;
    try {
      const { sessions: allSessions } = await listSessions(projectName);
      const sorted = sortSessions(allSessions || []);
      setSessions(sorted);
    } catch { /* transient — keep last known list */ }
  }, [projectName]);

  useEffect(() => {
    const timer = setInterval(refreshSessions, 5000);
    return () => clearInterval(timer);
  }, [refreshSessions]);

  // Keep ref in sync with cmdResult so callbacks avoid stale closures
  useEffect(() => {
    cmdPanelRef.current = cmdResult?.command ?? null;
  }, [cmdResult]);

  // Switch to a different session (user explicitly chose from drawer)
  const switchToSession = useCallback(async (s: Session) => {
    if (!projectName) return;
    setDrawerOpen(false);
    setLoading(true);
    setUserPickedSession(true);
    navigate(`/chat/${projectName}/${s.id}`, { replace: true });
    try {
      const detail = await getSession(projectName, s.id, 200);
      setCurrentSession(detail);
      if (detail.history) {
        setMessages(detail.history.map((h, i) => ({
          id: `hist-${i}`,
          role: h.role as 'user' | 'assistant',
          content: h.content,
          format: 'markdown',
          timestamp: h.timestamp,
        })));
      } else {
        setMessages([]);
      }
    } finally {
      setLoading(false);
    }
  }, [projectName, navigate]);

  // Rename a session (current or from the drawer list), then refresh.
  const handleSessionRenamed = useCallback(async (target: Session, newName: string) => {
    if (target.id === currentSession?.id) {
      setCurrentSession({ ...currentSession, name: newName });
    }
    await refreshSessions();
  }, [currentSession, refreshSessions]);

  // Toggle pin on a session (current or from the drawer list).
  const togglePinSession = useCallback(async (target: Session) => {
    if (!projectName) return;
    const next = !target.pinned;
    try {
      await updateSession(projectName, target.id, { pinned: next });
      if (target.id === currentSession?.id) {
        setCurrentSession({ ...currentSession, pinned: next });
      }
      await refreshSessions();
    } catch { /* transient */ }
  }, [projectName, currentSession, refreshSessions]);

  // Settle ALL in-flight streaming flags and the typing indicator. Called on
  // every terminal bridge event so an unmatched streaming placeholder (e.g. one
  // whose matching condition wasn't met, or a missed typing_stop) can never
  // leave the red "running" button stuck on after the turn actually finished.
  const settleAllStreaming = useCallback(() => {
    setTyping(false);
    setMessages(prev => {
      if (!prev.some(m => m.streaming)) return prev;
      return prev.map(m => (m.streaming ? { ...m, streaming: false } : m));
    });
  }, []);

  // Handle bridge incoming messages — only process messages for the current session
  const handleBridgeMessage = useCallback((msg: BridgeIncoming) => {
    const msgKey = (msg as any).session_key;
    const msgID = (msg as any).session_id;
    // A reply is relevant to this panel if its session_key matches the
    // conversation we're showing, OR (for legacy/shared-key replies) its
    // explicit session_id matches the conversation id. Matching either keeps
    // cross-tab live sync working now that every conversation owns a distinct key.
    const currentId = currentSession?.id;
    const matchesSessionKey = msgKey && sessionKeyRef.current && msgKey === sessionKeyRef.current;
    const matchesSessionId = msgID && currentId && msgID === currentId;
    if ((msgKey || msgID) && !matchesSessionKey && !matchesSessionId) {
      return;
    }

    // If a slash command is pending, route the first reply/card to the panel
    const pending = pendingCmdRef.current;
    if (pending && (msg.type === 'reply' || msg.type === 'card' || msg.type === 'buttons')) {
      pendingCmdRef.current = null;
      if (msg.type === 'card') {
        const card = msg as Extract<BridgeIncoming, { type: 'card' }>;
        setCmdResult({ command: pending, content: '', format: 'card', card: card.card });
      } else if (msg.type === 'buttons') {
        const btns = msg as Extract<BridgeIncoming, { type: 'buttons' }>;
        setCmdResult({ command: pending, content: btns.content, format: 'buttons', buttons: btns.buttons });
      } else {
        const reply = msg as Extract<BridgeIncoming, { type: 'reply' }>;
        setCmdResult({ command: pending, content: reply.content, format: 'markdown' });
      }
      setTyping(false);
      return;
    }

    if (msg.type === 'reply') {
      const reply = msg as Extract<BridgeIncoming, { type: 'reply' }>;
      setMessages(prev => {
        // Never clobber thinking/tool progress previews — those carry a
        // previewHandle. Update an existing answer placeholder (streaming, no
        // previewHandle) or append a fresh answer message instead.
        const ansIdx = prev.findIndex(m => m.streaming && m.role === 'assistant' && !m.previewHandle);
        if (ansIdx >= 0) {
          const updated = [...prev];
          updated[ansIdx] = { ...updated[ansIdx], content: reply.content, format: (reply as any).format === 'markdown' ? 'markdown' : 'text', streaming: false };
          // Settle any lingering progress previews so they stop pulsing.
          for (let i = 0; i < updated.length; i++) {
            if (updated[i].streaming && updated[i].previewHandle) updated[i] = { ...updated[i], streaming: false };
          }
          return updated;
        }
        // No matching placeholder: still settle any orphan streaming flags so the
        // running button can't get stuck, then append the final reply.
        const settled = prev.map(m => (m.streaming ? { ...m, streaming: false } : m));
        return [...settled, { id: `reply-${Date.now()}`, role: 'assistant', content: reply.content, format: (reply as any).format === 'markdown' ? 'markdown' : 'text' }];
      });
      setTyping(false);
    } else if (msg.type === 'reply_stream') {
      const stream = msg as Extract<BridgeIncoming, { type: 'reply_stream' }>;
      if (stream.done) {
        setMessages(prev => {
          // Match the answer placeholder specifically (no previewHandle), never
          // a thinking/tool progress preview.
          const idx = prev.findIndex(m => m.streaming && m.role === 'assistant' && !m.previewHandle);
          if (idx >= 0) {
            const updated = [...prev];
            updated[idx] = { ...updated[idx], content: stream.full_text, streaming: false };
            for (let i = 0; i < updated.length; i++) {
              if (updated[i].streaming && updated[i].previewHandle) updated[i] = { ...updated[i], streaming: false };
            }
            return updated;
          }
          // No matching placeholder: settle orphan streaming flags, then append.
          const settled = prev.map(m => (m.streaming ? { ...m, streaming: false } : m));
          return [...settled, { id: `stream-done-${Date.now()}`, role: 'assistant', content: stream.full_text, format: 'markdown' }];
        });
        setTyping(false);
      } else {
        setMessages(prev => {
          const idx = prev.findIndex(m => m.streaming);
          if (idx >= 0) {
            const updated = [...prev];
            updated[idx] = { ...updated[idx], content: stream.full_text };
            return updated;
          }
          return [...prev, { id: `stream-${Date.now()}`, role: 'assistant', content: stream.full_text, format: 'markdown', streaming: true }];
        });
      }
    } else if (msg.type === 'card') {
      const card = msg as Extract<BridgeIncoming, { type: 'card' }>;
      setMessages(prev => [...prev, { id: `card-${Date.now()}`, role: 'assistant', content: '', format: 'card', card: card.card }]);
      settleAllStreaming();
    } else if (msg.type === 'buttons') {
      const btns = msg as Extract<BridgeIncoming, { type: 'buttons' }>;
      setMessages(prev => [...prev, { id: `btn-${Date.now()}`, role: 'assistant', content: btns.content, format: 'buttons', buttons: btns.buttons }]);
      settleAllStreaming();
    } else if (msg.type === 'typing_start') {
      setTyping(true);
    } else if (msg.type === 'typing_stop') {
      // Turn concluded: clear the typing indicator and any lingering streaming
      // placeholders so the running button always returns to the input state.
      settleAllStreaming();
    } else if (msg.type === 'preview_start') {
      const ps = msg as Extract<BridgeIncoming, { type: 'preview_start' }>;
      const handle = `web-preview-${++previewHandleCounter.current}`;
      sendPreviewAck(ps.ref_id, handle);
      setMessages(prev => [...prev, {
        id: `stream-${handle}`,
        role: 'assistant',
        content: ps.content,
        format: 'markdown',
        streaming: true,
        previewHandle: handle,
        progressCard: parseProgressCard(ps.content) ?? undefined,
      }]);
    } else if (msg.type === 'update_message') {
      const um = msg as Extract<BridgeIncoming, { type: 'update_message' }>;
      setMessages(prev => {
        const idx = prev.findIndex(m => m.streaming && m.previewHandle === um.preview_handle);
        if (idx >= 0) {
          const updated = [...prev];
          updated[idx] = {
            ...updated[idx],
            content: um.content,
            // Re-parse on every push so the structured payload tracks the
            // latest progress without losing previously-toggled expand state
            // (the ProgressCard component holds that state internally by
            // stable content key).
            progressCard: parseProgressCard(um.content) ?? updated[idx].progressCard,
          };
          return updated;
        }
        // The preview message was lost — most commonly because the user
        // switched sessions mid-turn and the in-memory preview was replaced
        // by history. update_message carries the FULL accumulated progress
        // content (all tools so far), so re-attach instead of dropping: the
        // next tool event re-creates the progress block and live updates
        // continue. Only re-attach while the turn is still producing output;
        // finalized (non-streaming) previews are never resurrected.
        if (um.preview_handle) {
          return [...prev, {
            id: `stream-${um.preview_handle}`,
            role: 'assistant' as const,
            content: um.content,
            format: 'markdown' as const,
            streaming: true,
            previewHandle: um.preview_handle,
            progressCard: parseProgressCard(um.content) ?? undefined,
          }];
        }
        return prev;
      });
    } else if (msg.type === 'delete_message') {
      const dm = msg as Extract<BridgeIncoming, { type: 'delete_message' }>;
      setMessages(prev => {
        const idx = prev.findIndex(m => m.streaming && m.previewHandle === dm.preview_handle);
        if (idx >= 0) {
          // Finalize the progress block (thinking/tool) instead of removing it,
          // so the executed thinking/tool steps remain visible in the chat history.
          const updated = [...prev];
          updated[idx] = { ...updated[idx], streaming: false };
          return updated;
        }
        return prev;
      });
    }
  }, [currentSession?.id]);

  const { status: bridgeStatus, sendMessage: bridgeSend, sendCardAction, sendPreviewAck } = useBridgeSocket({
    bridgeCfg,
    sessionKey,
    projectName: projectName || '',
    onMessage: handleBridgeMessage,
  });

  // Scroll to bottom on new messages
  useEffect(() => {
    messagesEnd.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages, typing]);

  // If the bridge connection drops mid-turn, no terminal event will arrive to
  // clear the typing/streaming flags — settle them so the red button can't get
  // stuck in the "running" state after a disconnect.
  useEffect(() => {
    if (bridgeStatus !== 'connected') {
      settleAllStreaming();
    }
  }, [bridgeStatus, settleAllStreaming]);

  // True while the agent is actively producing a reply (typing indicator or any
  // streaming message in flight). Drives the red stop button.
  const isRunning = typing || messages.some(m => m.streaming);

  const handleStop = useCallback(() => {
    bridgeSend('/stop');
  }, [bridgeSend]);

  // Per-file size cap before we base64-encode it into the bridge frame.
  // base64 inflates ~33%, and the WebSocket frame carries it all in memory.
  const MAX_UPLOAD_BYTES = 10 * 1024 * 1024;

  const fileInputRef = useRef<HTMLInputElement | null>(null);

  const addPickedFiles = useCallback((files: FileList | File[]) => {
    const list = Array.from(files);
    if (list.length === 0) return;
    const tooBig: string[] = [];

    const readFile = (f: File): Promise<PickItem | null> =>
      new Promise((resolve) => {
        const reader = new FileReader();
        reader.onload = () => {
          const dataUrl = typeof reader.result === 'string' ? reader.result : '';
          if (!dataUrl) {
            resolve(null);
            return;
          }
          resolve({
            id: `pick-${Date.now()}-${Math.random().toString(36).slice(2)}`,
            name: f.name,
            mime_type: f.type || 'application/octet-stream',
            dataUrl,
            size: f.size,
            kind: f.type.startsWith('image/') ? 'image' : 'file',
          });
        };
        reader.onerror = () => resolve(null);
        reader.readAsDataURL(f);
      });

    Promise.all(
      list.map((f) => {
        if (f.size > MAX_UPLOAD_BYTES) {
          tooBig.push(f.name);
          return Promise.resolve<PickItem | null>(null);
        }
        return readFile(f);
      }),
    ).then((items) => {
      const ok = items.filter((i): i is PickItem => i !== null);
      if (ok.length > 0) setPickedFiles(prev => [...prev, ...ok]);
      if (tooBig.length > 0) {
        alert(`${t('chat.fileTooBig', 'File too large (>10MB)')}: ${tooBig.join(', ')}`);
      }
      if (fileInputRef.current) fileInputRef.current.value = '';
    });
  }, [t]);

  const removePickedFile = useCallback((id: string) => {
    setPickedFiles(prev => prev.filter(p => p.id !== id));
  }, []);

  const stripDataUrlPrefix = useCallback((dataUrl: string): string => {
    const comma = dataUrl.indexOf(',');
    return comma >= 0 ? dataUrl.slice(comma + 1) : dataUrl;
  }, []);

  // Send message
  const handleSend = useCallback(() => {
    if (isRunning) return;
    if ((!input.trim() && pickedFiles.length === 0) || bridgeStatus !== 'connected') return;
    const content = input.trim();
    setInput('');
    setSending(true);

    // Build media payload (only if any attachment is attached).
    const images: { mime_type: string; data: string; file_name?: string }[] = [];
    const files: { mime_type: string; data: string; file_name: string }[] = [];
    pickedFiles.forEach((p) => {
      const b64 = stripDataUrlPrefix(p.dataUrl);
      if (p.kind === 'image') images.push({ mime_type: p.mime_type, data: b64, file_name: p.name });
      else files.push({ mime_type: p.mime_type, data: b64, file_name: p.name });
    });
    const media = images.length > 0 || files.length > 0 ? { ...(images.length ? { images } : {}), ...(files.length ? { files } : {}) } : undefined;

    const cmdToken = content.split(' ')[0];
    const isKnownCmd = knownCommands.has(cmdToken);
    if (isKnownCmd && !chatCommands.has(cmdToken)) {
      pendingCmdRef.current = cmdToken;
    } else {
      setMessages(prev => [...prev, {
        id: `user-${Date.now()}`,
        role: 'user',
        content,
        localMedia: pickedFiles.length > 0 ? pickedFiles : undefined,
      }]);
    }
    bridgeSend(content, media, currentSession?.id);
    setPickedFiles([]);
    setTimeout(() => setSending(false), 300);
  }, [input, pickedFiles, bridgeStatus, bridgeSend, isRunning, stripDataUrlPrefix, currentSession?.id]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      // 输入法组合中（如中文拼音选词/确认字母）的回车不触发发送，
      // 否则会发送半成品。组合结束后的回车才真正发送。
      if (e.nativeEvent.isComposing) return;
      e.preventDefault();
      handleSend();
    }
    if (e.key === '/' && !input) {
      e.preventDefault();
      setCmdOpen(true);
    }
  };

  // Commands whose result should go to the message stream (they change state)
  const chatCommands = new Set(['/new', '/stop', '/switch', '/delete-mode', '/upgrade']);
  const knownCommands = new Set(slashCommands.map(c => c.cmd));

  const handleCmdSelect = useCallback((cmd: SlashCommand) => {
    setCmdOpen(false);
    if (bridgeStatus !== 'connected') return;

    if (chatCommands.has(cmd.cmd)) {
      setMessages(prev => [...prev, { id: `user-${Date.now()}`, role: 'user', content: cmd.cmd }]);
    } else {
      pendingCmdRef.current = cmd.cmd;
    }
    bridgeSend(cmd.cmd);
  }, [bridgeStatus, bridgeSend]);

  const handleCardAction = useCallback((value: string) => {
    if (bridgeStatus !== 'connected') return;
    // If the command panel is showing, route the follow-up response back to it
    if (cmdPanelRef.current) {
      pendingCmdRef.current = cmdPanelRef.current;
    }
    sendCardAction(value);
  }, [bridgeStatus, sendCardAction]);

  const handleNewSession = useCallback(() => {
    if (bridgeStatus !== 'connected') return;
    setUserPickedSession(false);
    setMessages(prev => [...prev, { id: `user-${Date.now()}`, role: 'user', content: '/new' }]);
    bridgeSend('/new');
    setDrawerOpen(false);
    if (routeSessionId) {
      navigate(`/chat/${projectName}`, { replace: true });
    }
  }, [bridgeStatus, bridgeSend, routeSessionId, projectName, navigate]);

  const canSend = bridgeStatus === 'connected';

  // Execution status of the currently viewed session (from the polled session
  // list). Distinct from isRunning, which tracks THIS client's live turn.
  const activeSessionKey = webSessionKey;
  const viewedStatus = sessions.find(s => s.session_key === activeSessionKey);

  if (loading && !currentSession && sessions.length === 0) {
    return <div className="flex items-center justify-center h-64 text-gray-400 animate-pulse">Loading...</div>;
  }

  return (
    <div className="flex flex-col flex-1 min-h-0 animate-fade-in md:flex-row">
      {/* Left chat column; on md+ the open preview drawer becomes an inline
          right column and this column flexes to fill the remaining width. */}
      <div className="flex flex-col flex-1 min-w-0 min-h-0">
      {/* Header */}
      <div className="flex items-center justify-between pb-3 border-b border-gray-200 dark:border-gray-800 shrink-0">
        <div className="flex items-center gap-3 min-w-0">
          <button
            type="button"
            onClick={() => {
              // Return to wherever the user entered from (chat list, project
              // detail, or session list) rather than always the chat list.
              // navigate(-1) uses SPA history; fall back to /chat on a fresh load.
              if (location.key === 'default') {
                navigate('/chat');
              } else {
                navigate(-1);
              }
            }}
            className="p-2 rounded-lg hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors shrink-0"
            aria-label={t('chat.back')}
          >
            <ArrowLeft size={18} className="text-gray-400" />
          </button>
          <div className="min-w-0">
            <div className="flex items-center gap-2 min-w-0">
              <h2 className="text-lg font-semibold text-gray-900 dark:text-white truncate">{projectName}</h2>
              {/* Bridge status badge is desktop-only: on mobile the title row
                  is too crowded and connection problems already surface via
                  the input area warning. */}
              <div className="hidden md:block">
                <StatusBadge status={bridgeStatus} />
              </div>
              {viewedStatus?.waiting_permission ? (
                <span className="flex items-center gap-1 text-[10px] text-amber-600 dark:text-amber-400 bg-amber-50 dark:bg-amber-900/20 px-1.5 py-0.5 rounded-full shrink-0">
                  <Clock size={9} /> {t('sessions.waitingPermission')}
                </span>
              ) : viewedStatus?.running ? (
                <span className="flex items-center gap-1 text-[10px] text-emerald-600 dark:text-emerald-400 bg-emerald-50 dark:bg-emerald-900/20 px-1.5 py-0.5 rounded-full shrink-0">
                  <Loader2 size={9} className="animate-spin" /> {t('sessions.running')}
                </span>
              ) : null}
            </div>
            <button
              type="button"
              onClick={() => setDrawerOpen(true)}
              className="flex items-center gap-1 min-w-0 max-w-full text-xs text-gray-500 hover:text-accent transition-colors mt-0.5"
            >
              {/* Auto-generated session titles can be long (first user
                  message); truncate so narrow screens keep a tidy header. */}
              <span className="truncate">
                {userPickedSession && currentSession
                  ? sessionTitle(currentSession)
                  : t('chat.defaultSession')}
              </span>
              <ChevronDown size={12} className="shrink-0" />
            </button>
          </div>
        </div>
        {/* Mobile: reserve right padding so the pin/files buttons clear the
            floating hamburger (~40px incl. offset) that the collapsed top bar
            pins at the top-right corner. Desktop has no floating button. */}
        <div className="flex items-center gap-1 shrink-0 pr-12 md:pr-0">
          {currentSession && (
            <>
              <button
                type="button"
                onClick={() => currentSession && setRenameTarget(currentSession)}
                className="hidden md:block p-2 rounded-lg hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors"
                aria-label="Rename session"
                title="重命名会话"
              >
                <Pencil size={16} className="text-gray-400" />
              </button>
              <button
                type="button"
                onClick={() => currentSession && togglePinSession(currentSession)}
                className="p-2 rounded-lg hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors"
                aria-label={currentSession.pinned ? 'Unpin session' : 'Pin session'}
                title={currentSession.pinned ? '取消置顶' : '置顶'}
              >
                {currentSession.pinned
                  ? <PinOff size={16} className="text-accent" />
                  : <Pin size={16} className="text-gray-400" />}
              </button>
            </>
          )}
          <button
            type="button"
            onClick={() => setFileBrowserOpen(true)}
            className="p-2 rounded-lg hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors"
            aria-label="Browse project files"
            title="项目文件"
          >
            <FolderOpen size={18} className="text-gray-400" />
          </button>
        </div>
      </div>

      {/* Messages — `flex-1 overflow-y-auto` + ancestor `min-h-0` chain
          (Layout wrapper has `min-h-0` since v1.1.24) gives correct scrolling
          and lets the input area sit flush at the bottom. The earlier
          `max-h-[calc(100dvh-136px)] md:max-h-[calc(100dvh-192px)]` cap
          overestimated header+input height on PC (the 192px shadow left
          ~50px of dead space below the input on desktop). */}
      <div className="flex-1 overflow-y-auto overflow-x-hidden py-4 md:py-6 px-2 space-y-5">
        {messages.length === 0 && !loading && (
          <div className="flex flex-col items-center justify-center h-full text-center py-12">
            <div className="w-16 h-16 rounded-2xl bg-accent/10 flex items-center justify-center mb-4">
              <Bot size={32} className="text-accent" />
            </div>
            <p className="text-sm text-gray-500 dark:text-gray-400 mb-1">{t('chat.emptyHint')}</p>
            <p className="text-xs text-gray-400 dark:text-gray-500">{t('chat.slashHint')}</p>
          </div>
        )}
        {messages.map((msg) => {
          const isUser = msg.role === 'user';
          const isEmpty = !msg.content && !msg.card && !msg.buttons && !msg.imageUrl && !msg.fileName && !msg.localMedia?.length;
          return (
            <div key={msg.id} className={cn('flex gap-3', isUser ? 'justify-end' : 'justify-start')}>
              {!isUser && (
                <div className="w-8 h-8 rounded-lg bg-accent/10 flex items-center justify-center shrink-0 mt-1">
                  <Bot size={16} className="text-accent" />
                </div>
              )}
              <div className={cn(
                'group/msg relative rounded-2xl px-3.5 py-3 sm:px-5 sm:py-3.5 text-sm min-w-0 break-words',
                isUser
                  ? 'max-w-[85%] sm:max-w-[70%] bg-accent text-black rounded-br-md'
                  : 'max-w-[85%] bg-white dark:bg-gray-800/80 border border-gray-200 dark:border-gray-700/60 text-gray-900 dark:text-gray-100 rounded-bl-md shadow-sm',
                msg.streaming && 'animate-pulse-subtle',
              )}>
                {isEmpty ? (
                  <p className="text-xs text-gray-400 dark:text-gray-500 italic">{t('chat.unsupportedMessage', '[Unsupported message]')}</p>
                ) : msg.format === 'card' ? (
                  <CardBlock card={msg.card} onAction={handleCardAction} />
                ) : msg.format === 'buttons' && msg.buttons ? (
                  <ButtonsBlock content={msg.content} buttons={msg.buttons} onAction={handleCardAction} />
                ) : msg.format === 'image' && msg.imageUrl ? (
                  <ImageBlock url={msg.imageUrl} />
                ) : msg.format === 'file' && msg.fileName ? (
                  <FileBlock name={msg.fileName} size={msg.fileSize} />
                ) : isUser ? (
                  <div className="space-y-2">
                    {msg.localMedia && msg.localMedia.length > 0 && (
                      <div className="flex flex-wrap gap-2">
                        {msg.localMedia.map((m) =>
                          m.kind === 'image' ? (
                            <img key={m.id} src={m.dataUrl} alt={m.name} className="max-w-[220px] max-h-[220px] rounded-lg border border-white/20 object-cover" />
                          ) : (
                            <FileBlock key={m.id} name={m.name} size={m.size} />
                          ),
                        )}
                      </div>
                    )}
                    {msg.content && <div className="whitespace-pre-wrap">{msg.content}</div>}
                  </div>
                ) : msg.progressCard ? (
                  <ProgressCard payload={msg.progressCard} />
                ) : (
                  <RenderMarkdown content={msg.content} onOpenFile={(path, fileName) => setPreviewFile({ path, fileName })} />
                )}
                {msg.streaming && !msg.progressCard && (
                  <span className="inline-block w-1.5 h-4 bg-accent/60 rounded-sm ml-0.5 animate-pulse" />
                )}
                {!isUser && !msg.streaming && msg.content && !msg.progressCard && (
                  <MsgCopyButton text={msg.content} stripFooter />
                )}
              </div>
              {isUser && (
                <div className="w-8 h-8 rounded-lg bg-gray-200 dark:bg-gray-700 flex items-center justify-center shrink-0 mt-1">
                  <User size={16} className="text-gray-500" />
                </div>
              )}
            </div>
          );
        })}
        {typing && !messages.some(m => m.streaming) && (
          <div className="flex gap-3 justify-start">
            <div className="w-8 h-8 rounded-lg bg-accent/10 flex items-center justify-center shrink-0 mt-1">
              <Bot size={16} className="text-accent" />
            </div>
            <div className="rounded-2xl px-5 py-3.5 text-sm bg-white dark:bg-gray-800/80 border border-gray-200 dark:border-gray-700/60 rounded-bl-md shadow-sm">
              <div className="flex gap-1.5">
                <span className="w-2 h-2 bg-gray-400 rounded-full animate-bounce" style={{ animationDelay: '0ms' }} />
                <span className="w-2 h-2 bg-gray-400 rounded-full animate-bounce" style={{ animationDelay: '150ms' }} />
                <span className="w-2 h-2 bg-gray-400 rounded-full animate-bounce" style={{ animationDelay: '300ms' }} />
              </div>
            </div>
          </div>
        )}
        <div ref={messagesEnd} />
      </div>

      {/* Input area */}
      <div className="border-t border-gray-200 dark:border-gray-800 pt-3 shrink-0">
        {canSend ? (
          <div className="relative flex items-end gap-2">
            {/* Attachment trigger */}
            <button
              type="button"
              onClick={() => fileInputRef.current?.click()}
              className="p-3 rounded-xl text-gray-400 hover:text-gray-600 dark:hover:text-gray-200 hover:bg-gray-100 dark:hover:bg-white/[0.06] transition-colors"
              title={t('chat.attach', 'Attach image/file')}
            >
              <Paperclip size={18} />
            </button>
            <input
              ref={fileInputRef}
              type="file"
              multiple
              accept="image/*,.pdf,.txt,.md,.markdown,.doc,.docx,.xls,.xlsx,.ppt,.pptx,.csv,.json,.yaml,.yml,.zip,.tar,.gz,.py,.js,.ts,.go,.java,.c,.h,.cpp,.sh,.sql,.log"
              className="hidden"
              onChange={(e) => {
                if (e.target.files) addPickedFiles(e.target.files);
              }}
            />

            {/* Command palette trigger */}
            <div className="relative">
              <button
                ref={cmdBtnRef}
                type="button"
                onClick={() => setCmdOpen(!cmdOpen)}
                className={cn(
                  'p-3 rounded-xl transition-all duration-200',
                  cmdOpen
                    ? 'bg-accent/15 text-accent ring-1 ring-accent/30'
                    : 'text-gray-400 hover:text-gray-600 dark:hover:text-gray-200 hover:bg-gray-100 dark:hover:bg-white/[0.06]',
                )}
                title={t('chat.commands')}
              >
                <Slash size={18} />
              </button>
              <CommandPalette
                open={cmdOpen}
                onClose={() => setCmdOpen(false)}
                onSelect={handleCmdSelect}
                anchorRef={cmdBtnRef}
              />
            </div>

            {/* Text input */}
            {/* Text input — `min-w-0` lets the row shrink below the button +
                textarea intrinsic width on narrow phones (textarea defaults to
                ~20 cols which alone exceeds 320px). */}
            <div className="flex-1 min-w-0 relative">
              {pickedFiles.length > 0 && (
                <div className="flex flex-wrap gap-1.5 mb-1.5">
                  {pickedFiles.map((p) => (
                    <div
                      key={p.id}
                      className="flex items-center gap-1.5 max-w-[220px] pl-1 pr-1.5 py-1 rounded-lg bg-gray-100 dark:bg-white/[0.06] border border-gray-200 dark:border-gray-700"
                    >
                      {p.kind === 'image' ? (
                        <img src={p.dataUrl} alt={p.name} className="w-6 h-6 rounded object-cover shrink-0" />
                      ) : (
                        <FileText size={14} className="text-gray-400 shrink-0" />
                      )}
                      <span className="text-xs text-gray-600 dark:text-gray-300 truncate">{p.name}</span>
                      <button
                        type="button"
                        onClick={() => removePickedFile(p.id)}
                        className="text-gray-400 hover:text-gray-600 dark:hover:text-gray-200 shrink-0"
                        title={t('chat.removeAttachment', 'Remove')}
                      >
                        <X size={12} />
                      </button>
                    </div>
                  ))}
                </div>
              )}
              <textarea
                value={input}
                onChange={(e) => {
                  setInput(e.target.value);
                  e.target.style.height = 'auto';
                  e.target.style.height = Math.min(e.target.scrollHeight, 160) + 'px';
                }}
                onKeyDown={handleKeyDown}
                placeholder={t('chat.inputPlaceholder')}
                rows={1}
                // `py-2` (8px) + text-base 16px line-height 24px + border 2px = 42px,
                // matching the `p-3` buttons (12+18+12 = 42px) so icons and
                // textarea sit on the same baseline. `text-base` (16px) on
                // mobile prevents iOS from auto-zooming on focus.
                className="w-full min-w-0 px-4 py-2 text-base md:text-sm rounded-xl border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-800 text-gray-900 dark:text-white focus:outline-none focus:ring-2 focus:ring-accent/50 focus:border-accent transition-colors placeholder:text-gray-400 resize-none overflow-y-auto"
              />
            </div>

            {/* Send / Stop button */}
            {isRunning ? (
              <button
                type="button"
                onClick={handleStop}
                title={t('chat.stop')}
                className="p-3 rounded-xl bg-red-500 text-white hover:bg-red-600 transition-colors flex items-center shadow-sm"
              >
                <Square size={16} className="fill-current" />
              </button>
            ) : (
              <button
                type="button"
                onClick={handleSend}
                disabled={!input.trim() && pickedFiles.length === 0}
                className="p-3 rounded-xl bg-accent text-black hover:bg-accent-dim transition-colors disabled:opacity-50 flex items-center"
              >
                <Send size={18} />
              </button>
            )}
          </div>
        ) : !bridgeCfg ? (
          <div className="flex items-center gap-2 px-4 py-3 text-sm text-amber-600 dark:text-amber-400 bg-amber-50 dark:bg-amber-900/20 rounded-xl">
            <WifiOff size={14} />
            <span>{t('sessions.bridgeNotAvailable')}</span>
          </div>
        ) : bridgeStatus === 'disconnected' || bridgeStatus === 'error' ? (
          <div className="flex items-center gap-2 px-4 py-3 text-sm text-amber-600 dark:text-amber-400 bg-amber-50 dark:bg-amber-900/20 rounded-xl">
            <WifiOff size={14} />
            <span>{t('sessions.bridgeDisconnected')}</span>
          </div>
        ) : (
          <div className="flex items-center gap-2 px-4 py-3 text-sm text-gray-400 bg-gray-50 dark:bg-gray-800/50 rounded-xl">
            <Loader2 size={14} className="animate-spin" />
            <span>{t('sessions.bridgeConnecting')}</span>
          </div>
        )}
      </div>
      </div>{/* /left chat column */}

      {/* Session drawer */}
      <SessionDrawer
        open={drawerOpen}
        onClose={() => setDrawerOpen(false)}
        sessions={sessions}
        currentSessionId={currentSession?.id || ''}
        onSelect={switchToSession}
        onNewSession={handleNewSession}
        onRename={(s) => setRenameTarget(s)}
        onTogglePin={togglePinSession}
      />

      {/* Command result panel */}
      <CommandResultPanel
        result={cmdResult}
        onClose={() => setCmdResult(null)}
        onCardAction={handleCardAction}
      />

      {/* Local file preview modal (agent-generated files) */}
      {previewFile && (
        <FilePreview
          filePath={previewFile.path}
          fileName={previewFile.fileName}
          onClose={() => setPreviewFile(null)}
          previewWidth={previewWidth}
          isDesktop={isDesktop}
          onResizeStart={beginResize}
        />
      )}

      {/* Project file browser drawer */}
      {fileBrowserOpen && (
        <ProjectFileBrowser
          open
          projectName={projectName || ''}
          onClose={() => setFileBrowserOpen(false)}
          onInsertFile={(relPath) => {
            setInput((prev) => (prev.trim() ? `${prev.trim()} ${relPath}` : relPath));
          }}
          previewWidth={previewWidth}
          isDesktop={isDesktop}
          onResizeStart={beginResize}
        />
      )}

      {/* Rename session modal */}
      {renameTarget && (
        <RenameSessionModal
          open
          project={projectName || ''}
          session={{ id: renameTarget.id, name: renameTarget.name }}
          onClose={() => setRenameTarget(null)}
          onSaved={(newName) => handleSessionRenamed(renameTarget, newName)}
        />
      )}
    </div>
  );
}
