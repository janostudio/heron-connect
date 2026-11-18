/**
 * @vitest-environment jsdom
 */
// Regression test: what the user types into the rename dialog used to vanish
// on any parent re-render.
//
// Both call sites pass `session` as an inline `{ id, name }` literal, and their
// parents re-render continuously — the chat page on every bridge frame while
// the agent streams, the session list on its 5s poll. The old effect depended
// on the `session` object, so its identity changed on each of those renders and
// re-seeded the input from the stored name, discarding the edit.
//
// The fix keys the re-seed on the session id. These tests pin both halves: a
// parent re-render must NOT touch the draft, a different session MUST re-seed.

import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import React, { useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import RenameSessionModal from './RenameSessionModal';
// Side-effect import: registers the i18next instance so `t()` resolves real
// strings instead of raw keys.
import '@/i18n';

let container: HTMLDivElement;
let root: Root | null = null;

// Controls the harness exposes so a test can drive it the way the real pages
// do (a poll tick / a bridge frame) without unmounting the dialog.
let reRenderParent: () => void = () => {};
let pointAtOtherSession: () => void = () => {};

beforeEach(() => {
  container = document.createElement('div');
  document.body.appendChild(container);
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
});

afterEach(() => {
  const r = root;
  if (r) act(() => { r.unmount(); });
  container.remove();
  root = null;
  delete (globalThis as any).IS_REACT_ACT_ENVIRONMENT;
  reRenderParent = () => {};
  pointAtOtherSession = () => {};
});

function Harness() {
  const [target, setTarget] = useState({ id: 'fs_demo_a', name: 'original name' });
  const [, setTick] = useState(0);
  reRenderParent = () => setTick((t) => t + 1);
  pointAtOtherSession = () => setTarget({ id: 'fs_demo_b', name: 'other session' });
  return React.createElement(RenameSessionModal, {
    open: true,
    project: 'demo-project',
    // Deliberately inline, exactly like both real call sites.
    session: { id: target.id, name: target.name },
    onClose: () => {},
  });
}

// The dialog renders through a portal into document.body, so it is not a
// descendant of the test container.
function dialogInput(): HTMLInputElement {
  const input = document.body.querySelector('input');
  if (!input) throw new Error('rename dialog input not found');
  return input as HTMLInputElement;
}

function mount(): HTMLInputElement {
  root = createRoot(container);
  act(() => { root!.render(React.createElement(Harness)); });
  return dialogInput();
}

function typeInto(input: HTMLInputElement, value: string) {
  // React caches the DOM value; bypass its setter before dispatching.
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
  setter.call(input, value);
  input.dispatchEvent(new Event('input', { bubbles: true }));
}

describe('RenameSessionModal draft survival', () => {
  it('keeps the typed name when the parent re-renders', () => {
    const input = mount();
    expect(input.value).toBe('original name');

    act(() => { typeInto(input, 'renamed by user'); });
    expect(input.value).toBe('renamed by user');

    // Stand-in for a bridge frame / list poll re-rendering the page.
    act(() => { reRenderParent(); });
    act(() => { reRenderParent(); });

    expect(dialogInput().value).toBe('renamed by user');
  });

  it('re-seeds the field when the dialog moves to another session', () => {
    const input = mount();
    act(() => { typeInto(input, 'typed for the first session'); });

    act(() => { pointAtOtherSession(); });

    expect(dialogInput().value).toBe('other session');
  });
});
