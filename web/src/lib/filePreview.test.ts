import { describe, expect, it } from 'vitest';
import {
  PREVIEW_LIMITS, decidePreview, forcePreview, formatBytes,
  fileIsPreviewable, isMarkdown, isHtmlFile, isTextualFile, attachmentAccept, TEXT_EXTS,
} from './filePreview';

// The decision table is the contract both front ends follow. These tests pin
// every boundary, because "off by one byte" here means either a frozen tab or a
// file the user cannot open at all.

describe('decidePreview — markdown', () => {
  it('renders at the budget and degrades to text just past it', () => {
    expect(decidePreview('a.md', 'text/markdown', PREVIEW_LIMITS.markdownRender))
      .toEqual({ kind: 'markdown', mode: 'full' });
    expect(decidePreview('a.md', 'text/markdown', PREVIEW_LIMITS.markdownRender + 1))
      .toEqual({ kind: 'markdown', mode: 'windowed', degraded: true });
  });

  it('refuses past the text budget but can be forced open', () => {
    const plan = decidePreview('a.md', 'text/markdown', PREVIEW_LIMITS.textWindow + 1);
    expect(plan.mode).toBe('download-only');
    expect(plan.reason).toBe('too-large');
    expect(forcePreview(plan)).toEqual({ kind: 'markdown', mode: 'windowed', degraded: true });
  });
});

describe('decidePreview — plain text', () => {
  it('windows anything up to the text budget', () => {
    expect(decidePreview('app.log', 'text/plain', 1024)).toEqual({ kind: 'text', mode: 'windowed' });
    expect(decidePreview('app.log', 'text/plain', PREVIEW_LIMITS.textWindow))
      .toEqual({ kind: 'text', mode: 'windowed' });
  });

  it('refuses past the budget and can be forced open', () => {
    const plan = decidePreview('app.log', 'text/plain', PREVIEW_LIMITS.textWindow + 1);
    expect(plan).toMatchObject({ mode: 'download-only', reason: 'too-large' });
    expect(forcePreview(plan)).toEqual({ kind: 'text', mode: 'windowed' });
  });

  it('treats source files with an octet-stream type as text', () => {
    expect(decidePreview('main.go', 'application/octet-stream', 4000))
      .toEqual({ kind: 'text', mode: 'windowed' });
  });
});

describe('decidePreview — html', () => {
  it('keeps srcDoc for small documents, blob for larger ones, download past the cap', () => {
    expect(decidePreview('page.html', 'text/html', PREVIEW_LIMITS.htmlSrcDoc))
      .toEqual({ kind: 'html', mode: 'full' });
    expect(decidePreview('page.html', 'text/html', PREVIEW_LIMITS.htmlSrcDoc + 1))
      .toEqual({ kind: 'html', mode: 'object-url' });
    expect(decidePreview('page.html', 'text/html', PREVIEW_LIMITS.htmlInline + 1).mode)
      .toBe('download-only');
  });
});

describe('decidePreview — binary', () => {
  it('uses blob URLs within budget and refuses past it', () => {
    expect(decidePreview('shot.png', 'image/png', 1024)).toEqual({ kind: 'image', mode: 'object-url' });
    expect(decidePreview('shot.png', 'image/png', PREVIEW_LIMITS.imageInline + 1).mode).toBe('download-only');

    expect(decidePreview('doc.pdf', 'application/pdf', 1024)).toEqual({ kind: 'pdf', mode: 'object-url' });
    expect(decidePreview('doc.pdf', 'application/pdf', PREVIEW_LIMITS.pdfInline + 1).mode).toBe('download-only');

    expect(decidePreview('clip.mp4', 'video/mp4', PREVIEW_LIMITS.mediaInline))
      .toEqual({ kind: 'video', mode: 'object-url' });
    expect(decidePreview('clip.mp4', 'video/mp4', PREVIEW_LIMITS.mediaInline + 1).mode).toBe('download-only');

    expect(decidePreview('voice.mp3', 'audio/mpeg', 1024)).toEqual({ kind: 'audio', mode: 'object-url' });
  });

  it('refuses unknown types without offering a force path', () => {
    const plan = decidePreview('archive.zip', 'application/zip', 1024);
    expect(plan).toEqual({ kind: 'none', mode: 'download-only', reason: 'unsupported' });
    expect(forcePreview(plan)).toBe(plan);
  });

  it('leaves already-renderable plans untouched when forced', () => {
    const plan = decidePreview('a.txt', 'text/plain', 10);
    expect(forcePreview(plan)).toBe(plan);
  });
});

describe('file type helpers', () => {
  it('classifies by content type and by extension', () => {
    expect(isMarkdown('README.md', '')).toBe(true);
    expect(isMarkdown('README', 'text/markdown')).toBe(true);
    expect(isMarkdown('a.txt', 'text/plain')).toBe(false);

    expect(isHtmlFile('index.html', '')).toBe(true);
    expect(isHtmlFile('page', 'text/html')).toBe(true);

    expect(isTextualFile('notes.md', '')).toBe(true);
    expect(isTextualFile('blob', 'application/json')).toBe(true);
    expect(isTextualFile('blob', 'application/zip')).toBe(false);
  });

  it('keeps every previewable text extension attachable', () => {
    const accept = attachmentAccept();
    for (const ext of TEXT_EXTS) {
      expect(fileIsPreviewable(`sample.${ext}`, '')).toBe(true);
      expect(accept).toContain(`.${ext},`);
    }
    expect(accept).toContain('.tar.gz');
  });
});

describe('formatBytes', () => {
  it('renders human sizes', () => {
    expect(formatBytes(0)).toBe('0 B');
    expect(formatBytes(999)).toBe('999 B');
    expect(formatBytes(1024)).toBe('1 KB');
    expect(formatBytes(512 * 1024)).toBe('512 KB');
    expect(formatBytes(2 * 1024 * 1024)).toBe('2 MB');
    expect(formatBytes(16 * 1024 * 1024)).toBe('16 MB');
  });
});
