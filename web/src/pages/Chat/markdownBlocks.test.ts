import { describe, it, expect } from 'vitest';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { stripReplyFooter, RenderMarkdown } from './markdownBlocks';

// stripReplyFooter removes the trailing `*model · usage · path*` runtime footer
// that heron-connect appends to assistant replies, so copying a message does not
// carry runtime metadata into the clipboard. The heuristics are deliberately
// conservative — a false positive would silently eat a legitimate last line.

describe('stripReplyFooter', () => {
  it('removes a single-line italic footer', () => {
    expect(stripReplyFooter('The answer is 42.\n\n*codebuddy · 1.2k tokens · /tmp/x*'))
      .toBe('The answer is 42.');
  });

  it('removes the footer and the blank line before it', () => {
    const out = stripReplyFooter('line one\nline two\n\n*model · usage*');
    expect(out).toBe('line one\nline two');
    expect(out.endsWith('\n')).toBe(false);
  });

  it('leaves text with no footer untouched', () => {
    const text = 'Just a normal reply.';
    expect(stripReplyFooter(text)).toBe(text);
  });

  it('leaves a trailing italic line that contains asterisks inside', () => {
    // `*bold *nested* text*` is not a simple footer — must be preserved.
    const text = 'reply\n*some *inner* asterisks*';
    expect(stripReplyFooter(text)).toBe(text);
  });

  it('does not strip a single-line reply that is entirely italic', () => {
    // A whole message wrapped in italics is legitimate content, and stripping
    // it would leave an empty string.
    const text = '*emphasis*';
    expect(stripReplyFooter(text)).toBe(text);
  });

  it('handles an empty or whitespace-only string', () => {
    expect(stripReplyFooter('')).toBe('');
    expect(stripReplyFooter('   \n  ')).toBe('   \n  ');
  });

  it('handles a footer-only reply without stripping it to nothing', () => {
    // A message consisting of exactly one italic line has no reply body above
    // it; treating it as a footer would copy an empty string.
    expect(stripReplyFooter('*model · usage*')).toBe('*model · usage*');
  });

  it('only removes the LAST line, keeping earlier italic lines', () => {
    const text = '*intro*\n\nbody\n\n*model · usage*';
    expect(stripReplyFooter(text)).toBe('*intro*\n\nbody');
  });

  it('tolerates trailing blank lines after the footer', () => {
    expect(stripReplyFooter('body\n\n*model · usage*\n\n\n')).toBe('body');
  });

  it('tolerates leading/trailing spaces around the footer', () => {
    expect(stripReplyFooter('body\n   *model · usage*   ')).toBe('body');
  });

  it('does not strip a markdown horizontal-rule-ish line', () => {
    const text = 'body\n***';
    expect(stripReplyFooter(text)).toBe(text);
  });

  it('does not strip a bulleted list item ending the message', () => {
    const text = 'steps:\n* do a thing';
    expect(stripReplyFooter(text)).toBe(text);
  });
});

// ```mermaid fences are routed to MermaidBlock instead of the plain code-block
// renderer. Under static rendering the block shows its source (the render
// effect needs a DOM + browser), which is what makes this observable here.
describe('mermaid fence routing', () => {
  it('routes a mermaid fence away from the generic code block', () => {
    const html = renderToStaticMarkup(
      createElement(RenderMarkdown, { content: '```mermaid\ngraph TD; A-->B;\n```' }),
    );
    // MermaidBlock's fallback carries the `mermaid` language tab and the source.
    expect(html).toContain('>mermaid</div>');
    expect(html).toContain('graph TD; A--&gt;B;');
  });

  it('leaves an ordinary fenced block on the generic code path', () => {
    const html = renderToStaticMarkup(
      createElement(RenderMarkdown, { content: '```js\nconst x = 1;\n```' }),
    );
    expect(html).toContain('>js</div>');
    expect(html).not.toContain('>mermaid</div>');
  });
});
