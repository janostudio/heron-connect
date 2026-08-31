import { useEffect, useMemo, useState, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { useParams, Link, useNavigate } from 'react-router-dom';
import {
  MessageSquare, Settings, ChevronDown, ChevronRight, ExternalLink,
  BarChart3, Activity, RefreshCw,
} from 'lucide-react';
import { Card, StatCard, Badge, Button, Input, EmptyState } from '@/components/ui';
import {
  getDashboardSettings, getDashboardReport, getDashboardSessionDetail, fetchInsights,
  formatTokens, formatDuration, insightTagText, insightTagTone,
  type DashboardReport, type DashboardSettings, type InsightPayload, type InsightSession,
  type DashboardSessionDetail, type DashboardPeriodType,
} from '@/api/dashboard';
import { useAuthStore } from '@/store/auth';
import { cn } from '@/lib/utils';
import { api } from '@/api/client';

// Merged session row: engine baseline + business enrichment.
interface MergedRow {
  key: string;
  sessionId?: string;      // engine session id (jump key)
  agentSessionId?: string; // CLI session id (secondary jump key)
  title: string;
  platform?: string;
  userName?: string;
  turns: number;
  totalTokens: number;
  toolCalls: number;
  activeMs: number;
  lastActive?: string;
  firstMessage?: string;
  summary?: string;
  metrics?: InsightSession['metrics'];
  tags?: InsightSession['tags'];
  tone?: string;
  detail?: string;
  business: boolean;
}

const TAG_TONE_CLASSES: Record<string, string> = {
  good: 'bg-emerald-50 text-emerald-600 dark:bg-emerald-900/30 dark:text-emerald-400',
  info: 'bg-blue-50 text-blue-600 dark:bg-blue-900/30 dark:text-blue-400',
  warn: 'bg-amber-50 text-amber-600 dark:bg-amber-900/30 dark:text-amber-400',
  error: 'bg-red-50 text-red-600 dark:bg-red-900/30 dark:text-red-400',
};

function TagChip({ tag, onClick }: { tag: string | { text: string; tone?: string }; onClick?: (e: React.MouseEvent) => void }) {
  const text = insightTagText(tag);
  const tone = insightTagTone(tag);
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        'px-1.5 py-0.5 rounded text-[10px] font-medium border-0 cursor-pointer',
        TAG_TONE_CLASSES[tone] || 'bg-gray-100 text-gray-500 dark:bg-white/[0.06] dark:text-gray-400',
        onClick && 'hover:opacity-80'
      )}
    >
      {text}
    </button>
  );
}

