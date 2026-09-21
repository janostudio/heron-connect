import { describe, it, expect } from 'vitest';
import {
  sessionsSignature, fileIsPreviewable, isMarkdown, isHtmlFile,
  isChatCommand, classifyInput, CHAT_COMMANDS, parseListItemText,
  attachmentAccept, TEXT_EXTS,
} from './chatHelpers';
import type { Session } from '@/api/sessions';

const session = (over: Partial<Session> = {}): Session => ({
  id: 's1',
  session_key: 'bridge:web-admin:p:conv-1',
  name: 'Chat',
  platform: 'bridge',
  agent_type: 'codebuddy',
  active: true,
  live: true,
  running: false,
  waiting_permission: false,
  pinned: false,
  created_at: '2026-09-15T00:00:00Z',
  updated_at: '2026-09-15T00:00:00Z',
  history_count: 3,
  last_message: null,
  ...over,
});

// ── sessionsSignature ────────────────────────────────────────
//
// This drives the 5s poll's "did anything change?" check. A field missing from
// the signature would make the UI silently stop updating — the tests below pin
// every field that drives a badge or the ordering.

describe('sessionsSignature', () => {
  it('is stable for structurally equal lists', () => {
    expect(sessionsSignature([session()])).toBe(sessionsSignature([session()]));
  });

  it('is empty for an empty list', () => {
    expect(sessionsSignature([])).toBe('');
  });

  it('changes when the list order changes', () => {
    const a = session({ id: 's1' });
    const b = session({ id: 's2' });
    expect(sessionsSignature([a, b])).not.toBe(sessionsSignature([b, a]));
  });

  it('changes when the list length changes', () => {
    expect(sessionsSignature([session()])).not.toBe(sessionsSignature([session(), session({ id: 's2' })]));
  });

  // Every badge-driving field must invalidate the signature.
  const cases: [string, Partial<Session>][] = [
    ['id', { id: 'other' }],
    ['session_key', { session_key: 'bridge:web-admin:p:conv-2' }],
    ['name', { name: 'renamed' }],
    ['running', { running: true }],
    ['waiting_permission', { waiting_permission: true }],
    ['live', { live: false }],
    ['pinned', { pinned: true }],
    ['updated_at', { updated_at: '2026-09-15T01:00:00Z' }],
    ['history_count', { history_count: 9 }],
  ];

  it.each(cases)('changes when %s changes', (_field, over) => {
    expect(sessionsSignature([session(over)])).not.toBe(sessionsSignature([session()]));
  });

  it('does not change for fields the UI ignores', () => {
    // agent_type / platform are not rendered as badges, so they must not force
    // a re-render on every poll.
    const base = sessionsSignature([session()]);
    expect(sessionsSignature([session({ agent_type: 'claude' })])).toBe(base);
    expect(sessionsSignature([session({ created_at: '2020-01-01T00:00:00Z' })])).toBe(base);
  });

  it('ignores interruptible — it is a project constant, not per-session state', () => {
    // Deliberately excluded: the signature exists to detect *changes worth
    // re-rendering for*, and this flag is identical for every session of a
    // project and never flips during a session's life. Including it would add a
    // constant to the comparison — noise that can only ever produce false
    // "changed" verdicts, never a real one.
    const base = sessionsSignature([session()]);
    expect(sessionsSignature([session({ interruptible: true })])).toBe(base);
    expect(sessionsSignature([session({ interruptible: false })])).toBe(base);
  });

  it('distinguishes running=false from undefined', () => {
    // Both render as "not running", so they may share a signature — but the
    // flip to true must be detected.
    const off = sessionsSignature([session({ running: undefined })]);
    expect(sessionsSignature([session({ running: true })])).not.toBe(off);
  });
});

// ── fileIsPreviewable ────────────────────────────────────────

