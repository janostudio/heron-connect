/**
 * @vitest-environment jsdom
 */
// The guard is what turns "you were in a conversation" into "after logging in
// you are back in that conversation". Two things have to hold: the target is
// carried on the login URL, and it is remembered even if the login page is
// reloaded (query string gone).

import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import ProtectedRoute from './ProtectedRoute';
import { useAuthStore } from '@/store/auth';

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

/** Stands in for the login page; exposes the query it was reached with. */
function LoginProbe() {
  const location = useLocation();
  return React.createElement('div', { 'data-testid': 'login' }, location.search);
}

function renderAt(path: string) {
  root = createRoot(container);
  act(() => {
    root!.render(
      React.createElement(
        MemoryRouter,
        { initialEntries: [path] },
        React.createElement(
          Routes,
          null,
          React.createElement(Route, { path: '/login', element: React.createElement(LoginProbe) }),
          React.createElement(Route, {
            path: '/chat/:name/:id',
            element: React.createElement(
              ProtectedRoute,
              null,
              React.createElement('div', { 'data-testid': 'chat' }, 'conversation'),
            ),
          }),
        ),
      ),
    );
  });
}

describe('ProtectedRoute', () => {
  it('sends the user to the login page with the conversation as ?next=', () => {
    renderAt('/chat/demo/sess-1');

    const login = container.querySelector('[data-testid="login"]');
    expect(login).not.toBeNull();
    expect(login!.textContent).toBe('?next=%2Fchat%2Fdemo%2Fsess-1');
    expect(container.querySelector('[data-testid="chat"]')).toBeNull();
  });

  it('remembers the target in storage, so a reloaded login page can still resume', () => {
    renderAt('/chat/demo/sess-1');

    expect(localStorage.getItem('cc_return_path')).toBe('/chat/demo/sess-1');
  });

  it('renders the conversation without redirecting once authenticated', () => {
    useAuthStore.setState({ isAuthenticated: true });
    renderAt('/chat/demo/sess-1');

    expect(container.querySelector('[data-testid="chat"]')).not.toBeNull();
    expect(container.querySelector('[data-testid="login"]')).toBeNull();
  });
});