export default function ProjectOverview() {
  const { t } = useTranslation();
  const { name = '' } = useParams<{ name: string }>();
  const navigate = useNavigate();
  const token = useAuthStore((s) => s.token);

  // ── Settings & presence ──────────────────────────────────────────────
  const [settings, setSettings] = useState<DashboardSettings | null>(null);
  const [settingsLoaded, setSettingsLoaded] = useState(false);

  // ── Time filter ──────────────────────────────────────────────────────
  const [period, setPeriod] = useState<DashboardPeriodType>('day');
  const [date, setDate] = useState(() => new Date().toISOString().slice(0, 10));
  const [start, setStart] = useState('');
  const [end, setEnd] = useState('');

  // ── Data ─────────────────────────────────────────────────────────────
  const [report, setReport] = useState<DashboardReport | null>(null);
  const [insights, setInsights] = useState<InsightPayload | null>(null);
  const [htmlPresent, setHtmlPresent] = useState(false);
  const [tagFilter, setTagFilter] = useState<string | null>(null);
  const [expanded, setExpanded] = useState<string | null>(null);
  const [turnDetail, setTurnDetail] = useState<DashboardSessionDetail | null>(null);
  const [loading, setLoading] = useState(false);

  const loadAll = useCallback(async () => {
    if (!settings) return;
    setLoading(true);
    try {
      const jobs: Promise<void>[] = [];
      if (settings.collect) {
        jobs.push(
          getDashboardReport({
            period, date,
            ...(period === 'custom' ? { start, end } : {}),
            project: name,
          }).then(setReport).catch(() => setReport(null))
        );
      }
      jobs.push(fetchInsights(name, settings.insights_path).then(setInsights));
      if (settings.html_path) {
        jobs.push(
          api.raw(`/files/${name}/${settings.html_path}`)
            .then(() => setHtmlPresent(true))
            .catch(() => setHtmlPresent(false))
        );
      }
      await Promise.all(jobs);
    } finally {
      setLoading(false);
    }
  }, [settings, period, date, start, end, name]);

  // Load settings once; then loadAll whenever filter changes.
  useEffect(() => {
    let cancelled = false;
    getDashboardSettings()
      .then((s) => { if (!cancelled) setSettings(s); })
      .catch(() => { if (!cancelled) setSettings(null); })
      .finally(() => { if (!cancelled) setSettingsLoaded(true); });
    return () => { cancelled = true; };
  }, [name]);

  useEffect(() => { loadAll(); }, [loadAll]);

  // ── Merged session rows ──────────────────────────────────────────────
  const rows = useMemo<MergedRow[]>(() => {
    const out: MergedRow[] = [];
    const engineById = new Map<string, MergedRow>();
    const engineByAgentId = new Map<string, MergedRow>();

    (report?.topics || []).forEach((tp) => {
      const row: MergedRow = {
        key: `e:${tp.session_id}`,
        sessionId: tp.session_id,
        agentSessionId: tp.agent_session_id,
        title: tp.name || tp.session_id,
        platform: tp.platform,
        userName: tp.user_name,
        turns: tp.turns,
        totalTokens: tp.total_tokens,
        toolCalls: tp.tool_calls,
        activeMs: tp.active_ms,
        lastActive: tp.last_active,
        firstMessage: tp.first_message,
        business: false,
      };
      out.push(row);
      engineById.set(tp.session_id, row);
      if (tp.agent_session_id) engineByAgentId.set(tp.agent_session_id, row);
    });

    (insights?.sessions || []).forEach((s) => {
      const target =
        (s.session_id && engineById.get(s.session_id)) ||
        (s.agent_session_id && engineByAgentId.get(s.agent_session_id));
      if (target) {
        target.business = true;
        if (s.title) target.title = s.title;
        if (s.summary) target.summary = s.summary;
        if (s.metrics) target.metrics = s.metrics;
        if (s.tags) target.tags = s.tags;
        if (s.tone) target.tone = s.tone;
        if (s.detail) target.detail = s.detail;
      } else {
        out.push({
          key: `b:${s.agent_session_id || s.session_id || s.title}`,
          sessionId: s.session_id,
          agentSessionId: s.agent_session_id,
          title: s.title,
          turns: 0,
          totalTokens: 0,
          toolCalls: 0,
          activeMs: 0,
          summary: s.summary,
          metrics: s.metrics,
          tags: s.tags,
          tone: s.tone,
          detail: s.detail,
          business: true,
        });
      }
    });
    return out;
  }, [report, insights]);

  const visibleRows = useMemo(
    () => (tagFilter ? rows.filter((r) => (r.tags || []).some((tg) => insightTagText(tg) === tagFilter)) : rows),
    [rows, tagFilter]
  );

  // ── Turn drawer ──────────────────────────────────────────────────────
  const toggleExpand = useCallback(async (row: MergedRow) => {
    if (expanded === row.key) {
      setExpanded(null);
      return;
    }
    setExpanded(row.key);
    setTurnDetail(null);
    if (row.sessionId && settings?.collect) {
      try {
        setTurnDetail(await getDashboardSessionDetail(name, row.sessionId));
      } catch {
        setTurnDetail(null);
      }
    }
  }, [expanded, name, settings]);

  const iframeSrc = useMemo(() => {
    if (!htmlPresent || !token) return '';
    const params = new URLSearchParams({ token });
    params.set('period', period);
    if (period === 'custom' && start && end) {
      params.set('start', start);
      params.set('end', end);
    } else {
      params.set('date', date);
    }
    return `/api/v1/files/${name}/${settings?.html_path}?${params.toString()}`;
  }, [htmlPresent, token, period, date, start, end, name, settings]);

  const statsActive = !!settings?.collect && !!report;
  const insightsActive = !!insights && ((insights.sessions?.length || 0) > 0 || (insights.cards?.length || 0) > 0);

  if (!settingsLoaded) {
    return <div className="flex items-center justify-center h-64 text-gray-400"><Activity className="animate-pulse" size={24} /></div>;
  }

  // Presence-driven fallback: no settings → feature off → minimal page.
  if (!settings) {
    return (
      <div className="space-y-6 animate-fade-in">
        <div className="flex items-center justify-between">
          <h2 className="text-lg font-bold text-gray-900 dark:text-white">{name}</h2>
          <div className="flex gap-2">
            <Link to={`/chat/${name}`}><Button variant="primary" size="sm"><MessageSquare size={14} className="mr-1" />{t('overview.enterChat')}</Button></Link>
            <Link to={`/projects/${name}`}><Button variant="secondary" size="sm"><Settings size={14} className="mr-1" />{t('overview.projectSettings')}</Button></Link>
          </div>
        </div>
        <Card><EmptyState message={t('overview.statsOff')} icon={BarChart3} /></Card>
      </div>
    );
  }

  const totals = report?.totals;

  return (
    <div className="space-y-6 animate-fade-in">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <h2 className="text-lg font-bold text-gray-900 dark:text-white">{name}</h2>
          <span className="text-xs text-gray-400 font-mono">{report?.period.label}</span>
        </div>
        <div className="flex gap-2">
          <Button variant="ghost" size="sm" onClick={loadAll} title={t('common.refresh')}>
            <RefreshCw size={14} className={loading ? 'animate-spin' : ''} />
          </Button>
          <Link to={`/chat/${name}`}><Button variant="primary" size="sm"><MessageSquare size={14} className="mr-1" />{t('overview.enterChat')}</Button></Link>
          <Link to={`/projects/${name}`}><Button variant="secondary" size="sm"><Settings size={14} className="mr-1" />{t('overview.projectSettings')}</Button></Link>
        </div>
      </div>

      {/* Time filter (stats zone only) */}
      {settings.collect && (
        <div className="flex flex-wrap items-center gap-2">
          {(['day', 'week', 'month', 'custom'] as DashboardPeriodType[]).map((p) => (
            <button
              key={p}
              onClick={() => setPeriod(p)}
              className={cn(
                'px-3 py-1.5 rounded-lg text-xs font-medium border transition-all',
                period === p
                  ? 'border-accent/50 bg-accent/10 text-accent'
                  : 'border-gray-200 dark:border-white/[0.08] text-gray-500 dark:text-gray-400 hover:border-accent/30'
              )}
            >
              {t(`overview.period.${p}`)}
            </button>
          ))}
          {period === 'custom' ? (
            <div className="flex items-center gap-2 ml-2">
              <Input type="date" value={start} onChange={(e: React.ChangeEvent<HTMLInputElement>) => setStart(e.target.value)} className="w-36 text-xs" />
              <span className="text-gray-400 text-xs">~</span>
              <Input type="date" value={end} onChange={(e: React.ChangeEvent<HTMLInputElement>) => setEnd(e.target.value)} className="w-36 text-xs" />
            </div>
          ) : (
            <Input
              type="date"
              value={date}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => setDate(e.target.value)}
              className="w-36 text-xs ml-2"
            />
          )}
        </div>
      )}

      {/* Zone 1: engine stats */}
      {statsActive && totals && (
        <>
          <div className="grid grid-cols-2 lg:grid-cols-5 gap-3">
            <StatCard label={t('overview.sessions')} value={`${totals.sessions_active}`} accent />
            <StatCard label={t('overview.turns')} value={totals.turns} />
            <StatCard label={t('overview.tokens')} value={formatTokens(totals.total_tokens)} />
            {(totals.cache_read_tokens > 0 || totals.cache_write_tokens > 0) && (
              <StatCard label={t('overview.cache')} value={`${formatTokens(totals.cache_read_tokens)}/${formatTokens(totals.cache_write_tokens)}`} />
            )}
            <StatCard label={t('overview.errors')} value={totals.errors} />
          </div>

          {/* Timeline bars */}
          {report && report.timeline.length > 0 && (
            <Card>
              <p className="text-xs font-semibold text-gray-500 dark:text-gray-400 mb-3">{t('overview.activity')}</p>
              <div className="flex items-end gap-[2px] h-20">
                {report.timeline.map((b) => {
                  const max = Math.max(...report.timeline.map((x) => x.turns), 1);
                  const h = Math.max((b.turns / max) * 100, b.turns > 0 ? 8 : 2);
                  return (
                    <div key={b.label} className="flex-1 flex flex-col items-center group relative" title={`${b.label} · ${b.turns}`}>
                      <div className="w-full rounded-t bg-accent/60 group-hover:bg-accent transition-all" style={{ height: `${h}%` }} />
                      <span className="text-[8px] text-gray-400 mt-1 hidden lg:block">{b.label}</span>
                    </div>
                  );
                })}
              </div>
            </Card>
          )}
        </>
      )}

      {/* Zone 2: business insights (cards + merged rows) */}
      {insightsActive && (
        <section>
          <div className="flex items-center justify-between mb-3">
            <h3 className="text-sm font-semibold text-gray-900 dark:text-white flex items-center gap-1.5">
              <BarChart3 size={14} className="text-gray-400" />
              {t('overview.businessInsights')}
            </h3>
            {insights?.generated_at && (
              <span className="text-[10px] text-gray-400">
                {t('overview.coverage')} {insights.period?.start} ~ {insights.period?.end}
              </span>
            )}
          </div>

          {(insights?.cards?.length || 0) > 0 && (
            <div className="grid grid-cols-2 md:grid-cols-4 xl:grid-cols-6 gap-3 mb-4">
              {insights!.cards!.map((c, i) => (
                <StatCard key={i} label={c.label} value={`${c.value}${c.unit || ''}`} accent={c.tone === 'good'} />
              ))}
            </div>
          )}

          {tagFilter && (
            <div className="mb-3">
              <button onClick={() => setTagFilter(null)} className="text-xs text-accent hover:underline">
                {t('overview.clearFilter')}: {tagFilter} ✕
              </button>
            </div>
          )}
        </section>
      )}

      {/* Merged session list (engine rows + business rows) */}
      {(statsActive || insightsActive) && (
        <section>
          <h3 className="text-sm font-semibold text-gray-900 dark:text-white mb-3 flex items-center gap-1.5">
            <Activity size={14} className="text-gray-400" />
            {t('overview.sessionList')}
          </h3>
          {visibleRows.length === 0 ? (
            <Card><EmptyState message={t('overview.noData')} icon={Activity} /></Card>
          ) : (
            <div className="space-y-2">
              {visibleRows.map((row) => {
                const jumpable = !!row.sessionId;
                return (
                  <Card key={row.key} className={cn('p-4', jumpable && 'cursor-pointer hover:border-accent/40')} >
                    <div className="flex items-start gap-2" onClick={() => jumpable && navigate(`/chat/${name}/${row.sessionId}`)}>
                      <button
                        onClick={(e) => { e.stopPropagation(); toggleExpand(row); }}
                        className="mt-0.5 text-gray-400 hover:text-accent shrink-0"
                      >
                        {expanded === row.key ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                      </button>
                      <div className="min-w-0 flex-1">
                        <div className="flex flex-wrap items-center gap-1.5">
                          <span className={cn('w-2 h-2 rounded-full shrink-0', TAG_TONE_CLASSES[row.tone || ''] ? '' : 'bg-accent/70')} style={row.tone ? { backgroundColor: undefined } : undefined} />
                          <span className="text-sm font-medium text-gray-900 dark:text-white truncate">{row.title}</span>
                          {(row.tags || []).map((tg, i) => (
                            <TagChip key={i} tag={tg} onClick={(e: React.MouseEvent) => { e.stopPropagation(); setTagFilter(insightTagText(tg)); }} />
                          ))}
                          {row.platform && <Badge className="text-[10px]">{row.platform}</Badge>}
                          {row.userName && <span className="text-[10px] text-gray-400">{row.userName}</span>}
                        </div>
                        <div className="flex flex-wrap gap-x-4 gap-y-1 mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                          {row.turns > 0 && <span>{t('overview.turnCount')}: {row.turns}</span>}
                          {row.totalTokens > 0 && <span>{formatTokens(row.totalTokens)} tok</span>}
                          {row.toolCalls > 0 && <span>{row.toolCalls} tools</span>}
                          {row.activeMs > 0 && <span>{formatDuration(row.activeMs)}</span>}
                          {row.lastActive && <span>{new Date(row.lastActive).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</span>}
                        </div>
                        {row.summary && <p className="text-xs text-gray-500 dark:text-gray-400 mt-1.5">{row.summary}</p>}
                        {(row.metrics?.length || 0) > 0 && (
                          <div className="flex flex-wrap gap-x-4 gap-y-1 mt-1.5">
                            {row.metrics!.map((m, i) => (
                              <span key={i} className="text-[11px] text-gray-600 dark:text-gray-300">
                                {m.label}: <span className="font-medium">{m.value}{m.unit || ''}</span>
                              </span>
                            ))}
                          </div>
                        )}
                        {row.firstMessage && (
                          <p className="text-[11px] text-gray-400 mt-1.5 truncate">“{row.firstMessage}”</p>
                        )}
                        {row.detail && (
                          <a
                            href={`/api/v1/files/${name}/${row.detail}?token=${token}`}
                            target="_blank"
                            rel="noreferrer"
                            onClick={(e) => e.stopPropagation()}
                            className="inline-flex items-center gap-1 text-[11px] text-accent hover:underline mt-1.5"
                          >
                            {t('overview.detail')} <ExternalLink size={10} />
                          </a>
                        )}
                      </div>
                    </div>

                    {/* Turn-level drawer */}
                    {expanded === row.key && (
                      <div className="mt-3 pt-3 border-t border-gray-100 dark:border-white/[0.06]">
                        {!turnDetail ? (
                          <p className="text-xs text-gray-400">{t('overview.noTurnDetail')}</p>
                        ) : (
                          <div className="space-y-1.5 max-h-64 overflow-y-auto">
                            {turnDetail.turns.map((tr, i) => (
                              <div key={i} className="flex items-center gap-3 text-[11px] text-gray-500 dark:text-gray-400">
                                <span className="font-mono w-12 shrink-0">{new Date(tr.ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</span>
                                <span className="w-10 shrink-0">{tr.trigger === 'cron' ? 'cron' : 'user'}</span>
                                <span className="w-16 shrink-0">{formatDuration(tr.duration_ms)}</span>
                                <span className="w-20 shrink-0">{formatTokens(tr.input_tokens)}→{formatTokens(tr.output_tokens)}{tr.tokens_estimated ? '~' : ''}</span>
                                <span className="truncate">{tr.error ? `⚠ ${tr.error}` : `${tr.tool_calls} tools`}</span>
                              </div>
                            ))}
                          </div>
                        )}
                      </div>
                    )}
                  </Card>
                );
              })}
            </div>
          )}
        </section>
      )}

      {/* Zone 3: business HTML dashboard (fallback, iframe) */}
      {htmlPresent && iframeSrc && (
        <section>
          <h3 className="text-sm font-semibold text-gray-900 dark:text-white mb-3 flex items-center gap-1.5">
            <ExternalLink size={14} className="text-gray-400" />
            {t('overview.htmlDashboard')}
          </h3>
          <Card className="p-0 overflow-hidden">
            <iframe
              src={iframeSrc}
              title="business-dashboard"
              className="w-full border-0"
              style={{ height: '70vh' }}
              sandbox="allow-scripts allow-same-origin allow-popups"
            />
          </Card>
        </section>
      )}

      {/* Nothing at all */}
      {!statsActive && !insightsActive && !htmlPresent && (
        <Card><EmptyState message={t('overview.noData')} icon={BarChart3} /></Card>
      )}
    </div>
  );
}
