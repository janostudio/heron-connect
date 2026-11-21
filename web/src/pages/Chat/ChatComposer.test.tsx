/**
 * @vitest-environment jsdom
 */
// Regression test for the 2026-09-16 typing-lag bug.
//
// A perf trace showed ~130ms of main-thread work per keystroke on a long
// conversation: the message text lived in ChatView's own state, so every
// keystroke re-rendered the whole page and made react-markdown re-parse every
// transcript row.
//
// The fix moved the draft into ChatComposer. This test pins the property that
// makes the fix hold: typing in the composer must not re-render sibling
// subtrees (the stand-in here is the transcript). If the draft state ever moves
// back up into the parent, this test fails.

import { describe, it, expect, beforeEach, afterEach, vi, type Mock } from 'vitest';
import React, { useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import ChatComposer from './ChatComposer';
// Side-effect import: registers the i18next instance so `t()` resolves real
// strings. Without it react-i18next warns NO_I18NEXT_INSTANCE and returns the
// raw keys, which would make the title assertions below meaningless.
import '@/i18n';

let container: HTMLDivElement;
// Lazily created per test and dropped in afterEach: each test owns its own
// container, so a stale root from the previous test must not be reused.
let root: Root | null = null;
beforeEach(() => {
  container = document.createElement('div');
  document.body.appendChild(container);
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  siblingRenders = 0;
});

afterEach(() => {
  const r = root;
  if (r) act(() => { r.unmount(); });
  container.remove();
  root = null;
  delete (globalThis as any).IS_REACT_ACT_ENVIRONMENT;
});

/** A sibling that counts its renders — stands in for the transcript subtree. */
let siblingRenders = 0;
function CountingSibling() {
  siblingRenders++;
  return React.createElement('div', { 'data-testid': 'transcript' }, 'transcript');
}

function Harness({ onSend, isRunning = false, interruptible = false, onStop = () => {} }: {
  onSend: (t: string) => void;
  isRunning?: boolean;
  interruptible?: boolean;
  onStop?: () => void;
}) {
  // Holds the state a real ChatView holds, so a keystroke leaking into the
  // parent would show up as extra sibling renders.
  const [picked] = useState<any[]>([]);
  return React.createElement(
    React.Fragment,
    null,
    React.createElement(CountingSibling),
    React.createElement(ChatComposer, {
      onSend,
      pickedFiles: picked,
      onRemoveFile: () => {},
      onAddFiles: () => {},
      canSend: true,
      bridgeCfgLoaded: true,
      bridgeStatus: 'connected',
      isRunning,
      interruptible,
      onStop,
      cmdOpen: false,
      onCmdOpenChange: () => {},
      onCmdSelect: () => {},
    }),
  );
}

/** Reuse the mounted root within a test, mounting it on first use. */
function renderInto(props: Parameters<typeof Harness>[0]) {
  if (!root) root = createRoot(container);
  const r = root;
  act(() => { r.render(React.createElement(Harness, props)); });
}

function render(onSend: (t: string) => void = () => {}) {
  renderInto({ onSend });
  return container.querySelector('textarea') as HTMLTextAreaElement;
}

/** Render with the stop button showing, so its title/behaviour can be asserted. */
function renderRunning(opts: { interruptible: boolean; onStop?: () => void }) {
  renderInto({ onSend: () => {}, isRunning: true, ...opts });
  return container.querySelector('[data-testid="composer-stop"]') as HTMLButtonElement;
}

function typeInto(textarea: HTMLTextAreaElement, value: string) {
  // React tracks the DOM value; bypass its value-setter cache before dispatch.
  const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!;
  setter.call(textarea, value);
  textarea.dispatchEvent(new Event('input', { bubbles: true }));
}

function clickSend() {
  // The send button is the last button and carries no title attribute.
  const buttons = Array.from(container.querySelectorAll('button'));
  const send = buttons[buttons.length - 1];
  act(() => { send.dispatchEvent(new MouseEvent('click', { bubbles: true })); });
}

describe('ChatComposer typing isolation', () => {
  it('does not re-render sibling subtrees while typing', () => {
    const textarea = render();
    expect(textarea).toBeTruthy();
    const afterMount = siblingRenders;

    act(() => { typeInto(textarea, 'h'); });
    act(() => { typeInto(textarea, 'he'); });
    act(() => { typeInto(textarea, 'hel'); });

    expect(siblingRenders).toBe(afterMount);
  });

  it('updates the textarea value as the user types', () => {
    const textarea = render();

    act(() => { typeInto(textarea, 'hello world'); });

    expect((container.querySelector('textarea') as HTMLTextAreaElement).value).toBe('hello world');
  });

  it('sends the trimmed draft and clears the composer', () => {
    const sent: string[] = [];
    const textarea = render((t) => sent.push(t));

    act(() => { typeInto(textarea, '  ship it  '); });
    clickSend();

    expect(sent).toEqual(['ship it']);
    expect((container.querySelector('textarea') as HTMLTextAreaElement).value).toBe('');
  });
});

// ── Stop button: the two busy states ─────────────────────────
//
// The button looks the same whether the agent is configured to interrupt or to
// queue, because clicking it means the same thing either way (stop this turn).
// What differs is the tooltip, which tells the user what a *new message* would
// do. The `interruptible` flag comes from the management API because the web
// client cannot know the agent's config, and a missing flag must degrade to the
// queue wording rather than crash.

describe('ChatComposer stop button', () => {
  it('shows the interrupt wording when the agent interrupts mid-turn', () => {
    const stop = renderRunning({ interruptible: true });
    expect(stop.title).toMatch(/interrupt/i);
    expect(stop.title).not.toMatch(/queue/i);
  });

  it('shows the queue wording when the agent queues mid-turn messages', () => {
    const stop = renderRunning({ interruptible: false });
    expect(stop.title).toMatch(/queue/i);
  });

  it('stays clickable and calls onStop in BOTH states', () => {
    // /stop must always be able to cancel a long-running turn — queueing new
    // messages is not a reason to trap the user in a turn they cannot end.
    for (const interruptible of [true, false]) {
      let calls = 0;
      const stop = renderRunning({ interruptible, onStop: () => { calls++; } });

      expect(stop.disabled).toBe(false);
      act(() => { stop.dispatchEvent(new MouseEvent('click', { bubbles: true })); });
      expect(calls).toBe(1);
    }
  });

  it('offers the send button when idle, regardless of interruptible', () => {
    renderInto({ onSend: () => {}, interruptible: true });
    expect(container.querySelector('[data-testid="composer-stop"]')).toBeNull();
    expect(container.querySelector('[data-testid="composer-send"]')).not.toBeNull();
  });
});

// ── Sending while a turn runs ────────────────────────────────
//
// The backend decides what a mid-turn message does (interrupt-and-run-first, or
// queue). The composer must therefore let it through instead of swallowing it —
// otherwise the interrupt path that IM platforms have is unreachable from Web.

describe('ChatComposer sending while running', () => {
  it('shows only Stop while running with an empty composer', () => {
    renderInto({ onSend: () => {}, isRunning: true, interruptible: true });
    expect(container.querySelector('[data-testid="composer-stop"]')).not.toBeNull();
    expect(container.querySelector('[data-testid="composer-send"]')).toBeNull();
  });

  it('shows Send next to Stop once there is a draft, and sends it', () => {
    const sent: string[] = [];
    renderInto({ onSend: (t) => sent.push(t), isRunning: true, interruptible: true });
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement;

    act(() => { typeInto(textarea, 'actually, do X instead'); });

    expect(container.querySelector('[data-testid="composer-stop"]')).not.toBeNull();
    const send = container.querySelector('[data-testid="composer-send"]') as HTMLButtonElement;
    expect(send).not.toBeNull();
    act(() => { send.dispatchEvent(new MouseEvent('click', { bubbles: true })); });

    expect(sent).toEqual(['actually, do X instead']);
  });

  it('Enter sends while running', () => {
    const sent: string[] = [];
    renderInto({ onSend: (t) => sent.push(t), isRunning: true, interruptible: false });
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement;

    act(() => { typeInto(textarea, 'queue me'); });
    act(() => {
      textarea.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }));
    });

    expect(sent).toEqual(['queue me']);
  });

  it('the Send tooltip says interrupt vs queue according to the agent', () => {
    for (const [interruptible, re] of [[true, /interrupt/i], [false, /queue/i]] as const) {
      renderInto({ onSend: () => {}, isRunning: true, interruptible });
      const textarea = container.querySelector('textarea') as HTMLTextAreaElement;
      act(() => { typeInto(textarea, 'x'); });
      const send = container.querySelector('[data-testid="composer-send"]') as HTMLButtonElement;
      expect(send.title).toMatch(re);
      act(() => { typeInto(textarea, ''); });
    }
  });
});

