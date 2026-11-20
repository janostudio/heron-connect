// File-type classification and preview budgeting.
//
// The admin UI (pages/Chat) and the standalone share viewer (share/main) both
// have to answer the same question before they download anything: what can this
// file be shown as, and how much of it is safe to hand the browser? Keeping the
// answer in one framework-free module is what keeps the two front ends from
// drifting, and makes it testable without a DOM.
//
// The budgets come from measurement, not taste: rendering markdown runs the
// unified pipeline and turns roughly 150 DOM nodes per KB into a one-shot
// commit, so 256 KiB already costs ~39k nodes and several hundred milliseconds.
// Plain text is cheap to split and cheap to virtualize, so its budget is far
// larger — the cost there is only bytes in memory, not layout.

export const TEXT_EXTS: readonly string[] = [
  'md', 'markdown', 'txt', 'log', 'json', 'yaml', 'yml', 'xml', 'svg', 'csv',
  'html', 'htm',
  'ts', 'tsx', 'js', 'jsx', 'go', 'py', 'rs', 'c', 'h', 'cpp', 'hpp', 'java',
  'rb', 'php', 'sh', 'sql', 'toml', 'ini', 'conf', 'cfg', 'env', 'gitignore',
];

// Document types the composer's picker offers that are NOT text — they round
// out ACCEPT below. Kept separate from TEXT_EXTS because they are neither
// previewable inline nor language-specific source.
const DOC_EXTS: readonly string[] = ['pdf', 'doc', 'docx', 'xls', 'xlsx', 'ppt', 'pptx'];

// Archive formats. `.tar.gz` needs the explicit two-part entry: the browser
// matches on the final extension, and the picker's own accept filter parses
// this value too — which is why it is built from `['tar.gz', ...]` rather than
// appended inline.
const ARCHIVE_EXTS: readonly string[] = ['zip', 'tar', 'gz'];

/** Size budgets, in bytes. One place, so both front ends agree. */
export const PREVIEW_LIMITS = {
  /** Largest file still rendered through the markdown pipeline. */
  markdownRender: 256 * 1024,
  /** Largest text file the windowed preview will open at all. */
  textWindow: 16 * 1024 * 1024,
  /** HTML above this renders from a blob URL and gets no source-view toggle. */
  htmlSrcDoc: 256 * 1024,
  /** Largest HTML document handed to an iframe. */
  htmlInline: 2 * 1024 * 1024,
  /** Largest image decoded in the browser. */
  imageInline: 64 * 1024 * 1024,
  /** Largest PDF handed to the browser's viewer. */
  pdfInline: 64 * 1024 * 1024,
  /** Largest audio/video fed to a blob URL. */
  mediaInline: 512 * 1024 * 1024,
  /** First text slice. */
  firstChunk: 512 * 1024,
  /** Each subsequent text slice. */
  nextChunk: 1024 * 1024,
  /** A single line longer than this is displayed cut, not laid out. One
   *  unterminated multi-megabyte line would otherwise defeat virtualization. */
  maxLineChars: 2000,
  /** Directory entries per page. Mirrors management.dirPageDefault. */
  dirPage: 500,
} as const;

export function extOf(fileName: string): string {
  return (fileName.split('.').pop() || '').toLowerCase();
}

/**
 * Whether a file can be shown inline in the web UI rather than only offered
 * for download. Checks the runtime Content-Type first, then falls back to a
 * curated extension list (servers often send application/octet-stream for
 * source files).
 */
export function fileIsPreviewable(fileName: string, contentType: string): boolean {
  const ct = (contentType || '').toLowerCase();
  if (ct.startsWith('image/') || ct.startsWith('audio/') || ct.startsWith('video/') || ct === 'application/pdf') {
    return true;
  }
  if (isTextualContentType(ct)) return true;
  return TEXT_EXTS.includes(extOf(fileName));
}

/** Whether the bytes are text as far as the preview is concerned. */
export function isTextualContentType(contentType: string): boolean {
  const ct = (contentType || '').toLowerCase();
  if (ct.startsWith('text/')) return true;
  return /(json|xml|yaml|javascript|typescript|svg)/.test(ct);
}

/** Whether the file is text, judged by type or by extension. */
export function isTextualFile(fileName: string, contentType: string): boolean {
  return isTextualContentType(contentType) || TEXT_EXTS.includes(extOf(fileName));
}

/**
 * Whether the file should be rendered as markdown in the preview rather than as
 * raw text. Matches on both the file extension and the runtime Content-Type, so
 * a .md served with a non-standard MIME still works.
 */
export function isMarkdown(fileName: string, contentType: string): boolean {
  return extOf(fileName) === 'md' || extOf(fileName) === 'markdown'
    || (contentType || '').toLowerCase() === 'text/markdown';
}

/**
 * Whether the file should offer the two HTML view modes: the rendered effect
 * (sandboxed iframe) and the raw source. Matches on both the file extension and
 * the runtime Content-Type like isMarkdown does.
 */