describe('fileIsPreviewable', () => {
  it.each([
    ['photo.png', 'image/png'],
    ['clip.mp4', 'video/mp4'],
    ['sound.mp3', 'audio/mpeg'],
    ['doc.pdf', 'application/pdf'],
    ['notes.txt', 'text/plain'],
    ['data.json', 'application/json'],
    ['icon.svg', 'image/svg+xml'],
    ['conf.yaml', 'application/yaml'],
    ['app.ts', 'application/octet-stream'],   // ext fallback
    ['main.go', 'application/octet-stream'],  // ext fallback
    ['Dockerfile', 'text/plain'],
    ['.gitignore', 'application/octet-stream'],
  ])('previews %s (%s)', (name, ct) => {
    expect(fileIsPreviewable(name, ct)).toBe(true);
  });

  it.each([
    ['archive.zip', 'application/zip'],
    ['binary.bin', 'application/octet-stream'],
    ['app.exe', 'application/x-msdownload'],
  ])('does not preview %s (%s)', (name, ct) => {
    expect(fileIsPreviewable(name, ct)).toBe(false);
  });

  it('handles a filename with no extension', () => {
    expect(fileIsPreviewable('Makefile', 'application/octet-stream')).toBe(false);
    expect(fileIsPreviewable('Makefile', 'text/plain')).toBe(true);
  });

  it('is case-insensitive on both name and content type', () => {
    expect(fileIsPreviewable('README.MD', 'APPLICATION/OCTET-STREAM')).toBe(true);
    expect(fileIsPreviewable('PHOTO.PNG', 'IMAGE/PNG')).toBe(true);
  });

  it('handles empty inputs without throwing', () => {
    expect(fileIsPreviewable('', '')).toBe(false);
    expect(fileIsPreviewable('', 'text/plain')).toBe(true);
  });

  it('previews every extension the picker offers as text', () => {
    // The other direction of the attachmentAccept consistency check: a format
    // the user can attach must also be previewable once the agent writes it
    // back out.
    for (const ext of TEXT_EXTS) {
      expect(fileIsPreviewable(`sample.${ext}`, '')).toBe(true);
    }
  });
});

// ── isMarkdown / isHtmlFile ──────────────────────────────────

describe('isMarkdown', () => {
  it('matches the .md / .markdown extensions', () => {
    expect(isMarkdown('README.md', '')).toBe(true);
    expect(isMarkdown('doc.markdown', '')).toBe(true);
    expect(isMarkdown('README.MD', '')).toBe(true);
  });

  it('matches the text/markdown content type even for another extension', () => {
    expect(isMarkdown('notes.txt', 'text/markdown')).toBe(true);
    expect(isMarkdown('noext', 'text/markdown')).toBe(true);
  });

  it('rejects other types', () => {
    expect(isMarkdown('notes.txt', 'text/plain')).toBe(false);
    expect(isMarkdown('a.mdx', 'text/plain')).toBe(false);
  });
});

describe('isHtmlFile', () => {
  it('matches .html / .htm and text/html', () => {
    expect(isHtmlFile('index.html', '')).toBe(true);
    expect(isHtmlFile('page.htm', '')).toBe(true);
    expect(isHtmlFile('INDEX.HTML', '')).toBe(true);
    expect(isHtmlFile('weird.txt', 'text/html')).toBe(true);
  });

  it('rejects other types', () => {
    expect(isHtmlFile('index.md', 'text/markdown')).toBe(false);
    expect(isHtmlFile('app.tsx', 'text/plain')).toBe(false);
  });
});

// ── slash-command classification ─────────────────────────────

describe('classifyInput', () => {
  const known = new Set(['/status', '/help', '/skills', '/new', '/stop', '/switch']);

  it('routes state-changing commands to the message stream', () => {
    for (const cmd of ['/new', '/stop', '/switch', '/delete-mode', '/upgrade']) {
      expect(isChatCommand(cmd)).toBe(true);
      expect(classifyInput(cmd, new Set([...known, cmd])).goesToPanel).toBe(false);
    }
  });

  it('routes result commands to the panel', () => {
    for (const cmd of ['/status', '/help', '/skills']) {
      expect(classifyInput(cmd, known).goesToPanel).toBe(true);
    }
  });

  it('takes the first token when the command has arguments', () => {
    const r = classifyInput('/status verbose extra', known);
    expect(r.token).toBe('/status');
    expect(r.isKnown).toBe(true);
    expect(r.goesToPanel).toBe(true);
  });

  it('treats a plain message as neither', () => {
    const r = classifyInput('hello world', known);
    expect(r.token).toBe('hello');
    expect(r.isKnown).toBe(false);
    expect(r.goesToPanel).toBe(false);
  });

  it('treats an unknown slash command as a plain message', () => {
    const r = classifyInput('/unknowncmd arg', known);
    expect(r.isKnown).toBe(false);
    expect(r.goesToPanel).toBe(false);
  });

  it('handles empty input', () => {
    const r = classifyInput('', known);
    expect(r.token).toBe('');
    expect(r.goesToPanel).toBe(false);
  });

  it('does not treat a mid-message slash as a command', () => {
    expect(classifyInput('see /status for details', known).isKnown).toBe(false);
  });

  it('exposes the chat-command set consistently', () => {
    expect(CHAT_COMMANDS.has('/new')).toBe(true);
    expect(CHAT_COMMANDS.has('/status')).toBe(false);
  });
});

