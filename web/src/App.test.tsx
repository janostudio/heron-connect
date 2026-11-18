/**
 * @vitest-environment jsdom
 */
// Wiring test for post-login resume.
//
// Two routes have to agree for the feature to work: the guard records where the
// user was, and `/login` sends an authenticated visitor to that recorded target
// instead of the dashboard. The unit tests cover the decision function and the
// guard; this one covers the router-level wiring between them.

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { useAuthStore } from '@/store/auth';
import '@/i18n';

// The chat page and the layout pull in the websocket and poll several APIs;
// neither is what this test is about.
vi.mock('@/pages/Chat/ChatView', async () => {
  const React = await import('react');
  return { default: () => React.createElement('div', { 'data-testid': 'chatview' }, 'conversation') };
});
vi.mock('@/components/Layout/Layout', async () => {
  const React = await import('react');
  const { Outlet } = await import('react-router-dom');
  return { default: () => React.createElement('div', null, React.createElement(Outlet)) };
});

const { default: App } = await import('./App');

let container: HTMLDivElement;
let root: Root | null = null;

beforeEach(() => {
  container = document.createElement('div');
  document.body.appendChild(container);
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  localStorage.clear();
  useAuthStore.setState({ isAuthenticated: false });
});

afterEach(() => {
  const r = root;
  if (r) act(() => { r.unmount(); });
  container.remove();
  root = null;
  delete (globalThis as any).IS_REACT_ACT_ENVIRONMENT;
  useAuthStore.setState({ isAuthenticated: false });
});

function LocationProbe() {
  const location = useLocation();
  return React.createElement('div', { 'data-testid': 'loc' }, location.pathname + location.search);
}

function renderAt(path: string) {
  root = createRoot(container);
  act(() => {
    root!.render(
      React.createElement(
        MemoryRouter,
        { initialEntries: [path] },
        React.createElement(LocationProbe),
        React.createElement(App),
      ),
    );
  });
}

describe('post-login resume wiring', () => {
  it('resumes the conversation when an authenticated visitor lands on /login?next=', () => {
    useAuthStore.setState({ isAuthenticated: true });
    renderAt('/login?next=%2Fchat%2Fdemo%2Fsess-1');

    expect(container.querySelector('[data-testid="chatview"]')).not.toBeNull();
    expect(container.querySelector('[data-testid="loc"]')!.textContent).toBe('/chat/demo/sess-1');
  });

  it('shows the login form and carries the conversation as ?next= when unauthenticated', () => {
    renderAt('/chat/demo/sess-1');

    expect(container.querySelector('[data-testid="chatview"]')).toBeNull();
    expect(container.querySelector('[data-testid="loc"]')!.textContent).toBe(
      '/login?next=%2Fchat%2Fdemo%2Fsess-1',
    );
    // The login form is rendered rather than the app shell.
    expect(container.querySelector('form')).not.toBeNull();
  });
});