// ── Quick-prompt panel ───────────────────────────────────────
//
// The panel is driven by focus, not by a toggle. These tests pin the three
// behaviours that are easy to break by accident: it opens on focus, it survives
// long enough for a click to land after focus leaves the textarea, and it does
// not fight the slash-command palette for the same space.

describe('ChatComposer quick-prompt panel', () => {
  const PANEL = '[data-testid="prompt-panel"]';

  function mountComposer(opts: {
    canSend?: boolean;
    cmdOpen?: boolean;
    onPromptsOpenChange?: Mock<(open: boolean) => void>;
  } = {}) {
    const onPromptsOpenChange = opts.onPromptsOpenChange ?? vi.fn();
    let close = () => {};
    const promptsSlot = (controls: { close: () => void }) => {
      close = controls.close;
      return React.createElement('div', { 'data-testid': 'prompt-panel' }, 'panel');
    };
    if (!root) root = createRoot(container);
    act(() => {
      root!.render(React.createElement(ChatComposer, {
        onSend: () => {},
        pickedFiles: [],
        onRemoveFile: () => {},
        onAddFiles: () => {},
        canSend: opts.canSend ?? true,
        bridgeCfgLoaded: true,
        bridgeStatus: opts.canSend === false ? 'disconnected' : 'connected',
        isRunning: false,
        interruptible: false,
        onStop: () => {},
        cmdOpen: opts.cmdOpen ?? false,
        onCmdOpenChange: () => {},
        onCmdSelect: () => {},
        promptsSlot,
        onPromptsOpenChange,
      }));
    });
    return { onPromptsOpenChange, close: () => close() };
  }

  it('opens on focus and closes after focus leaves', async () => {
    const { onPromptsOpenChange } = mountComposer();
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement;

    act(() => { textarea.focus(); });
    expect(container.querySelector(PANEL)).not.toBeNull();
    expect(onPromptsOpenChange).toHaveBeenLastCalledWith(true);

    act(() => { textarea.blur(); });
    // Still mounted: the close is delayed so a click on a row can land.
    expect(container.querySelector(PANEL)).not.toBeNull();

    await act(async () => { await new Promise((r) => setTimeout(r, 220)); });
    expect(container.querySelector(PANEL)).toBeNull();
    expect(onPromptsOpenChange).toHaveBeenLastCalledWith(false);
  });

  it('collapses when a prompt is sent and reopens on the next click', () => {
    const { close } = mountComposer();
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement;

    act(() => { textarea.focus(); });
    expect(container.querySelector(PANEL)).not.toBeNull();

    act(() => { close(); });
    expect(container.querySelector(PANEL)).toBeNull();

    // The textarea already has focus, so no new focus event fires — the click
    // handler is what brings the panel back.
    act(() => { textarea.dispatchEvent(new MouseEvent('click', { bubbles: true })); });
    expect(container.querySelector(PANEL)).not.toBeNull();
  });

  it('stays closed while the command palette is open', () => {
    mountComposer({ cmdOpen: true });
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement;

    act(() => { textarea.focus(); });
    expect(container.querySelector(PANEL)).toBeNull();
  });

  it('collapses when the bridge goes down', () => {
    // One probe shared across both renders: the second mount passes a fresh
    // callback otherwise, and the assertion would read the first render's mock.
    const probe = vi.fn();
    mountComposer({ onPromptsOpenChange: probe });
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement;
    act(() => { textarea.focus(); });
    expect(container.querySelector(PANEL)).not.toBeNull();

    // Re-render the same root with the bridge disconnected: the composer swaps
    // itself for a warning row, so the panel must be reported closed or the
    // parent keeps hiding its "jump to latest" button.
    mountComposer({ canSend: false, onPromptsOpenChange: probe });
    expect(container.querySelector(PANEL)).toBeNull();
    expect(probe).toHaveBeenLastCalledWith(false);
  });
});
