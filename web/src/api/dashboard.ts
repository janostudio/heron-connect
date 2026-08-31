import api from './client';

// ── Dashboard usage statistics (engine-collected) ────────────────────────

export interface DashboardPeriod {
  type: 'day' | 'week' | 'month' | 'custom';
  start: string;
  end: string;
  label: string;
}

export interface DashboardTotals {
  sessions_active: number;
  sessions_new: number;
  turns: number;
  turns_user: number;
  turns_cron: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  total_tokens: number;
  tokens_estimated: boolean;
  active_ms: number;
  tool_calls: number;
  errors: number;
}

export interface DashboardBucket {
  label: string;
  turns: number;
  tokens: number;
  tool_calls: number;
}

export interface DashboardBreakdown {
  name: string;
  sessions: number;
  turns: number;
  total_tokens: number;
  tool_calls: number;
  errors: number;
  active_ms: number;
}

export interface DashboardTopic {
  session_id: string;
  agent_session_id?: string;
  name: string;
  project: string;
  platform: string;
  user_name?: string;
  turns: number;
  total_tokens: number;
  tool_calls: number;
  active_ms: number;
  first_message?: string;
  last_active: string;
}

export interface DashboardReport {
  version: number;
  period: DashboardPeriod;
  scope: { project: string };
  generated_at: string;
  totals: DashboardTotals;
  timeline: DashboardBucket[];
  by_project: DashboardBreakdown[];
  by_platform: DashboardBreakdown[];
  by_agent: DashboardBreakdown[];
  topics: DashboardTopic[];
  top_tools: { name: string; calls: number }[];
  top_users: { name: string; turns: number; total_tokens: number }[];
}

export interface DashboardSettings {
  enabled: boolean;
  collect: boolean;
  insights_path: string;
  html_path: string;
  reports_dir: string;
}

export interface DashboardSessionDetail {
  session_id: string;
  session_name: string;
  project: string;
  start: string;
  end: string;
  turns: {
    ts: string;
    trigger: string;
    duration_ms: number;
    input_tokens: number;
    output_tokens: number;
    cache_read_tokens: number;
    cache_write_tokens: number;
    tokens_estimated: boolean;
    tool_calls: number;
    tools?: Record<string, number>;
    error: string;
  }[];
}

export type DashboardPeriodType = 'day' | 'week' | 'month' | 'custom';

export interface DashboardQuery {
  period: DashboardPeriodType;
  date?: string;   // YYYY-MM-DD (day/week/month)
  start?: string;  // YYYY-MM-DD (custom)
  end?: string;    // YYYY-MM-DD (custom, inclusive)
  project?: string;
}

export const getDashboardSettings = () => api.get<DashboardSettings>('/dashboard/settings');

export const getDashboardReport = (q: DashboardQuery) =>
  api.get<DashboardReport>('/dashboard', Object.fromEntries(
    Object.entries(q).filter(([, v]) => v !== undefined && v !== '')
  ) as Record<string, string>);

export const getDashboardSummary = (project?: string) =>
  api.get<{ today: DashboardReport; week: DashboardReport }>('/dashboard/summary', project ? { project } : undefined);

export const getDashboardSessionDetail = (project: string, sessionID: string, start?: string, end?: string) =>
  api.get<DashboardSessionDetail>(`/dashboard/sessions/${project}/${sessionID}`, (start && end) ? { start, end } : undefined);

// ── Business insights (InsightPayload, fixed-path file) ─────────────────

export type InsightTag = string | { text: string; tone?: string };

export interface InsightMetric {
  label: string;
  value: number | string;
  unit?: string;
}

export interface InsightCard {
  label: string;
  value: number | string;
  unit?: string;
  tone?: string;
}

export interface InsightSession {
  agent_session_id?: string;
  session_id?: string;
  title: string;
  summary?: string;
  metrics?: InsightMetric[];
  tags?: InsightTag[];
  tone?: string;
  detail?: string;
}

export interface InsightPayload {
  version?: number;
  generated_at?: string;
  generated_by?: string;
  period?: { start: string; end: string };
  cards?: InsightCard[];
  sessions?: InsightSession[];
}

// fetchInsights loads the business insights JSON through the authenticated
// files API. Returns null when the file is absent (zone hidden).
export async function fetchInsights(project: string, insightsPath: string): Promise<InsightPayload | null> {
  try {
    const text = await api.raw(`/files/${project}/${insightsPath}`);
    return JSON.parse(text) as InsightPayload;
  } catch {
    return null;
  }
}

// ── Report index (business archive) ──────────────────────────────────────

export interface ReportEntry {
  path: string;
  title: string;
  type?: string;
  format: 'html' | 'md';
  date?: string;
  size: number;
  mtime: number;
  url: string;
}

export const listReports = (project: string, type?: string, limit?: number) =>
  api.get<{ project: string; reports: ReportEntry[] }>('/reports', {
    project,
    ...(type ? { type } : {}),
    ...(limit ? { limit: String(limit) } : {}),
  });

// ── Formatting helpers ───────────────────────────────────────────────────

export function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${Math.round(n / 1_000)}k`;
  return String(n);
}

export function formatDuration(ms: number): string {
  const mins = Math.round(ms / 60000);
  if (mins < 60) return `${mins}min`;
  return `${Math.floor(mins / 60)}h${mins % 60}m`;
}

export function insightTagText(tag: InsightTag): string {
  return typeof tag === 'string' ? tag : tag.text;
}

export function insightTagTone(tag: InsightTag): string {
  return typeof tag === 'string' ? '' : (tag.tone || '');
}
