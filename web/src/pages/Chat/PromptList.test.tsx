/**
 * @vitest-environment jsdom
 */
// Behaviour of the quick-prompt panel: one-click sends, project-scoped list,
// and the two guards that keep a mis-click from being destructive — rows are
// inert while a turn is running, and a delete needs a second click.

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import PromptList from './PromptList';
// Side-effect import: registers the i18next instance so `t()` resolves real
// strings instead of returning raw keys.
import '@/i18n';
import type { QuickPrompt } from '@/api/prompts';

let container: HTMLDivElement;
let root: Root | null = null;

beforeEach(() => {
  container = document.createElement('div');
  document.body.appendChild(container);
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
});

afterEach(() => {
  const r = root;
  if (r) act(() => { r.unmount(); });
  container.remove();
  document.body.innerHTML = '';
  root = null;
  delete (globalThis as any).IS_REACT_ACT_ENVIRONMENT;
});

const makePrompt = (id: string, content: string): QuickPrompt => ({
  id,
  project: 'repo-a',
  content,
  created_at: '2024-01-01T00:00:00Z',
  updated_at: '2024-01-01T00:00:00Z',
});

function render(overrides: Partial<React.ComponentProps<typeof PromptList>> = {}) {
  const handlers = {
    onSend: vi.fn(),
    onCreate: vi.fn().mockResolvedValue(undefined),
    onUpdate: vi.fn().mockResolvedValue(undefined),
    onDelete: vi.fn().mockResolvedValue(undefined),
    onClose: vi.fn(),
  };
  const props = {
    prompts: [makePrompt('qp_1', 'first prompt'), makePrompt('qp_2', 'second prompt')],
    loading: false,
    loadError: null,
    canSend: true,
    isRunning: false,
    maxContentLength: 2000,
    maxPerProject: 50,
    ...handlers,
    ...overrides,
  };
  root = createRoot(container);
  act(() => {
    root!.render(<PromptList {...props} />);
  });
  return { ...handlers, props };
}

const q = (selector: string) => container.querySelector<HTMLElement>(selector);
const qa = (selector: string) => Array.from(container.querySelectorAll<HTMLButtonElement>(selector));
const bodyQ = (selector: string) => document.querySelector(selector) as HTMLElement | null;

function click(el: HTMLElement | null) {
  expect(el).not.toBeNull();
  act(() => {
    el!.click();
  });
}

describe('PromptList', () => {
  it('renders one row per prompt in list order', () => {
    render();
    const rows = qa('[data-testid="prompt-row"]');
    expect(rows.map((r) => r.textContent?.trim())).toEqual(['first prompt', 'second prompt']);
  });

  it('sends the prompt text on click and collapses the panel', () => {
    const { onSend, onClose } = render();
    click(qa('[data-testid="prompt-row"]')[1]);

    expect(onSend).toHaveBeenCalledWith('second prompt');
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('does nothing on click while a turn is running', () => {
    const { onSend, onClose } = render({ isRunning: true });
    const row = qa('[data-testid="prompt-row"]')[0];
    expect(row.disabled).toBe(true);

    click(row);
    expect(onSend).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
  });

  it('does nothing on click while the bridge is down', () => {
    const { onSend } = render({ canSend: false });
    const row = qa('[data-testid="prompt-row"]')[0];
    expect(row.disabled).toBe(true);

    click(row);
    expect(onSend).not.toHaveBeenCalled();
  });

  it('requires a second click to delete', async () => {
    const { onDelete } = render();

    click(qa('[data-testid="prompt-delete"]')[0]);
    expect(onDelete).not.toHaveBeenCalled(); // armed, not deleted

    await act(async () => {
      q('[data-testid="prompt-confirm-delete"]')!.click();
    });

    expect(onDelete).toHaveBeenCalledWith('qp_1');
  });

  it('creates a prompt through the editor', async () => {
    const { onCreate } = render();

    click(q('[data-testid="prompt-add"]'));
    const input = bodyQ('[data-testid="prompt-input"]') as HTMLTextAreaElement;
    expect(input).not.toBeNull();

    const setter = Object.getOwnPropertyDescriptor(
      window.HTMLTextAreaElement.prototype, 'value',
    )!.set!;
    act(() => {
      setter.call(input, 'a brand new prompt');
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });

    await act(async () => {
      bodyQ('[data-testid="prompt-save"]')!.click();
    });

    expect(onCreate).toHaveBeenCalledWith('a brand new prompt');
  });

  it('rejects a prompt starting with "/" instead of saving it', async () => {
    const { onCreate } = render();

    click(q('[data-testid="prompt-add"]'));
    const input = bodyQ('[data-testid="prompt-input"]') as HTMLTextAreaElement;
    const setter = Object.getOwnPropertyDescriptor(
      window.HTMLTextAreaElement.prototype, 'value',
    )!.set!;
    act(() => {
      setter.call(input, '/restart');
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });

    await act(async () => {
      bodyQ('[data-testid="prompt-save"]')!.click();
    });

    expect(onCreate).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain('/');
  });

  it('disables adding once the per-project cap is reached', () => {
    render({ prompts: [makePrompt('qp_1', 'only prompt')], maxPerProject: 1 });
    expect(q('[data-testid="prompt-add"]')!.hasAttribute('disabled')).toBe(true);
  });

  it('shows the load error instead of an empty list', () => {
    render({ prompts: [], loadError: 'boom' });
    expect(container.textContent).toContain('boom');
    expect(qa('[data-testid="prompt-row"]')).toHaveLength(0);
  });
});
