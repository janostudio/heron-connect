import { useEffect, useState, useRef, useCallback, useMemo, memo, Fragment } from 'react';
import { useTranslation } from 'react-i18next';
import { useParams, useNavigate, useLocation } from 'react-router-dom';
import {
  ArrowLeft, Bot, Circle, WifiOff,
  FileText, Loader2, Download, FileDown,
  ChevronDown, Clock, X, Folder, FolderOpen, ChevronLeft, ChevronRight, ArrowUp, ArrowDown,
  Pin, PinOff, Pencil, RefreshCw, Share2, Check, Link2, Trash2,
} from 'lucide-react';
import { Button } from '@/components/ui';
import { listSessions, getSession, createSession, sessionTitle, updateSession, sortSessions, type Session, type SessionDetail } from '@/api/sessions';
import api from '@/api/client';
import { newConvKey } from '@/lib/webSessionKey';
import {
  useBridgeSocket, fetchBridgeConfig,
  type BridgeConfig, type BridgeIncoming, type BridgeStatus,
} from '@/hooks/useBridgeSocket';
import { type SlashCommand, slashCommands } from './CommandPalette';
import SessionDrawer from './SessionDrawer';
import CommandResultPanel, { type CommandResult } from './CommandResultPanel';
import RenameSessionModal from './RenameSessionModal';
import MessageRow from './MessageRow';
import ChatComposer, { type ChatComposerHandle } from './ChatComposer';
import { RenderMarkdown } from './markdownBlocks';
import { useChatSessions, historyToMessages } from './useChatSessions';
import { nowStamp } from './messageTime';
import type { ChatMsg, PickItem } from './chatMessage';
import { SequenceGuard } from '@/lib/sequenceGuard';
import {
  sessionsSignature, fileIsPreviewable, isMarkdown, isHtmlFile,
  CHAT_COMMANDS, classifyInput,
} from './chatHelpers';
import { cn, loadLS, saveLS, copyText } from '@/lib/utils';
import { createShare, revokeShare, absoluteShareURL, type ShareInfo } from '@/api/share';

// `chatCommands` produce output in the message stream (they change state);
// the rest are handled by their own result panel.
const chatCommands = CHAT_COMMANDS;
const knownCommands = new Set(slashCommands.map(c => c.cmd));

// ── Transcript ───────────────────────────────────────────────
//
// The message list is the expensive subtree: every row renders markdown
// (react-markdown + remark-gfm + rehype-highlight), and a long conversation
// holds hundreds of rows.
//
// It is memoized at module scope so that re-renders of ChatView itself —
// which happen for unrelated reasons (bridge status, drawers, modals, the
// resize drag) — do not walk the whole transcript. ChatView's per-conversation
// store keeps `messages` identity stable when a slice is untouched
// (see useChatSessions.updateSlice), and `onOpenFile` / `onCardAction` are
// useCallback-stabilised, so the memo actually holds.
//
// The scroll wiring (refs + onScroll) stays in the parent because the
// auto-scroll effect depends on the message list and must not be trapped here.
//
// `flex-1 overflow-y-auto` + ancestor `min-h-0` chain (Layout wrapper has
// `min-h-0` since v1.1.24) gives correct scrolling and lets the input area sit
// flush at the bottom. The earlier
// `max-h-[calc(100dvh-136px)] md:max-h-[calc(100dvh-192px)]` cap
// overestimated header+input height on PC (the 192px shadow left
// ~50px of dead space below the input on desktop).
const Transcript = memo(function Transcript({
  messages, typing, loading, projectName, onOpenFile, onCardAction,
  scrollRef, onScroll, messagesEnd,
}: {
  messages: ChatMsg[];
  typing: boolean;
  loading: boolean;
  projectName: string;
  onOpenFile: (path: string, fileName: string) => void;
  onCardAction: (value: string) => void;
  scrollRef: React.RefObject<HTMLDivElement | null>;
  onScroll: () => void;
  messagesEnd: React.RefObject<HTMLDivElement | null>;
}) {
  const { t } = useTranslation();
  return (
    <div
      ref={scrollRef}
      onScroll={onScroll}
      className="flex-1 overflow-y-auto overflow-x-hidden py-4 md:py-6 px-2"
    >
      {/* Single content child, so a ResizeObserver on it reports the whole
          transcript's height. Without this wrapper the scroller's
          firstElementChild is (depending on state) the empty-state block or the
          first message row, and observing it would track one row's height
          rather than the content that actually pushes the bottom down. The
          `space-y-5` that used to live on the scroller is here because the
          spacing must be inside the observed element.
          `min-h-full` keeps the empty state's `h-full` centering working: the
          scroller has a definite height (flex-1 in its column), so this wrapper
          gets at least that height and the inner percentage resolves. */}
      <div className="space-y-5 min-h-full">
        {messages.length === 0 && !loading && (
          <div className="flex flex-col items-center justify-center h-full text-center py-12">
            <div className="w-16 h-16 rounded-2xl bg-accent/10 flex items-center justify-center mb-4">
              <Bot size={32} className="text-accent" />
            </div>
            <p className="text-sm text-gray-500 dark:text-gray-400 mb-1">{t('chat.emptyHint')}</p>
            <p className="text-xs text-gray-400 dark:text-gray-500">{t('chat.slashHint')}</p>
          </div>
        )}
        {messages.map((msg) => (
          <MessageRow
            key={msg.id}
            msg={msg}
            projectName={projectName}
            onOpenFile={onOpenFile}
            onCardAction={onCardAction}
          />
        ))}
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
    </div>
  );
});

