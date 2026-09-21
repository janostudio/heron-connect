/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { fileIsPreviewable, isMarkdown, isHtmlFile } from '@/pages/Chat/chatHelpers';

// The share viewer decides how to present a file from its name + content type.
// These tests pin the branch selection and, more importantly, that markdown is
// actually rendered rather than echoed — the original bug was a shared .md
// arriving at the browser as literal "# heading" source.

describe('share viewer branch selection', () => {
  it('treats markdown as markdown so it can be rendered', () => {
    expect(isMarkdown('report.md', 'text/markdown; charset=utf-8')).toBe(true);
    expect(isMarkdown('report.markdown', 'text/plain')).toBe(true);
  });

  it('routes .html to the sandboxed frame rather than the markdown renderer', () => {
    expect(isHtmlFile('page.html', 'text/html; charset=utf-8')).toBe(true);
    expect(isMarkdown('page.html', 'text/html; charset=utf-8')).toBe(false);
  });

  it('previews binary document types', () => {
    expect(fileIsPreviewable('a.pdf', 'application/pdf')).toBe(true);
    expect(fileIsPreviewable('a.png', 'image/png')).toBe(true);
    expect(fileIsPreviewable('a.mp4', 'video/mp4')).toBe(true);
    expect(fileIsPreviewable('a.bin', 'application/octet-stream')).toBe(false);
  });
});

// Rendering is the deliverable: a markdown page must come out as real HTML.
describe('markdown rendering in the share viewer', () => {
  it('renders headings, tables and code instead of their source syntax', async () => {
    const { RenderMarkdown } = await import('@/pages/Chat/markdownBlocks');
    const source = [
      '# Quarterly Report',
      '',
      '| Metric | Value |',
      '| ------ | ----- |',
      '| Users  | 42    |',
      '',
      'Some **bold** text.',
    ].join('\n');

    const html = renderToStaticMarkup(<RenderMarkdown content={source} />);

    expect(html).toContain('<h1');
    expect(html).toContain('Quarterly Report');
    expect(html).toContain('<table');
    expect(html).toContain('<strong>bold</strong>');
    // The source syntax must not survive into the output.
    expect(html).not.toContain('# Quarterly Report');
    expect(html).not.toContain('| Metric | Value |');
    expect(html).not.toContain('**bold**');
  });

  it('does not interpret raw HTML embedded in the markdown', async () => {
    // react-markdown's safe default: no rehype-raw, so injected markup is
    // escaped into inert text. Enabling rehype-raw would be an XSS hole on a
    // public page, so this asserts the markup never becomes live elements —
    // the literal text "onerror" appearing escaped is fine and expected.
    const { RenderMarkdown } = await import('@/pages/Chat/markdownBlocks');
    const html = renderToStaticMarkup(
      <RenderMarkdown content={'<script>alert(1)</script>\n\n<img src=x onerror=alert(1)>'} />,
    );
    // No live script element and no live img carrying the handler.
    expect(html).not.toMatch(/<script[\s>]/i);
    expect(html).not.toMatch(/<img[^>]*onerror/i);
    // The payload survives only as escaped text.
    expect(html).toContain('&lt;script&gt;');
    expect(html).toContain('&lt;img');
  });
});
