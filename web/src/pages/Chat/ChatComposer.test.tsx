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

import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import React, { useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import ChatComposer from './ChatComposer';

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  container = document.createElement('div');
  document.body.appendChild(container);
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  siblingRenders = 0;
});

afterEach(() => {
  act(() => { root.unmount(); });
  container.remove();
  delete (globalThis as any).IS_REACT_ACT_ENVIRONMENT;
});

/** A sibling that counts its renders — stands in for the transcript subtree. */
let siblingRenders = 0;
function CountingSibling() {
  siblingRenders++;
  return React.createElement('div', { 'data-testid': 'transcript' }, 'transcript');
}

function Harness({ onSend }: { onSend: (t: string) => void }) {
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
      isRunning: false,
      onStop: () => {},
      cmdOpen: false,
      onCmdOpenChange: () => {},
      onCmdSelect: () => {},
    }),
  );
}

function render(onSend: (t: string) => void = () => {}) {
  root = createRoot(container);
  act(() => { root.render(React.createElement(Harness, { onSend })); });
  return container.querySelector('textarea') as HTMLTextAreaElement;
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