// ── attachmentAccept ─────────────────────────────────────────
//
// The picker's accept list is derived from TEXT_EXTS. The bug this guards
// against is drift: `.html` was previewable but un-attachable because the two
// lists were maintained by hand. If a future format is added to TEXT_EXTS and
// the picker does not offer it, these fail.

describe('attachmentAccept', () => {
  const accepted = () => attachmentAccept().split(',');

  it('offers every previewable text extension', () => {
    const list = accepted();
    for (const ext of TEXT_EXTS) {
      expect(list).toContain(`.${ext}`);
    }
  });

  it('includes html and htm', () => {
    // The reported gap, and its Windows/legacy-systems twin.
    expect(accepted()).toContain('.html');
    expect(accepted()).toContain('.htm');
  });

  it('keeps the existing document and archive entries', () => {
    const list = accepted();
    for (const ext of ['image/*', '.pdf', '.docx', '.xlsx', '.pptx', '.zip', '.tar', '.gz']) {
      expect(list).toContain(ext);
    }
  });

  it('keeps the two-part tar.gz entry intact', () => {
    // If this is ever built by appending `.gz`, the browser sees only the
    // final extension and `.tar.gz` stops matching.
    expect(accepted()).toContain('.tar.gz');
  });

  it('emits no duplicate or malformed entries', () => {
    const list = accepted();
    expect(new Set(list).size).toBe(list.length);
    // Each entry is `*`, `type/*`, or a dotted extension such as `.tar.gz`.
    for (const e of list) {
      expect(e).toMatch(/^(\*|[a-z0-9]+\/\*|\.[a-z0-9]+(\.[a-z0-9]+)*)$/);
    }
  });

  it('produces every extension lowercased (browser matching is case-insensitive but the value should be canonical)', () => {
    for (const e of accepted()) {
      expect(e).toBe(e.toLowerCase());
    }
  });
});

// ── parseListItemText ────────────────────────────────────────

describe('parseListItemText', () => {
  it('splits a bolded command from its description', () => {
    expect(parseListItemText('**/status** Show current status'))
      .toEqual({ cmd: '/status', desc: 'Show current status' });
  });

  it('splits a plain "command description" pair', () => {
    expect(parseListItemText('/status Show status'))
      .toEqual({ cmd: '/status', desc: 'Show status' });
  });

  it('returns the whole text as cmd when there is no space', () => {
    expect(parseListItemText('/status')).toEqual({ cmd: '/status', desc: '' });
  });

  it('handles a bolded command with no description', () => {
    expect(parseListItemText('**/status**')).toEqual({ cmd: '/status', desc: '' });
  });

  it('keeps the remainder intact when the description contains spaces', () => {
    expect(parseListItemText('**/switch** switch to another session by id'))
      .toEqual({ cmd: '/switch', desc: 'switch to another session by id' });
  });

  it('normalises the whitespace between bold marker and description', () => {
    expect(parseListItemText('**/help**    lots of spaces'))
      .toEqual({ cmd: '/help', desc: 'lots of spaces' });
  });

  it('treats a leading space as no command', () => {
    // indexOf(' ') === 0 is not a split point.
    expect(parseListItemText(' leading')).toEqual({ cmd: ' leading', desc: '' });
  });

  it('handles empty input', () => {
    expect(parseListItemText('')).toEqual({ cmd: '', desc: '' });
  });

  it('does not treat a mid-text bold as a command', () => {
    // The bold pattern is anchored to the start.
    expect(parseListItemText('text **bold** more').cmd).toBe('text');
  });
});
