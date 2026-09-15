import { useEffect, useState, useRef, useCallback, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { useParams, useNavigate, useLocation } from 'react-router-dom';
import {
  ArrowLeft, Send, Bot, Circle, WifiOff,
  FileText, Loader2, Download, FileDown,
  Slash, ChevronDown, Square, Clock, X, Folder, FolderOpen, ChevronLeft, ChevronRight, ArrowUp,
  Pin, PinOff, Pencil, Paperclip, RefreshCw,
} from 'lucide-react';
import { Button } from '@/components/ui';
import { listSessions, getSession, createSession, sessionTitle, updateSession, sortSessions, type Session, type SessionDetail } from '@/api/sessions';
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
import MessageRow from './MessageRow';
import { RenderMarkdown } from './markdownBlocks';
import { useChatSessions, historyToMessages } from './useChatSessions';
import type { ChatMsg, PickItem } from './chatMessage';
import { SequenceGuard } from '@/lib/sequenceGuard';
import {
  sessionsSignature, fileIsPreviewable, isMarkdown, isHtmlFile,
  CHAT_COMMANDS, classifyInput,
} from './chatHelpers';
import { cn, loadLS, saveLS } from '@/lib/utils';

// `chatCommands` produce output in the message stream (they change state);
// the rest are handled by their own result panel.
const chatCommands = CHAT_COMMANDS;
const knownCommands = new Set(slashCommands.map(c => c.cmd));

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

// ── Main component ───────────────────────────────────────────

export default function ChatView() {
  const { t } = useTranslation();
  const { name: projectName, id: routeSessionId } = useParams<{ name: string; id?: string }>();
  const navigate = useNavigate();
  const location = useLocation();

  // Session state
  const [sessions, setSessions] = useState<Session[]>([]);
  const [currentSession, setCurrentSession] = useState<SessionDetail | null>(null);
  const [input, setInput] = useState('');
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
  const cmdBtnRef = useRef<HTMLButtonElement>(null);

  // Per-conversation live state. Each Web conversation owns a distinct
  // session_key, and the bridge broadcasts every frame to every client — so
  // the page receives live output for all conversations. Keeping a slice per
  // conversation (instead of one shared transcript) is what lets a background
  // conversation keep streaming while you read another one.
  const store = useChatSessions();
  const { slices, slicesRef, setSlices, ensureSlice, updateSlice, apply, seedHistory, settleAll } = store;

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
      } else {
        setCurrentSession(null);
      }
    } finally {
      if (seq.isCurrent(ticket)) setLoading(false);
    }
  }, [projectName, routeSessionId, seedHistory]);

  useEffect(() => { fetchData(); }, [fetchData]);

  // Periodically refresh the session list so execution-status badges (running
  // / waiting permission) stay current while other sessions run in parallel.
  // Only refreshes the list — never touches the open conversation.
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
    } catch { /* transient — keep last known list */ }
  }, [projectName]);

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
  // The frame handler must stay identity-stable (useBridgeSocket captures it
  // once as onMessage), so read the current view id through a ref rather than
  // closing over it.
  const viewedIdRef = useRef(viewedId);
  viewedIdRef.current = viewedId;

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
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const stickToBottomRef = useRef(true);
  const scrollRafRef = useRef<number | null>(null);

  const handleScroll = useCallback(() => {
    const el = scrollRef.current;
    if (!el) return;
    const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
    stickToBottomRef.current = distance < 80;
  }, []);

  // Scroll signature: total message count plus the length of the last
  // message. Depend on this instead of the `messages` array identity so
  // re-renders that don't add content (e.g. a settle pass) don't scroll.
  const lastMsg = messages[messages.length - 1];
  const scrollSignature = `${messages.length}:${lastMsg?.content.length ?? 0}:${lastMsg?.streaming ? 1 : 0}`;

  useEffect(() => {
    if (!stickToBottomRef.current) return;
    if (scrollRafRef.current != null) return;
    scrollRafRef.current = requestAnimationFrame(() => {
      scrollRafRef.current = null;
      messagesEnd.current?.scrollIntoView({ behavior: 'auto', block: 'end' });
    });
  }, [scrollSignature, typing]);

  // Cancel a pending scroll frame on unmount.
  useEffect(() => () => {
    if (scrollRafRef.current != null) cancelAnimationFrame(scrollRafRef.current);
  }, []);

  // If the bridge connection drops mid-turn, no terminal event will arrive for
  // ANY conversation — settle them all so no red stop button gets stuck.
  useEffect(() => {
    if (bridgeStatus !== 'connected') {
      settleAll();
    }
  }, [bridgeStatus, settleAll]);

  // True while the VIEWED conversation is actively producing a reply (typing
  // indicator or a streaming message in flight). Scoped to the conversation on
  // screen: a background conversation running in parallel must not light up
  // this one's stop button.
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

    const { token: cmdToken, goesToPanel } = classifyInput(content, knownCommands);
    if (goesToPanel) {
      updateSlice(targetId, s => ({ ...s, pendingCmd: cmdToken }));
    } else {
      updateSlice(targetId, s => ({
        ...s,
        messages: [...s.messages, {
          id: `user-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
          role: 'user' as const,
          content,
          localMedia: pickedFiles.length > 0 ? pickedFiles : undefined,
        }],
      }));
    }
    bridgeSend(content, media, currentSession?.id);
    setPickedFiles([]);
  }, [input, pickedFiles, bridgeStatus, bridgeSend, isRunning, stripDataUrlPrefix, currentSession?.id, ensureViewedId, ensureSlice, updateSlice]);

  // Paste handler: turn clipboard images into queued attachments (sent with
  // the message, not immediately). Plain text pastes fall through to the
  // textarea's default behaviour.
  const handlePaste = (e: React.ClipboardEvent<HTMLTextAreaElement>) => {
    const items = e.clipboardData?.items;
    if (!items) return;
    const imageFiles: File[] = [];
    for (let i = 0; i < items.length; i++) {
      const item = items[i];
      if (item.kind === 'file' && item.type.startsWith('image/')) {
        const f = item.getAsFile();
        if (f) imageFiles.push(f);
      }
    }
    if (imageFiles.length > 0) {
      e.preventDefault();
      addPickedFiles(imageFiles);
    }
  };

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

  // Stable file-open handler. Must NOT be an inline arrow at the call site:
  // a new function identity on every render would defeat the React.memo on
  // MessageRow / RenderMarkdown in the transcript.
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
        }],
      }));
    } else {
      updateSlice(targetId, s => ({ ...s, pendingCmd: cmd.cmd }));
    }
    bridgeSend(cmd.cmd);
  }, [bridgeStatus, bridgeSend, ensureViewedId, ensureSlice, updateSlice]);

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
      <div
        ref={scrollRef}
        onScroll={handleScroll}
        className="flex-1 overflow-y-auto overflow-x-hidden py-4 md:py-6 px-2 space-y-5"
      >
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
            projectName={projectName || ''}
            onOpenFile={handleOpenFile}
            onCardAction={handleCardAction}
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
                onPaste={handlePaste}
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