// ── File preview (local agent-generated files, served over HTTP) ──
// fileIsPreviewable / isMarkdown / isHtmlFile live in chatHelpers.ts.

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
//
// Files on disk are not pushed to the UI, so the toolbar carries a manual
// refresh button that re-fetches the latest bytes. HTML files additionally
// toggle between the rendered effect (sandboxed iframe, default) and the raw
// source text.
function FileContentView({ filePath, fileName }: { filePath: string; fileName: string }) {
  const [state, setState] = useState<'loading' | 'ready' | 'error'>('loading');
  const [contentType, setContentType] = useState('');
  const [text, setText] = useState('');
  const [dataUrl, setDataUrl] = useState('');
  const [error, setError] = useState('');
  // Bump refreshTick to re-fetch the file from the server.
  const [refreshTick, setRefreshTick] = useState(0);
  // HTML files: 'rendered' shows the page effect, 'source' shows the text.
  const [htmlView, setHtmlView] = useState<'rendered' | 'source'>('rendered');

  // Reset the HTML view mode only when switching files — a refresh keeps the
  // mode the user is currently looking at.
  useEffect(() => {
    setHtmlView('rendered');
  }, [filePath, fileName]);

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
    // api.file always fetches with cache: 'no-store', so a manual refresh
    // reliably pulls the latest bytes from the server.
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
  }, [filePath, fileName, refreshTick]);

  const previewable = fileIsPreviewable(fileName, contentType);
  const isHtml = isHtmlFile(fileName, contentType);
  const showHtmlRendered = isHtml && htmlView === 'rendered';

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
          {/* Toolbar: HTML view toggle + manual refresh (files change on disk
              without any push — refresh re-fetches the latest bytes). */}
          {previewable && (
            <div className="flex items-center justify-end gap-2">
              {isHtml && (
                <div className="flex items-center rounded-lg border border-gray-200 dark:border-gray-700 overflow-hidden text-xs">
                  <button type="button" onClick={() => setHtmlView('rendered')}
                    className={cn(
                      'px-2.5 py-1 transition-colors',
                      showHtmlRendered
                        ? 'bg-accent/15 text-accent font-medium'
                        : 'text-gray-500 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-white/[0.06]',
                    )}>
                    效果
                  </button>
                  <button type="button" onClick={() => setHtmlView('source')}
                    className={cn(
                      'px-2.5 py-1 transition-colors',
                      !showHtmlRendered
                        ? 'bg-accent/15 text-accent font-medium'
                        : 'text-gray-500 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-white/[0.06]',
                    )}>
                    源码
                  </button>
                </div>
              )}
              <button
                type="button"
                onClick={() => setRefreshTick((t) => t + 1)}
                title="刷新（重新获取文件内容）"
                aria-label="Refresh file"
                className="p-1.5 rounded-lg text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-white hover:bg-gray-100 dark:hover:bg-white/[0.08] transition-colors"
              >
                <RefreshCw size={14} />
              </button>
            </div>
          )}
          {previewable && text !== '' && showHtmlRendered && (
            // Rendered mode: sandboxed iframe. allow-scripts lets the page's
            // inline JS (charts etc.) run, but WITHOUT allow-same-origin the
            // frame lives in an opaque origin — it cannot reach our cookies
            // or the management API token. srcDoc means relative sibling-file
            // references won't resolve (only inline/CDN assets render).
            // key={refreshTick} remounts the frame on refresh so scripts
            // re-run even when the bytes are identical.
            <iframe
              key={refreshTick}
              srcDoc={text}
              title={fileName}
              sandbox="allow-scripts allow-popups allow-forms"
              className="w-full h-[70vh] rounded-lg border border-gray-200 dark:border-gray-700 bg-white"
            />
          )}
          {previewable && text !== '' && !showHtmlRendered && (
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
  const { t } = useTranslation();
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

  // ── File sharing ──
  // Sharing hands out a link that works without login, so it is opt-in per
  // file and always revocable. Keyed by the project-relative path so switching
  // files does not leave a stale link on screen.
  const [share, setShare] = useState<ShareInfo | null>(null);
  const [shareBusy, setShareBusy] = useState(false);
  const [shareCopied, setShareCopied] = useState<'ok' | 'fail' | null>(null);
  const [shareError, setShareError] = useState('');

  // Clear the share panel when the user navigates to a different file.
  useEffect(() => {
    setShare(null);
    setShareError('');
    setShareCopied(null);
  }, [currentFileRel]);

  const copyShareLink = useCallback(async (url: string) => {
    const ok = await copyText(absoluteShareURL(url));
    setShareCopied(ok ? 'ok' : 'fail');
    setTimeout(() => setShareCopied(null), 2000);
  }, []);

  const createShareForCurrent = useCallback(async () => {
    if (!currentFileRel) return;
    setShareBusy(true);
    setShareError('');
    try {
      const info = await createShare(projectName, currentFileRel);
      setShare(info);
      await copyShareLink(info.url);
    } catch (e: any) {
      setShareError(e?.message || 'Failed to create share link');
    } finally {
      setShareBusy(false);
    }
  }, [currentFileRel, projectName, copyShareLink]);

  const revokeCurrentShare = useCallback(async () => {
    if (!share) return;
    setShareBusy(true);
    setShareError('');
    try {
      await revokeShare(share.token);
      setShare(null);
    } catch (e: any) {
      setShareError(e?.message || 'Failed to revoke share link');
    } finally {
      setShareBusy(false);
    }
  }, [share]);

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
          {/* Breadcrumb / current dir — click to open dropdown.
              Each segment is its own button so you can jump straight to an
              ancestor (project / auto_bugfix / docs) instead of only stepping
              up one level at a time via "上一级". */}
          <div className="flex items-center gap-1 min-w-0 text-sm font-medium">
            <button
              type="button"
              onClick={() => openDir('')}
              className="flex items-center gap-1 shrink-0 rounded px-1 -mx-1 py-0.5 text-gray-900 dark:text-white hover:bg-gray-100 dark:hover:bg-white/[0.08] transition-colors min-w-0"
            >
              <Folder size={16} className="text-gray-500 dark:text-gray-400 shrink-0" />
              <span className="truncate">{projectName}</span>
            </button>
            {breadcrumbSegments.map((seg, i) => {
              const isCurrent = i === breadcrumbSegments.length - 1;
              return (
                <Fragment key={`${i}-${seg}`}>
                  <span className="text-gray-400 dark:text-gray-500 shrink-0">/</span>
                  <button
                    type="button"
                    onClick={() => openDir(breadcrumbSegments.slice(0, i + 1).join('/'))}
                    aria-current={isCurrent ? 'true' : undefined}
                    className={cn(
                      'shrink-0 rounded px-1 -mx-1 py-0.5 transition-colors truncate max-w-[12rem]',
                      isCurrent
                        ? 'text-accent'
                        : 'text-gray-900 dark:text-white hover:bg-gray-100 dark:hover:bg-white/[0.08]',
                    )}
                    title={breadcrumbSegments.slice(0, i + 1).join('/')}
                  >
                    {seg}
                  </button>
                </Fragment>
              );
            })}
            <button
              type="button"
              onClick={() => setDropdownOpen((v) => !v)}
              aria-label="Toggle directory list"
              className="shrink-0 p-0.5 rounded text-gray-500 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-white/[0.08] transition-colors"
            >
              <ChevronDown size={14} className={cn('transition-transform', dropdownOpen && 'rotate-180')} />
            </button>
          </div>

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
          <div className="flex items-center gap-2">
            {/* Share creates a link that needs no login, so its state is
                shown inline (copied / failed) rather than in a global toast —
                matching the CopyButton pattern used elsewhere. */}
            <button
              type="button"
              onClick={() => (share ? copyShareLink(share.url) : createShareForCurrent())}
              disabled={!currentFile || shareBusy}
              className={cn(
                'flex items-center gap-2 px-3 py-1.5 rounded-lg text-xs font-medium transition-colors',
                shareCopied === 'ok'
                  ? 'text-emerald-600 dark:text-emerald-400 bg-emerald-50 dark:bg-emerald-900/20'
                  : shareCopied === 'fail'
                    ? 'text-red-600 dark:text-red-400 bg-red-50 dark:bg-red-900/20'
                    : 'text-gray-600 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-white/[0.08]',
                'disabled:opacity-40 disabled:pointer-events-none',
              )}
              title={share ? t('chat.share.copyLink') : t('chat.share.share')}
            >
              {shareBusy ? <Loader2 size={15} className="animate-spin" />
                : shareCopied === 'ok' ? <Check size={15} />
                : shareCopied === 'fail' ? <X size={15} />
                : <Share2 size={15} />}
              {shareCopied === 'ok' ? t('chat.share.linkCopied') : t('chat.share.share')}
            </button>
            {share && (
              <button
                type="button"
                onClick={revokeCurrentShare}
                disabled={shareBusy}
                className="flex items-center gap-2 px-3 py-1.5 rounded-lg text-xs font-medium text-gray-500 dark:text-gray-400 hover:bg-red-50 dark:hover:bg-red-900/20 hover:text-red-600 dark:hover:text-red-400 disabled:opacity-40 disabled:pointer-events-none transition-colors"
                title={t('chat.share.revoke')}
              >
                <Trash2 size={15} /> {t('chat.share.revoke')}
              </button>
            )}
            <Button onClick={triggerDownload} disabled={!currentFile} className="flex items-center gap-2">
              <Download size={15} /> Download
            </Button>
          </div>
        </div>

        {/* Share panel: the created link plus an explicit "anyone with this
            link can read the file" warning, since sharing bypasses login. */}
        {(share || shareError) && (
          <div className="px-4 pb-3 shrink-0">
            {shareError ? (
              <div className="flex items-start gap-2 px-3 py-2 rounded-lg text-xs bg-red-50 dark:bg-red-900/20 text-red-700 dark:text-red-400">
                <X size={14} className="shrink-0 mt-0.5" />
                <span className="break-all">{shareError}</span>
              </div>
            ) : (
              <div className="rounded-lg border border-amber-200 dark:border-amber-900/50 bg-amber-50/60 dark:bg-amber-900/10 p-3 space-y-2">
                <div className="text-[11px] text-amber-700 dark:text-amber-400">
                  {t('chat.share.warning')}
                </div>
                <div className="flex items-center gap-2">
                  <Link2 size={14} className="shrink-0 text-gray-400" />
                  <input
                    readOnly
                    value={absoluteShareURL(share!.url)}
                    onFocus={(e) => e.currentTarget.select()}
                    className="flex-1 min-w-0 px-2 py-1 rounded-md text-[11px] font-mono bg-white dark:bg-black/30 border border-gray-200 dark:border-white/10 text-gray-700 dark:text-gray-300"
                  />
                  <button
                    type="button"
                    onClick={() => copyShareLink(share!.url)}
                    className="shrink-0 px-2 py-1 rounded-md text-[11px] font-medium text-accent hover:bg-accent/10 transition-colors"
                  >
                    {t('chat.share.copyLink')}
                  </button>
                </div>
              </div>
            )}
          </div>
        )}
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

// ── Main component ───────────────────────────────────────────

export default function ChatView() {
  const { t } = useTranslation();
  const { name: projectName, id: routeSessionId } = useParams<{ name: string; id?: string }>();
  const navigate = useNavigate();
  const location = useLocation();

  // Session state
  const [sessions, setSessions] = useState<Session[]>([]);
  const [currentSession, setCurrentSession] = useState<SessionDetail | null>(null);
  // The in-progress message text lives in <ChatComposer>, not here. Keeping it
  // in this component made every keystroke re-render the whole page including
  // the transcript — see the note on ChatComposer.
  const [pickedFiles, setPickedFiles] = useState<PickItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [bridgeCfg, setBridgeCfg] = useState<BridgeConfig | null>(null);
  // Whether the user explicitly picked a session from the drawer
  const [userPickedSession, setUserPickedSession] = useState(false);

  // UI state
  const [cmdOpen, setCmdOpen] = useState(false);
  const [drawerOpen, setDrawerOpen] = useState(false);
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
  //
  // The active drag's listeners are held in a ref so the unmount cleanup below
  // can detach them. Previously they were only removed on mouseup, so
  // unmounting mid-drag (navigating away, closing the preview with ESC) leaked
  // the document listeners AND left `cursor`/`userSelect` stuck on <body>.
  const resizeCleanupRef = useRef<(() => void) | null>(null);

  const endResize = useCallback(() => {
    resizeCleanupRef.current?.();
    resizeCleanupRef.current = null;
  }, []);

  const beginResize = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    if (!isDesktop) return;
    // A previous drag that never saw a mouseup must not stack listeners.
    endResize();
    const startX = e.clientX;
    const startWidth = previewWidth;
    const clamp = (w: number) => Math.min(Math.max(w, 320), Math.floor(window.innerWidth * 0.7));
    const onMove = (ev: MouseEvent) => {
      setPreviewWidth(clamp(startWidth + (startX - ev.clientX)));
    };
    const cleanup = () => {
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', cleanup);
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
    };
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', cleanup);
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
    resizeCleanupRef.current = cleanup;
  }, [isDesktop, previewWidth, endResize]);

  // Detach any in-flight drag listeners when the view unmounts.
  useEffect(() => () => { resizeCleanupRef.current?.(); }, []);

  // Persist the width when it changes (mouseup lands here via the move handler
  // that runs setPreviewWidth; we save on every settled change).
  useEffect(() => {
    saveLS('cc_preview_width', previewWidth);
  }, [previewWidth]);

  // Rename session modal target (null = closed)
  const [renameTarget, setRenameTarget] = useState<Session | null>(null);

  const messagesEnd = useRef<HTMLDivElement>(null);
  // Imperative handle on the composer: the project file browser needs to seed
  // the draft with a path without lifting the draft state back up here.
  const composerRef = useRef<ChatComposerHandle>(null);

  // Per-conversation live state. Each Web conversation owns a distinct
  // session_key, and the bridge broadcasts every frame to every client — so
  // the page receives live output for all conversations. Keeping a slice per
  // conversation (instead of one shared transcript) is what lets a background
  // conversation keep streaming while you read another one.
  const store = useChatSessions();
  const { slices, slicesRef, setSlices, ensureSlice, updateSlice, apply, seedHistory, setServerRunning, settleAll } = store;

  // ── Stable routing keys ──────────────────────────────────────
  //
  // `draftKey` is ONE minted key for a conversation that has not been
  // persisted yet. It must be stable: the old code called newConvKey() inside
  // a useMemo, so a fresh random key was minted on every identity change,
  // making the key (and therefore the session-status lookup) unstable.
  //
  // It is minted lazily (on first send into an unsaved conversation) and
  // cleared as soon as that conversation gets a real server id, so the next
  // new conversation mints its own key.
  const [draftKey, setDraftKey] = useState('');
  const draftProjectRef = useRef('');
  useEffect(() => {
    // Switching projects invalidates the draft.
    if (draftProjectRef.current !== projectName) {
      draftProjectRef.current = projectName || '';
      setDraftKey('');
    }
  }, [projectName]);

  // The conversation currently on screen. `currentSession.id` is the routing
  // id once persisted; for an unsaved draft we key the slice by its own key.
  const viewedId = currentSession?.id || draftKey;
  const sessionKey = currentSession?.session_key || draftKey;

  // The frame handler and the session-list poll must stay identity-stable —
  // useBridgeSocket captures the handler once as onMessage, and adding
  // `viewedId` to the poll's deps would rebuild its interval on every
  // navigation — so both read the current view id through this ref.
  const viewedIdRef = useRef(viewedId);
  viewedIdRef.current = viewedId;

  // Get (or lazily mint) the routing id for the conversation on screen. Called
  // before sending, so a brand-new conversation has a slice to receive into.
  const ensureViewedId = useCallback((): string => {
    if (currentSession?.id) return currentSession.id;
    if (draftKey) return draftKey;
    const key = newConvKey(projectName || '');
    setDraftKey(key);
    return key;
  }, [currentSession?.id, draftKey, projectName]);

  // session_key → conversation id index, used to route frames that carry only
  // a session_key (notably typing_start/typing_stop, which the backend does
  // not stamp with a session_id) and frames from a not-yet-persisted draft.
  const keyToIdRef = useRef<Map<string, string>>(new Map());
  useEffect(() => {
    if (sessionKey && viewedId) keyToIdRef.current.set(sessionKey, viewedId);
  }, [sessionKey, viewedId]);
  useEffect(() => {
    for (const s of sessions) {
      if (s.session_key && s.id) keyToIdRef.current.set(s.session_key, s.id);
    }
  }, [sessions]);

  // The slice for the conversation on screen (never undefined for a live view).
  const viewedSlice = viewedId ? slices[viewedId] : undefined;
  const messages = viewedSlice?.messages ?? [];
  const typing = viewedSlice?.typing ?? false;
  const serverRunning = viewedSlice?.serverRunning ?? false;
  const cmdResult: CommandResult | null = viewedSlice?.cmdResult ?? null;

  // When an unsaved draft becomes a persisted session, its live output sits
  // under the draft key while the view now points at the server id. Move the
  // slice across (keyed by session_key so it is unambiguous) and release the
  // draft key so the next "new conversation" mints its own.
  useEffect(() => {
    const id = currentSession?.id;
    const key = currentSession?.session_key;
    if (!id || !key || !draftKey || draftKey !== key) return;
    setDraftKey('');
    setSlices(prev => {
      if (!prev[draftKey] || prev[id]) return prev;
      const { [draftKey]: moved, ...rest } = prev;
      return { ...rest, [id]: moved };
    });
  }, [currentSession?.id, currentSession?.session_key, draftKey, setSlices]);

  // Guards against out-of-order responses when the user switches conversations
  // quickly: only the newest fetch is allowed to publish its result.
  const fetchSeqRef = useRef(new SequenceGuard());

  // Load project sessions and auto-select latest (or the one specified in the URL)
  const fetchData = useCallback(async () => {
    if (!projectName) return;
    const seq = fetchSeqRef.current;
    const ticket = seq.begin();
    setLoading(true);
    try {
      const [{ sessions: allSessions }, cfg] = await Promise.all([
        listSessions(projectName),
        fetchBridgeConfig(),
      ]);
      // A newer fetch started while we were awaiting — discard this result.
      if (!seq.isCurrent(ticket)) return;
      // Keep the previous object when nothing actually changed so downstream
      // consumers don't see a new identity (the bridge socket keys off the
      // routing values, but other memos may still depend on the object).
      setBridgeCfg(prev =>
        prev && cfg && prev.port === cfg.port && prev.path === cfg.path && prev.token === cfg.token
          ? prev
          : cfg
      );
      const sorted = sortSessions(allSessions || []);
      setSessions(sorted);

      const target = routeSessionId
        ? sorted.find(s => s.id === routeSessionId) || null
        : sorted[0];

      if (target) {
        setUserPickedSession(!!routeSessionId);
        const detail = await getSession(projectName, target.id, 200);
        if (!seq.isCurrent(ticket)) return;
        setCurrentSession(detail);
        // Seed (not replace) the conversation's slice: if it already holds
        // live output that arrived from the bridge, that content is newer than
        // the persisted history and must survive.
        seedHistory(target.id, historyToMessages(detail.history || []));
        // REST is authoritative for "is a turn still running". A page reload
        // wipes every bridge frame, so typing/streaming read false even though
        // the backend turn is still going — without this the composer would
        // offer "send" while the agent is mid-answer.
        setServerRunning(target.id, !!detail.running);
      } else {
        setCurrentSession(null);
      }
    } finally {
      if (seq.isCurrent(ticket)) setLoading(false);
    }
  }, [projectName, routeSessionId, seedHistory, setServerRunning]);

  useEffect(() => { fetchData(); }, [fetchData]);

  // Periodically refresh the session list so execution-status badges (running
  // / waiting permission) stay current while other sessions run in parallel.
  //
  // It also keeps the OPEN conversation's server-authoritative busy flag in
  // sync. This is what unsticks a red stop button when a turn ends but the
  // terminal bridge frame was lost, and what flips it back when another client
  // stops a turn. It does NOT reopen the "history clobbers live content"
  // problem the old comment guarded against: only `serverRunning` is written,
  // never `messages`/`typing`.
  //
  // The poll fires every 5s but the payload is almost always identical. Since
  // sortSessions() always builds a fresh array, an unconditional setSessions
  // re-rendered the whole ChatView (including the memoized MessageRow list and
  // the always-mounted SessionDrawer) every 5s forever. Compare a cheap
  // signature of the badge-relevant fields and bail out when nothing changed.
  const refreshSessions = useCallback(async () => {
    if (!projectName) return;
    try {
      const { sessions: allSessions } = await listSessions(projectName);
      const sorted = sortSessions(allSessions || []);
      setSessions(prev => (sessionsSignature(prev) === sessionsSignature(sorted) ? prev : sorted));

      // Read the open conversation from the ref, not from a closure: depending
      // on `viewedId` here would rebuild this callback (and its interval) on
      // every navigation.
      const openId = viewedIdRef.current;
      if (openId) {
        const open = sorted.find(s => s.id === openId);
        if (open) setServerRunning(openId, !!open.running);
      }
    } catch { /* transient — keep last known list */ }
  }, [projectName, setServerRunning]);

  useEffect(() => {
    const timer = setInterval(refreshSessions, 5000);
    return () => clearInterval(timer);
  }, [refreshSessions]);

  // Switch to a different session (user explicitly chose from drawer).
  //
  // This only navigates: the URL change re-runs fetchData (it depends on
  // routeSessionId), which loads the detail and seeds the slice. Doing the
  // fetch here as well produced two concurrent getSession+seedHistory calls
  // for the same conversation, with the slower one winning.
  const switchToSession = useCallback((s: Session) => {
    if (!projectName) return;
    setDrawerOpen(false);
    setUserPickedSession(true);
    navigate(`/chat/${projectName}/${s.id}`, { replace: true });
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

  // The handler must acknowledge preview_start frames, but the ack sender
  // comes from useBridgeSocket — which is created AFTER the handler (it needs
  // the handler as its onMessage). Route the ack through a ref so the handler
  // keeps a stable identity instead of being re-created on every render.
  const sendPreviewAckRef = useRef<((refId: string, handle: string) => void) | null>(null);

  // Handle a bridge frame by ROUTING it to the conversation it belongs to —
  // not by asking whether it belongs to the conversation on screen.
  //
  // The bridge multicasts every frame to every connected client, so this page
  // sees live output for conversations it is not currently displaying. Those
  // frames must be filed into their own conversation's slice so the turn keeps
  // accumulating while the user reads something else.
  //
  // Resolution order (the frame's own identifiers, strongest first):
  //   1. session_id  — the server session id; most frames carry it.
  //   2. session_key — always present, and the ONLY signal for typing_start /
  //                    typing_stop (the backend does not stamp those) and for
  //                    a conversation that has not been persisted yet.
  //   3. drop        — a conversation this client never opened (e.g. another
  //                    tab's conversation on the same platform).
  const handleBridgeMessage = useCallback((msg: BridgeIncoming) => {
    const msgKey = (msg as any).session_key as string | undefined;
    const msgID = (msg as any).session_id as string | undefined;

    // Resolve the target conversation. Prefer the server session id, but only
    // for a conversation this client knows about (an existing slice, or the
    // one on screen) — otherwise fall back to the session_key index. Falling
    // back unconditionally on an unknown id would let another tab's frame mint
    // a phantom slice here.
    let targetId: string | undefined;
    if (msgID && (msgID === viewedIdRef.current || slicesRef.current[msgID])) {
      targetId = msgID;
    }
    if (!targetId && msgKey) {
      targetId = keyToIdRef.current.get(msgKey);
    }
    if (!targetId) {
      if (msgKey || msgID) {
        console.warn('[heron] bridge frame could not be routed to a conversation', {
          type: msg.type, session_key: msgKey, session_id: msgID,
        });
      }
      return;
    }

    // Learn the key → id association from any frame that carries both, so
    // key-only frames (typing_*) resolve from then on.
    if (msgKey && msgID) keyToIdRef.current.set(msgKey, msgID);

    // A preview must be acknowledged so the backend starts streaming it. Mint
    // the handle here and hand the same value to the reducer, so the ack and
    // the rendered message id agree.
    let previewHandle: string | undefined;
    if (msg.type === 'preview_start') {
      const ps = msg as Extract<BridgeIncoming, { type: 'preview_start' }>;
      previewHandle = `web-preview-${(slicesRef.current[targetId]?.previewHandleCounter ?? 0) + 1}`;
      sendPreviewAckRef.current?.(ps.ref_id, previewHandle);
    }

    apply(targetId, msg, previewHandle);
  }, [apply]);

  const { status: bridgeStatus, sendMessage: bridgeSend, sendCardAction, sendPreviewAck } = useBridgeSocket({
    bridgeCfg,
    sessionKey,
    projectName: projectName || '',
    onMessage: handleBridgeMessage,
  });
  sendPreviewAckRef.current = sendPreviewAck;

  // ── Auto-scroll ──────────────────────────────────────────────
  //
  // Previously this ran `scrollIntoView({behavior:'smooth'})` on every
  // change to `messages` — i.e. on every streaming delta. Hundreds of queued
  // smooth-scroll animations per turn is a major source of the perceived
  // lag, and it also yanked the viewport back down while the user was trying
  // to scroll up and read earlier output.
  //
  // Now: follow only when the user is already near the bottom, scroll
  // instantly (not smoothly) while streaming, and coalesce multiple requests
  // within the same animation frame.
  //
  // Three layers, because any one of them alone leaves the first paint stuck
  // short of the bottom (the v1.3.4 bug):
  //
  //  1. Signature effect — scrolls when the message list changes. This is the
  //     streaming/send path.
  //  2. ResizeObserver on the transcript — scrolls when the CONTENT grows
  //     without a message-list change. This is what covers async height:
  //     HistoryImage swaps a 120x80 placeholder for a (up to 220px) <img>
  //     after its fetch resolves (see markdownBlocks.tsx), and
  //     rehype-highlight re-lays-out code blocks. Both land well after the
  //     requestAnimationFrame in layer 1 has already fired, so layer 1 alone
  //     lands on the placeholder-height bottom and stops mid-transcript.
  //  3. Programmatic-scroll flag — suppresses the scroll events that layers 1
  //     and 2 themselves generate, so a content growth cannot be mistaken for
  //     the user scrolling up and silently disable stickiness.
  //
  // Layer 1 can also fail to fire at all: seedHistory merges into the slice and
  // mergeHistoryIntoSlice deliberately keeps the slice identity when the
  // history is positionally identical (chatSessionsCore.ts), so scrollSignature
  // does not change. The session-switch effect below re-attaches unconditionally
  // to cover that.
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const stickToBottomRef = useRef(true);
  const scrollRafRef = useRef<number | null>(null);

  // Set while WE are moving the viewport (auto-scroll / jump-to-latest), so a
  // scroll event that fires as a result of it does not get read as user intent.
  // Cleared on the next frame after the scroll has been dispatched; a scroll
  // event is queued before that frame, so handleScroll always sees the flag.
  const programmaticScrollRef = useRef(false);

  // Mirror of `stickToBottomRef` for the "jump to latest" button. The ref is
  // read inside the scroll effect (which must not re-run on its own), so it
  // stays a ref; this state only drives the button's visibility and is
  // updated at most once per crossing (see handleScroll).
  const [atBottom, setAtBottom] = useState(true);

  // Scroll to the newest message instantly and mark the move as ours.
  const scrollToBottom = useCallback(() => {
    programmaticScrollRef.current = true;
    messagesEnd.current?.scrollIntoView({ behavior: 'auto', block: 'end' });
    // Clear on the next frame: the scroll event this call queued is delivered
    // before it, so handleScroll still observes the flag.
    requestAnimationFrame(() => { programmaticScrollRef.current = false; });
  }, []);

  const handleScroll = useCallback(() => {
    const el = scrollRef.current;
    if (!el) return;
    // Our own scroll landing at the bottom must not be re-interpreted as a
    // stickiness decision — and more importantly, a scroll event caused by the
    // content growing under a stationary scrollTop (distance suddenly > 80)
    // must not be read as "the user scrolled up".
    if (programmaticScrollRef.current) return;
    const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
    const near = distance < 80;
    stickToBottomRef.current = near;
    // Only flip the state on an actual crossing — a plain setState per scroll
    // event would re-render ChatView (and its whole toolbar) on every wheel tick.
    setAtBottom(prev => (prev === near ? prev : near));
  }, []);

  // Re-attach to the bottom on the NEXT render, without scrolling right now.
  //
  // `updateSlice` only schedules a re-render, so at call time `messagesEnd` has
  // not yet moved past the bubble being appended — scrolling here would land
  // one message short. So we just force the stickiness flags; layers 1 and 2 of
  // the Auto-scroll machinery do the real scroll once the new message is in the
  // DOM (and re-settle it as the row's content finishes laying out).
  //
  // Used by the send paths: sending is an explicit "give me the newest content"
  // action, so it overrides a user who had scrolled up. Without this they would
  // send a message while `stickToBottomRef` was false and never see it.
  const reattachToBottom = useCallback(() => {
    stickToBottomRef.current = true;
    setAtBottom(true);
  }, []);

  // One-shot jump to the newest message, used by the floating button.
  const jumpToBottom = useCallback(() => {
    reattachToBottom();
    programmaticScrollRef.current = true;
    messagesEnd.current?.scrollIntoView({ behavior: 'smooth', block: 'end' });
    // The smooth animation emits scroll events over several frames; keep the
    // flag up long enough that none of them is read as user intent.
    const clear = () => { programmaticScrollRef.current = false; };
    requestAnimationFrame(clear);
    setTimeout(clear, 400);
  }, [reattachToBottom]);

  // Scroll signature: total message count plus the length of the last
  // message. Depend on this instead of the `messages` array identity so
  // re-renders that don't add content (e.g. a settle pass) don't scroll.
  const lastMsg = messages[messages.length - 1];
  const scrollSignature = `${messages.length}:${lastMsg?.content.length ?? 0}:${lastMsg?.streaming ? 1 : 0}`;

  // Layer 1: message list changed (streaming delta, send, settle).
  useEffect(() => {
    if (!stickToBottomRef.current) return;
    if (scrollRafRef.current != null) return;
    scrollRafRef.current = requestAnimationFrame(() => {
      scrollRafRef.current = null;
      scrollToBottom();
    });
  }, [scrollSignature, typing, scrollToBottom]);

  // Layer 2: the transcript's rendered height changed without a message-list
  // change. This is the fix for the first paint stopping short: the
  // HistoryImage placeholder → real image swap, syntax highlighting, and font
  // loading all resize the content after layer 1 has already run.
  //
  // The observed element is the transcript's single content wrapper (see the
  // Transcript component) — observing the scroller alone would not report
  // anything, because its own border box is fixed by the flex parent while the
  // content inside it overflows.
  useEffect(() => {
    if (typeof ResizeObserver === 'undefined') return;
    const scroller = scrollRef.current;
    const content = scroller?.firstElementChild;
    if (!scroller || !content) return;
    let lastHeight = scroller.scrollHeight;
    const ro = new ResizeObserver(() => {
      // Ignore growth while the user is reading history, and ignore shrinks —
      // those are removals (a settled placeholder, a collapsed card) where
      // following would fight the user.
      if (!stickToBottomRef.current) { lastHeight = scroller.scrollHeight; return; }
      if (scroller.scrollHeight <= lastHeight) { lastHeight = scroller.scrollHeight; return; }
      lastHeight = scroller.scrollHeight;
      // Coalesce with layer 1 through the shared rAF slot so a burst of
      // resizes costs one scroll, not one per mutation.
      if (scrollRafRef.current != null) return;
      scrollRafRef.current = requestAnimationFrame(() => {
        scrollRafRef.current = null;
        scrollToBottom();
      });
    });
    ro.observe(content);
    return () => ro.disconnect();
  }, [scrollToBottom, loading, viewedId]);

  // Switching (or first opening) a conversation is an explicit "show me this
  // conversation" action, so it re-attaches to the bottom unconditionally —
  // including when the user had scrolled up in the PREVIOUS conversation.
  //
  // This is the layer that covers scrollSignature not changing: seedHistory
  // merges positionally-identical history into the slice and keeps the slice
  // identity (chatSessionsCore.ts), so layer 1 never fires and the transcript
  // is left wherever the scrollTop happened to be.
  //
  // The rAF here may run before the seeded rows are laid out; layer 2 picks up
  // the slack on the resize that follows. Both are needed.
  useEffect(() => {
    if (!viewedId) return;
    stickToBottomRef.current = true;
    setAtBottom(true);
    const id = requestAnimationFrame(scrollToBottom);
    return () => cancelAnimationFrame(id);
  }, [viewedId, scrollToBottom]);

  // Cancel a pending scroll frame on unmount.
  useEffect(() => () => {
    if (scrollRafRef.current != null) cancelAnimationFrame(scrollRafRef.current);
  }, []);

  // If the bridge connection drops mid-turn, no terminal event will arrive for
  // ANY conversation — settle their local stream flags so no red stop button
  // stays stuck. `serverRunning` is intentionally left alone (see settleAll):
  // it is REST-sourced truth, and clearing it here would show a sendable
  // composer for a conversation the backend is still working on.
  useEffect(() => {
    if (bridgeStatus !== 'connected') {
      settleAll();
    }
  }, [bridgeStatus, settleAll]);

  // True while the VIEWED conversation is actively producing a reply. Scoped to
  // the conversation on screen: a background conversation running in parallel
  // must not light up this one's stop button.
  //
  // Busy = the backend says a turn is executing (authoritative, survives a
  // reload) OR this client is locally streaming. OR-ed rather than replaced:
  // the REST flag lags by up to one poll interval, while the WS flag is instant
  // but empty after a reload — together they cover both edges. Never AND-ed,
  // because either source alone may know about a turn the other cannot see.
  const isRunning = serverRunning || typing || messages.some(m => m.streaming);

  const handleStop = useCallback(() => {
    bridgeSend('/stop');
  }, [bridgeSend]);

  // Per-file size cap before we base64-encode it into the bridge frame.
  // base64 inflates ~33%, and the WebSocket frame carries it all in memory.
  const MAX_UPLOAD_BYTES = 10 * 1024 * 1024;

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
    });
  }, [t]);

  const removePickedFile = useCallback((id: string) => {
    setPickedFiles(prev => prev.filter(p => p.id !== id));
  }, []);

  const stripDataUrlPrefix = useCallback((dataUrl: string): string => {
    const comma = dataUrl.indexOf(',');
    return comma >= 0 ? dataUrl.slice(comma + 1) : dataUrl;
  }, []);

  // Send message. `content` comes from the composer (which owns the draft), so
  // this callback has no dependency on the text being typed.
  const handleSend = useCallback((content: string) => {
    if (isRunning) return;
    if (!content.trim() && pickedFiles.length === 0) return;
    if (bridgeStatus !== 'connected') return;
    const text = content.trim();

    // Build media payload (only if any attachment is attached).
    const images: { mime_type: string; data: string; file_name?: string }[] = [];
    const files: { mime_type: string; data: string; file_name: string }[] = [];
    pickedFiles.forEach((p) => {
      const b64 = stripDataUrlPrefix(p.dataUrl);
      if (p.kind === 'image') images.push({ mime_type: p.mime_type, data: b64, file_name: p.name });
      else files.push({ mime_type: p.mime_type, data: b64, file_name: p.name });
    });
    const media = images.length > 0 || files.length > 0 ? { ...(images.length ? { images } : {}), ...(files.length ? { files } : {}) } : undefined;

    // Make sure the conversation's slice exists BEFORE sending, so the very
    // first frame (which may only carry a session_key) has somewhere to land.
    const targetId = ensureViewedId();
    ensureSlice(targetId);

    const { token: cmdToken, goesToPanel } = classifyInput(text, knownCommands);
    if (goesToPanel) {
      updateSlice(targetId, s => ({ ...s, pendingCmd: cmdToken }));
    } else {
      updateSlice(targetId, s => ({
        ...s,
        messages: [...s.messages, {
          id: `user-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
          role: 'user' as const,
          content: text,
          timestamp: nowStamp(),
          localMedia: pickedFiles.length > 0 ? pickedFiles : undefined,
        }],
      }));
    }
    bridgeSend(text, media, currentSession?.id);
    setPickedFiles([]);
    reattachToBottom();
  }, [pickedFiles, bridgeStatus, bridgeSend, isRunning, stripDataUrlPrefix, currentSession?.id, ensureViewedId, ensureSlice, updateSlice, reattachToBottom]);

  // Stable file-open handler. Must NOT be an inline arrow at the call site:
  // a new function identity on every render would defeat the React.memo on
  // MessageRow / RenderMarkdown / Transcript.
  const handleOpenFile = useCallback((path: string, fileName: string) => {
    setPreviewFile({ path, fileName });
  }, []);

  const handleCmdSelect = useCallback((cmd: SlashCommand) => {
    setCmdOpen(false);
    if (bridgeStatus !== 'connected') return;
    const targetId = ensureViewedId();
    ensureSlice(targetId);

    if (chatCommands.has(cmd.cmd)) {
      updateSlice(targetId, s => ({
        ...s,
        messages: [...s.messages, {
          id: `user-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
          role: 'user' as const,
          content: cmd.cmd,
          timestamp: nowStamp(),
        }],
      }));
    } else {
      updateSlice(targetId, s => ({ ...s, pendingCmd: cmd.cmd }));
    }
    bridgeSend(cmd.cmd);
    reattachToBottom();
  }, [bridgeStatus, bridgeSend, ensureViewedId, ensureSlice, updateSlice, reattachToBottom]);

  const handleCardAction = useCallback((value: string) => {
    if (bridgeStatus !== 'connected') return;
    // If the command panel is showing for this conversation, route the
    // follow-up response back to that panel.
    if (viewedId) {
      const shown = slicesRef.current[viewedId]?.cmdResult;
      if (shown) {
        updateSlice(viewedId, s => ({ ...s, pendingCmd: shown.command }));
      }
    }
    sendCardAction(value);
  }, [bridgeStatus, sendCardAction, viewedId, updateSlice, slicesRef]);

  // Create a NEW conversation without disturbing the current one. Uses the
  // management API (same path as the Sessions page) instead of sending /new
  // over the bridge: /new used to reset the CURRENT conversation — killing a
  // running turn and detaching its agent session — and rendered the "/new"
  // text inside the ongoing chat. Creating a session is purely additive:
  // the current conversation keeps running and stays resumable.
  const handleNewSession = useCallback(async () => {
    if (!projectName) return;
    setDrawerOpen(false);
    try {
      const res = await createSession(projectName, { session_key: newConvKey(projectName) });
      navigate(`/chat/${projectName}/${res.id}`);
    } catch (e) {
      // Creation failed — stay on the current conversation, untouched.
      console.error('create session failed:', e);
    }
  }, [projectName, navigate]);

  const canSend = bridgeStatus === 'connected';

  // Execution status of the currently viewed session (from the polled session
  // list). Distinct from isRunning, which tracks this client's live turn.
  const viewedStatus = sessions.find(s => s.session_key === sessionKey);
  const closeCmdPanel = useCallback(() => {
    if (viewedId) updateSlice(viewedId, s => (s.cmdResult ? { ...s, cmdResult: null } : s));
  }, [viewedId, updateSlice]);

  // Stable callbacks for the always-mounted SessionDrawer, so its React.memo
  // is not defeated by a fresh function identity on every render.
  const closeDrawer = useCallback(() => setDrawerOpen(false), []);
  const openRenameModal = useCallback((s: Session) => setRenameTarget(s), []);

  if (loading && !currentSession && sessions.length === 0) {
    return <div className="flex items-center justify-center h-64 text-gray-400 animate-pulse">Loading...</div>;
  }

  return (
    <div className="flex flex-col flex-1 min-h-0 animate-fade-in md:flex-row">
      {/* Left chat column; on md+ the open preview drawer becomes an inline
          right column and this column flexes to fill the remaining width.
          `relative` anchors the floating "jump to latest" button below. */}
      <div className="relative flex flex-col flex-1 min-w-0 min-h-0">
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

      {/* Messages — see the Transcript component above for the scrolling /
          memoization notes. */}
      <Transcript
        messages={messages}
        typing={typing}
        loading={loading}
        projectName={projectName || ''}
        onOpenFile={handleOpenFile}
        onCardAction={handleCardAction}
        scrollRef={scrollRef}
        onScroll={handleScroll}
        messagesEnd={messagesEnd}
      />

      {/* Input area — see ChatComposer: it owns the draft text so typing does
          not re-render this page (and therefore the transcript).
          `relative` anchors the floating "jump to latest" button, which is
          positioned against this wrapper's TOP edge — i.e. it always hovers
          just above the input bar, however tall the composer grows (multi-line
          draft, attachment chips). Anchoring it to the column's bottom instead
          would need the composer's height, which is not fixed.
          The button itself is icon-only (ArrowDown) and sits in the
          bottom-right, just above the input bar. */}
      <div className="relative border-t border-gray-200 dark:border-gray-800 pt-3 shrink-0">
        {!atBottom && messages.length > 0 && (
          <button
            type="button"
            onClick={jumpToBottom}
            className={cn(
              'absolute right-4 -translate-y-1/2 z-30',
              'flex items-center justify-center w-9 h-9 rounded-full',
              'bg-white/95 backdrop-blur-xl border border-gray-200/80 shadow-lg shadow-black/10',
              'text-gray-500 hover:text-accent hover:border-accent/40 active:scale-95',
              'dark:bg-[#1f2228]/95 dark:border-white/[0.12] dark:text-gray-300',
              'dark:hover:text-accent dark:shadow-black/50',
              'transition-colors duration-200 animate-fade-in',
            )}
            aria-label={t('chat.jumpToLatest')}
            title={t('chat.jumpToLatest')}
          >
            <ArrowDown size={16} />
          </button>
        )}
        <ChatComposer
          ref={composerRef}
          onSend={handleSend}
          pickedFiles={pickedFiles}
          onRemoveFile={removePickedFile}
          onAddFiles={addPickedFiles}
          canSend={canSend}
          bridgeCfgLoaded={!!bridgeCfg}
          bridgeStatus={bridgeStatus}
          isRunning={isRunning}
          interruptible={viewedStatus?.interruptible ?? false}
          onStop={handleStop}
          cmdOpen={cmdOpen}
          onCmdOpenChange={setCmdOpen}
          onCmdSelect={handleCmdSelect}
        />
      </div>

      </div>{/* /left chat column */}

      {/* Session drawer */}
      <SessionDrawer
        open={drawerOpen}
        onClose={closeDrawer}
        sessions={sessions}
        currentSessionId={currentSession?.id || ''}
        onSelect={switchToSession}
        onNewSession={handleNewSession}
        onRename={openRenameModal}
        onTogglePin={togglePinSession}
      />

      {/* Command result panel */}
      <CommandResultPanel
        result={cmdResult}
        onClose={closeCmdPanel}
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
            composerRef.current?.insert(relPath);
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
