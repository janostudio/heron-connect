/**
 * @vitest-environment jsdom
 */
// The create-session dialog offers "open in new tab" next to the plain create
// button. Two things have to hold for that to be worth anything:
//
//  1. The blank tab is opened inside the click. Browsers only allow a popup
//     from a user gesture, and anything after `await createSession(...)` is no
//     longer one — so the tab must be requested before the request, not after.
//  2. A blocked popup must not swallow the session. If `window.open` returns
//     null the conversation has still been created, so the list tab takes it
//     over instead of leaving the user with nothing to show for the click.

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import SessionList from './SessionList';
import { createSession } from '@/api/sessions';
import i18n from '@/i18n';

vi.mock('@/api/projects', () => ({
  listProjects: async () => ({ projects: [{ name: 'demo-project' }] }),
}));

vi.mock('@/api/sessions', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/sessions')>();
  return {
    ...actual,
    listSessions: async () => ({ sessions: [] }),
    createSession: vi.fn(async () => ({ id: 'fs_test_session', session_key: 'k', name: '' })),
  };
});

let container: HTMLDivElement;
let root: Root | null = null;
let currentPath = '';

// Stands in for the router so a test can tell "stayed on the list" from
// "navigated into the created conversation".
function LocationProbe() {
  const location = useLocation();
  currentPath = location.pathname;
  return null;
}

function buttons(): HTMLButtonElement[] {
  // The dialog renders through a portal into document.body, so it is not a
  // descendant of the test container.
  return Array.from(document.body.querySelectorAll('button'));
}

function buttonWithTitle(title: string): HTMLButtonElement {
  const button = buttons().find((b) => b.title === title);
  if (!button) throw new Error(`button with title "${title}" not found`);
  return button;
}

function newSessionButton(): HTMLButtonElement {
  const button = Array.from(container.querySelectorAll('button')).find((b) =>
    b.textContent?.includes('New Session'),
  );
  if (!button) throw new Error('new-session button not found on the list page');
  return button;
}

async function mount() {
  root = createRoot(container);
  await act(async () => {
    root!.render(
      <MemoryRouter initialEntries={['/sessions']}>
        <SessionList />
        <LocationProbe />
      </MemoryRouter>,
    );
  });
}

async function openDialog() {
  await act(async () => {
    newSessionButton().click();
  });
}

function fakeTab() {
  const replace = vi.fn();
  const close = vi.fn();
  return { tab: { location: { replace }, close } as unknown as Window, replace, close };
}

beforeEach(async () => {
  container = document.createElement('div');
  document.body.appendChild(container);
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  currentPath = '';
  // Pin the language so the assertions below match the shipped English copy
  // regardless of the machine's locale.
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

describe('create-session dialog: open in new tab', () => {
  it('opens the tab inside the click and leaves the list in place', async () => {
    await mount();
    await openDialog();

    const { tab, replace } = fakeTab();
    const open = vi.spyOn(window, 'open').mockReturnValue(tab);

    await act(async () => {
      buttonWithTitle('Open in new tab').click();
    });

    expect(open).toHaveBeenCalledWith('', '_blank');
    expect(replace).toHaveBeenCalledWith(`${window.location.origin}/chat/demo-project/fs_test_session`);
    expect(currentPath).toBe('/sessions');
  });

  it('keeps the conversation reachable when the popup is blocked', async () => {
    await mount();
    await openDialog();

    vi.spyOn(window, 'open').mockReturnValue(null);

    await act(async () => {
      buttonWithTitle('Open in new tab').click();
    });

    expect(currentPath).toBe('/chat/demo-project/fs_test_session');
  });

  it('closes the blank tab and reports the error when creation fails', async () => {
    await mount();
    await openDialog();

    const { tab, replace, close } = fakeTab();
    vi.spyOn(window, 'open').mockReturnValue(tab);
    vi.mocked(createSession).mockRejectedValueOnce(new Error('bridge offline'));

    await act(async () => {
      buttonWithTitle('Open in new tab').click();
    });

    expect(close).toHaveBeenCalled();
    expect(replace).not.toHaveBeenCalled();
    expect(currentPath).toBe('/sessions');
    expect(document.body.textContent).toContain('bridge offline');
  });

  it('still navigates the current tab from the plain create button', async () => {
    await mount();
    await openDialog();

    const open = vi.spyOn(window, 'open');

    // The dialog's action row, reached through the sibling button so this does
    // not accidentally hit the list page's own "New Session" button.
    const actionRow = buttonWithTitle('Open in new tab').parentElement!;
    const createButton = Array.from(actionRow.querySelectorAll('button')).find(
      (b) => b.textContent?.trim() === 'New Session',
    );

    await act(async () => {
      createButton!.click();
    });

    expect(open).not.toHaveBeenCalled();
    expect(currentPath).toBe('/chat/demo-project/fs_test_session');
  });
});