export function isHtmlFile(fileName: string, contentType: string): boolean {
  return extOf(fileName) === 'html' || extOf(fileName) === 'htm'
    || (contentType || '').toLowerCase() === 'text/html';
}

/**
 * The `accept` attribute for the composer's file picker. Derived from
 * TEXT_EXTS so every previewable text format is attachable — the two lists
 * used to be maintained by hand and had drifted (`.html` could be previewed
 * but not picked, which is what prompted this).
 */
export function attachmentAccept(): string {
  return [
    'image/*',
    ...DOC_EXTS.map(e => `.${e}`),
    ...TEXT_EXTS.map(e => `.${e}`),
    ...ARCHIVE_EXTS.map(e => `.${e}`),
    '.tar.gz',
  ].join(',');
}

// ── Preview planning ─────────────────────────────────────────

export type PreviewKind =
  | 'markdown'
  | 'text'
  | 'html'
  | 'image'
  | 'pdf'
  | 'audio'
  | 'video'
  | 'none';

export type PreviewMode =
  /** One full GET, then render. */
  | 'full'
  /** Slice-by-slice, rendered through the virtualized text view. */
  | 'windowed'
  /** Full GET into a blob URL. */
  | 'object-url'
  /** Refuse to fetch: the size is beyond what the browser can show. */
  | 'download-only';

export interface PreviewPlan {
  kind: PreviewKind;
  mode: PreviewMode;
  /** Set when mode is download-only, so the UI can explain itself. */
  reason?: 'too-large' | 'unsupported';
  /** Markdown past its render budget, shown as plain text instead. */
  degraded?: boolean;
}

const tooLarge = (kind: PreviewKind): PreviewPlan => ({ kind, mode: 'download-only', reason: 'too-large' });

/**
 * Decide how to show a file of a known size. The caller must know the size
 * before calling (a HEAD request), because the whole point is to avoid
 * downloading something that cannot be displayed.
 */
export function decidePreview(fileName: string, contentType: string, size: number): PreviewPlan {
  const ct = (contentType || '').toLowerCase();
  const bytes = Number.isFinite(size) && size > 0 ? size : 0;

  if (ct.startsWith('image/')) {
    return bytes > PREVIEW_LIMITS.imageInline ? tooLarge('image') : { kind: 'image', mode: 'object-url' };
  }
  if (ct === 'application/pdf') {
    return bytes > PREVIEW_LIMITS.pdfInline ? tooLarge('pdf') : { kind: 'pdf', mode: 'object-url' };
  }
  if (ct.startsWith('audio/')) {
    return bytes > PREVIEW_LIMITS.mediaInline ? tooLarge('audio') : { kind: 'audio', mode: 'object-url' };
  }
  if (ct.startsWith('video/')) {
    return bytes > PREVIEW_LIMITS.mediaInline ? tooLarge('video') : { kind: 'video', mode: 'object-url' };
  }

  if (isHtmlFile(fileName, ct)) {
    if (bytes > PREVIEW_LIMITS.htmlInline) return tooLarge('html');
    // Small HTML keeps the source toggle (srcDoc); larger documents are handed
    // to the frame as a blob so the string is not copied twice.
    return { kind: 'html', mode: bytes > PREVIEW_LIMITS.htmlSrcDoc ? 'object-url' : 'full' };
  }

  if (isMarkdown(fileName, ct)) {
    if (bytes <= PREVIEW_LIMITS.markdownRender) return { kind: 'markdown', mode: 'full' };
    // Beyond the render budget the file is still readable — as text.
    if (bytes <= PREVIEW_LIMITS.textWindow) {
      return { kind: 'markdown', mode: 'windowed', degraded: true };
    }
    return tooLarge('markdown');
  }

  if (isTextualFile(fileName, ct)) {
    if (bytes <= PREVIEW_LIMITS.textWindow) return { kind: 'text', mode: 'windowed' };
    return tooLarge('text');
  }

  if (fileIsPreviewable(fileName, ct)) return { kind: 'text', mode: 'windowed' };
  return { kind: 'none', mode: 'download-only', reason: 'unsupported' };
}

/**
 * The "preview it anyway" path behind the too-large notice.
 *
 * Text is always safe to open — the virtualized view only ever lays out a
 * screenful — so the escape hatch exists for exactly that case. Binary stays
 * refused: past its budget the browser would have to decode the whole thing.
 */
export function forcePreview(plan: PreviewPlan): PreviewPlan {
  if (plan.mode !== 'download-only' || plan.reason !== 'too-large') return plan;
  switch (plan.kind) {
    case 'markdown':
      return { kind: 'markdown', mode: 'windowed', degraded: true };
    case 'text':
      return { kind: 'text', mode: 'windowed' };
    default:
      return plan;
  }
}

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB'];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  const text = Number.isInteger(value) ? String(value) : value.toFixed(1);
  return `${text} ${units[unit]}`;
}
