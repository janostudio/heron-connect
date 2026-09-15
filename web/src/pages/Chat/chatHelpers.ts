import type { Session } from '@/api/sessions';

// Pure helper functions extracted from ChatView so they can be unit-tested
// without rendering anything. All of these are used by the chat page's render
// and preview paths.

/**
 * Fold the fields that actually drive the session list UI (status badges,
 * ordering, titles) into one string, so the 5s poll can skip a no-op state
 * update. sortSessions always builds a fresh array, so without this every poll
 * re-rendered the whole page forever.
 *
 * Field order matters: a change in any listed field — or in the list's order —
 * must change the signature. If a field that drives the UI is missing here,
 * the UI silently stops updating, which is why this is unit-tested.
 */
export function sessionsSignature(list: Session[]): string {
  return list
    .map(s => `${s.id}|${s.session_key}|${s.name}|${s.running ? 1 : 0}|${s.waiting_permission ? 1 : 0}|${s.live ? 1 : 0}|${s.pinned ? 1 : 0}|${s.updated_at}|${s.history_count}`)
    .join('\n');
}

const TEXT_EXTS = new Set([
  'md', 'markdown', 'txt', 'log', 'json', 'yaml', 'yml', 'xml', 'svg', 'csv',
  'ts', 'tsx', 'js', 'jsx', 'go', 'py', 'rs', 'c', 'h', 'cpp', 'hpp', 'java',
  'rb', 'php', 'sh', 'sql', 'toml', 'ini', 'conf', 'cfg', 'env', 'gitignore',
]);

/**
 * Whether a file can be shown inline in the web UI rather than only offered
 * for download. Checks the runtime Content-Type first, then falls back to a
 * curated extension list (servers often send application/octet-stream for
 * source files).
 */
export function fileIsPreviewable(fileName: string, contentType: string): boolean {
  const ext = (fileName.split('.').pop() || '').toLowerCase();
  const ct = contentType.toLowerCase();
  if (ct.startsWith('image/') || ct.startsWith('audio/') || ct.startsWith('video/') || ct === 'application/pdf') {
    return true;
  }
  if (ct.startsWith('text/') || ct.includes('json') || ct.includes('xml') || ct.includes('svg') || ct.includes('yaml') || ct.includes('javascript') || ct.includes('typescript')) {
    return true;
  }
  return TEXT_EXTS.has(ext);
}

/**
 * Whether the file should be rendered as markdown in the preview rather than as
 * raw text. Matches on both the file extension and the runtime Content-Type, so
 * a .md served with a non-standard MIME still works.
 */
export function isMarkdown(fileName: string, contentType: string): boolean {
  const ext = (fileName.split('.').pop() || '').toLowerCase();
  return ext === 'md' || ext === 'markdown' || contentType.toLowerCase() === 'text/markdown';
}

/**
 * Whether the file should offer the two HTML view modes: the rendered effect
 * (sandboxed iframe) and the raw source. Matches on both the file extension and
 * the runtime Content-Type like isMarkdown does.
 */
export function isHtmlFile(fileName: string, contentType: string): boolean {
  const ext = (fileName.split('.').pop() || '').toLowerCase();
  return ext === 'html' || ext === 'htm' || contentType.toLowerCase() === 'text/html';
}

/**
 * Parse a card list_item label of the form "**command** description" (or a
 * plain "command description") into its two parts. Shared by the in-stream card
 * renderer (markdownBlocks) and the command result panel, which previously
 * carried identical private copies.
 */
export function parseListItemText(text: string): { cmd: string; desc: string } {
  const m = text.match(/^\*\*(.+?)\*\*\s*(.*)/);
  if (m) return { cmd: m[1], desc: m[2] };
  const sp = text.indexOf(' ');
  if (sp > 0) return { cmd: text.slice(0, sp), desc: text.slice(sp + 1) };
  return { cmd: text, desc: '' };
}

// ── Slash command classification ─────────────────────────────

// Commands whose output belongs in the message stream; the rest render into the
// dedicated command result panel.
export const CHAT_COMMANDS = new Set(['/new', '/stop', '/switch', '/delete-mode', '/upgrade']);

/** A slash command that changes chat state rather than showing a result panel. */
export function isChatCommand(cmd: string): boolean {
  return CHAT_COMMANDS.has(cmd);
}

/**
 * Classify a user's input line. Returns the command token (if the input starts
 * a known slash command) and whether its reply should go to the result panel.
 */
export function classifyInput(content: string, knownCommands: Set<string>): {
  token: string;
  isKnown: boolean;
  goesToPanel: boolean;
} {
  const token = content.split(' ')[0];
  const isKnown = knownCommands.has(token);
  return { token, isKnown, goesToPanel: isKnown && !CHAT_COMMANDS.has(token) };
}
