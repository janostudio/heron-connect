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

// File-type classification lives in @/lib/filePreview so the standalone share
// viewer can use the same rules without importing the chat page. Re-exported
// here to keep the existing import sites unchanged.
export {
  TEXT_EXTS, fileIsPreviewable, isTextualContentType, isTextualFile,
  isMarkdown, isHtmlFile, attachmentAccept,
} from '@/lib/filePreview';

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

// ── File link → project-relative path ───────────────────────

const FILES_PREFIX = '/api/v1/files/';

/** Split a project-relative path into its parent dir and its file name. */
export function splitFileRel(rel: string): { dir: string; name: string } {
  const i = rel.lastIndexOf('/');
  return i === -1 ? { dir: '', name: rel } : { dir: rel.slice(0, i), name: rel.slice(i + 1) };
}

// decodeURIComponent throws on a malformed sequence; a raw segment is still a
// usable path, so failures fall back to the undecoded text.
function safeDecode(seg: string): string {
  try {
    return decodeURIComponent(seg);
  } catch {
    return seg;
  }
}

/**
 * localStorage key holding the file browser's remembered position (dir +
 * selected file). Scoped per conversation: two conversations in the same
 * project browse independently, so opening a report in one does not move the
 * other to it.
 *
 * Deliberately NOT falling back to the project-scoped key that older versions
 * wrote: inheriting it would make every conversation start on whichever file
 * was last open — the very "one position for all conversations" behaviour this
 * replaces. A conversation with no remembered position starts at the project
 * root.
 */
export function fileBrowserKey(projectName: string, sessionId: string): string {
  return `cc_file_browser:${projectName}:${sessionId}`;
}

/**
 * Extract the project-relative path from a `/api/v1/files/<project>/<rel>` URL
 * — the link form the engine emits for local file references. Returns null when
 * the URL is not a file link for `projectName`: only then does the caller fall
 * back to the standalone preview, which has no directory context to show.
 */
export function relFromFilesHref(href: string, projectName: string): string | null {
  if (!href.startsWith(FILES_PREFIX)) return null;
  const parts = href.slice(FILES_PREFIX.length).split('/').filter(p => p !== '');
  // project name + at least one path segment
  if (parts.length < 2) return null;
  if (safeDecode(parts[0]) !== projectName) return null;
  return parts.slice(1).map(safeDecode).join('/');
}
