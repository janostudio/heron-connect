/**
 * @vitest-environment jsdom
 */
// The mermaid renderer is stubbed so these tests pin MermaidBlock's own
// behaviour — when it draws, when it falls back to source, and that it never
// injects anything it did not get from a successful render.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';

const renderMermaid = vi.fn();
vi.mock('@/lib/mermaidRenderer', () => ({
  renderMermaid: (code: string, theme: string) => renderMermaid(code, theme),
}));

const { MermaidBlock } = await import('./MermaidBlock');

const CODE = 'graph TD; A-->B;';

let container: HTMLDivElement;
let root: Root | null = null;

beforeEach(() => {
  // React only treats act() as the current environment when this flag is set;
  // without it every act() call logs a "not configured to support act" warning.
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  container = document.createElement('div');
  document.body.appendChild(container);
  renderMermaid.mockReset();
  renderMermaid.mockResolvedValue({ svg: '<svg data-testid="diagram"></svg>' });
});

afterEach(() => {
  act(() => root?.unmount());
  root = null;
  container.remove();
  delete (globalThis as any).IS_REACT_ACT_ENVIRONMENT;
});

// Mounts and lets the async render effect settle.
async function mount(props: { code: string; streaming?: boolean }) {
  root = createRoot(container);
  await act(async () => {
    root!.render(React.createElement(MermaidBlock, props));
  });
}

describe('MermaidBlock', () => {
  it('shows the source and never calls mermaid while streaming', async () => {
    await mount({ code: CODE, streaming: true });
    expect(container.textContent).toContain('graph TD; A-->B;');
    expect(container.querySelector('svg[data-testid="diagram"]')).toBeNull();
    expect(renderMermaid).not.toHaveBeenCalled();
  });

  it('injects the rendered svg once streaming ends', async () => {
    await mount({ code: CODE, streaming: false });
    expect(renderMermaid).toHaveBeenCalledWith(CODE, expect.any(String));
    expect(container.querySelector('svg[data-testid="diagram"]')).not.toBeNull();
  });

  it('falls back to the source when mermaid rejects the syntax', async () => {
    renderMermaid.mockResolvedValue({ error: 'Parse error on line 1' });
    await mount({ code: 'nonsense', streaming: false });
    expect(container.textContent).toContain('nonsense');
    expect(container.textContent).toContain('Parse error on line 1');
    // Nothing was injected.
    expect(container.querySelector('svg[data-testid="diagram"]')).toBeNull();
  });

  it('never injects markup on the error path', async () => {
    renderMermaid.mockResolvedValue({ error: 'x' });
    await mount({ code: '<script>alert(1)</script>', streaming: false });
    expect(container.querySelector('script')).toBeNull();
    expect(container.textContent).toContain('<script>alert(1)</script>');
  });

  it('toggles between the diagram and its source', async () => {
    await mount({ code: CODE, streaming: false });
    expect(container.querySelector('svg[data-testid="diagram"]')).not.toBeNull();

    const toggle = container.querySelector('button')!;
    await act(async () => { toggle.dispatchEvent(new MouseEvent('click', { bubbles: true })); });

    expect(container.querySelector('svg[data-testid="diagram"]')).toBeNull();
    expect(container.textContent).toContain('graph TD; A-->B;');

    await act(async () => { toggle.dispatchEvent(new MouseEvent('click', { bubbles: true })); });
    expect(container.querySelector('svg[data-testid="diagram"]')).not.toBeNull();
  });
});
