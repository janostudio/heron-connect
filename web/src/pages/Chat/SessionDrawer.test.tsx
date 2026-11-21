/**
 * @vitest-environment jsdom
 */
// The chat drawer's header carries two create buttons: one opens the new
// conversation in this tab, the other in a new tab. They sit next to each
// other and look alike, so the wiring has to be pinned: clicking one must not
// trigger the other, and each button must be distinguishable by its title.
// The sort selector has its own describe block below.

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import SessionDrawer from './SessionDrawer';
import i18n from '@/i18n';

let container: HTMLDivElement;
let root: Root | null = null;

function buttonWithTitle(title: string): HTMLButtonElement {
  const button = Array.from(document.body.querySelectorAll('button')).find((b) => b.title === title);
  if (!button) throw new Error(`button with title "${title}" not found`);
  return button;
}

function renderDrawer(props: Record<string, unknown> = {}) {
  root = createRoot(container);
  act(() => {
    root!.render(
      <SessionDrawer
        open
        onClose={() => {}}
        sessions={[]}
        currentSessionId=""
        onSelect={() => {}}
        {...props}
      />,
    );
  });
}

beforeEach(async () => {
  container = document.createElement('div');
  document.body.appendChild(container);
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  // Pin the language so the titles below come from the shipped copy rather
  // than the machine's locale.
  await i18n.changeLanguage('en');
});

afterEach(() => {
  const r = root;
  if (r) act(() => { r.unmount(); });
  container.remove();
  root = null;
  delete (globalThis as any).IS_REACT_ACT_ENVIRONMENT;
  vi.restoreAllMocks();
});

describe('SessionDrawer create buttons', () => {
  it('shows a separate button for opening in a new tab', () => {
    renderDrawer({ onNewSession: () => {}, onNewSessionInNewTab: () => {} });

    expect(buttonWithTitle(String(i18n.t('cmd.new')))).toBeTruthy();
    expect(buttonWithTitle(String(i18n.t('sessions.openInNewTab')))).toBeTruthy();
  });

  it('routes each button to its own handler', () => {
    const inThisTab = vi.fn();
    const inNewTab = vi.fn();
    renderDrawer({ onNewSession: inThisTab, onNewSessionInNewTab: inNewTab });

    act(() => { buttonWithTitle(String(i18n.t('sessions.openInNewTab'))).click(); });
    expect(inNewTab).toHaveBeenCalledTimes(1);
    expect(inThisTab).not.toHaveBeenCalled();

    act(() => { buttonWithTitle(String(i18n.t('cmd.new'))).click(); });
    expect(inThisTab).toHaveBeenCalledTimes(1);
    expect(inNewTab).toHaveBeenCalledTimes(1);
  });

  it('omits the new-tab button when the page does not provide the handler', () => {
    renderDrawer({ onNewSession: () => {} });

    expect(buttonWithTitle(String(i18n.t('cmd.new')))).toBeTruthy();
    const titles = Array.from(document.body.querySelectorAll('button')).map((b) => b.title);
    expect(titles).not.toContain(String(i18n.t('sessions.openInNewTab')));
  });
});

// The two orders are both rendered so the active one is visible rather than
// implied by an icon state; the page owns the value, so the drawer only has to
// report the click.
function buttonWithText(text: string): HTMLButtonElement {
  const button = Array.from(document.body.querySelectorAll('button')).find(
    (b) => (b.textContent || '').trim() === text,
  );
  if (!button) throw new Error(`button with text "${text}" not found`);
  return button;
}

describe('SessionDrawer sort selector', () => {
  it('renders both orders and marks the active one', () => {
    renderDrawer({ sortMode: 'reading', onSortModeChange: () => {} });

    const reading = buttonWithText(String(i18n.t('sessions.sortReading')));
    const activity = buttonWithText(String(i18n.t('sessions.sortActivity')));
    expect(reading.getAttribute('aria-pressed')).toBe('true');
    expect(activity.getAttribute('aria-pressed')).toBe('false');
    // Each option explains its rule on hover: the labels alone do not say
    // whether pinned conversations still come first.
    expect(reading.title).toBe(String(i18n.t('sessions.sortReadingHint')));
    expect(activity.title).toBe(String(i18n.t('sessions.sortActivityHint')));
  });

  it('reports the picked order', () => {
    const onChange = vi.fn();
    renderDrawer({ sortMode: 'reading', onSortModeChange: onChange });

    act(() => { buttonWithText(String(i18n.t('sessions.sortActivity'))).click(); });

    expect(onChange).toHaveBeenCalledWith('activity');
  });

  it('omits the selector when the page provides no handler', () => {
    renderDrawer();

    const labels = Array.from(document.body.querySelectorAll('button')).map((b) => (b.textContent || '').trim());
    expect(labels).not.toContain(String(i18n.t('sessions.sortReading')));
  });
});
