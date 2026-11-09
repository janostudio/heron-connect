/**
 * @vitest-environment jsdom
 */
// The session list hides scheduled-run sessions by default: a job with
// session_mode=new_per_run leaves one isolated session per run, and they sort to
// the top by run time, so without the filter they push human conversations out
// of view. These tests pin the two halves of that behaviour — hidden by default,
// revealed by the toggle — plus the count that keeps the filter visible rather
// than silent.

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router-dom';
import SessionList from './SessionList';
// Side-effect import: registers i18next so `t()` resolves real strings.
import '@/i18n';
import type { Session } from '@/api/sessions';

vi.mock('@/api/projects', () => ({
  listProjects: vi.fn(),
}));
vi.mock('@/api/sessions', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/sessions')>();
  return {
    ...actual,
    // Keep sortSessions / splitByOrigin real: the behaviour under test is how
    // the component uses them together.
    listSessions: vi.fn(),
    createSession: vi.fn(),
    updateSession: vi.fn(),
  };
});

const humanSession: Session = {
  id: 's-human',
  session_key: 'wecom:user:chat',
  name: 'human conversation',
  platform: 'wecom',
  agent_type: 'codebuddy',
  active: true,
  live: false,
  created_at: '2026-11-01T10:00:00Z',
  updated_at: '2026-11-01T10:00:00Z',
  last_user_at: '2026-11-01T10:00:00Z',
  history_count: 4,
  last_message: null,
};

const cronSession: Session = {
  ...humanSession,
  id: 's-cron',
  name: 'cron-daily-report',
  origin: 'cron',
  last_user_at: '2026-11-02T10:00:00Z',
};

let container: HTMLDivElement;
let root: Root | null = null;

beforeEach(() => {
  localStorage.clear();
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
});

async function renderList() {
  const { listProjects } = await import('@/api/projects');
  const { listSessions } = await import('@/api/sessions');
  (listProjects as any).mockResolvedValue({ projects: [{ name: 'proj' }] });
  (listSessions as any).mockResolvedValue({ sessions: [humanSession, cronSession], active_keys: {} });

  await act(async () => {
    root = createRoot(container);
    root.render(
      <MemoryRouter>
        <SessionList />
      </MemoryRouter>,
    );
  });
  // Let the fetch promise chain settle.
  await act(async () => {});
}

const toggle = () => container.querySelector('input[type="checkbox"]') as HTMLInputElement;

describe('SessionList scheduled-run filter', () => {
  it('hides scheduled-run sessions by default and reports the hidden count', async () => {
    await renderList();

    expect(container.textContent).toContain('human conversation');
    expect(container.textContent).not.toContain('cron-daily-report');
    // The filter must be visible, not silent: the counter says something is hidden.
    expect(container.textContent).toMatch(/1 .*(scheduled-run|定时任务)/i);
    expect(toggle().checked).toBe(false);
  });

  it('reveals them when the toggle is switched on', async () => {
    await renderList();

    await act(async () => {
      toggle().click();
    });

    expect(container.textContent).toContain('cron-daily-report');
    expect(container.textContent).toContain('human conversation');
    expect(toggle().checked).toBe(true);
    // Nothing is hidden, so the count disappears.
    expect(container.textContent).not.toMatch(/1 .*(scheduled-run|定时任务)/i);
  });

  it('remembers the choice for the next visit', async () => {
    await renderList();
    await act(async () => {
      toggle().click();
    });

    const r = root;
    if (r) act(() => { r.unmount(); });
    root = null;
    container.innerHTML = '';

    await renderList();
    expect(toggle().checked).toBe(true);
    expect(container.textContent).toContain('cron-daily-report');
  });
});
