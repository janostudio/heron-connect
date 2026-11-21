import { useEffect, useState, useCallback, useMemo, useRef, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useNavigate } from 'react-router-dom';
import { MessageSquare, Circle, Filter, Search, Plus, User, Loader2, Clock, Pin, PinOff, Pencil, ExternalLink } from 'lucide-react';
import { Badge, Button, EmptyState, Input, Modal } from '@/components/ui';
import { listProjects, type ProjectSummary } from '@/api/projects';
import { listSessions, createSession, updateSession, sortSessions, sessionTitle, sessionSource, splitByOrigin, type Session, type SessionSortMode } from '@/api/sessions';
import { newConvKey } from '@/lib/webSessionKey';
import { reserveTab, openReservedTab } from '@/lib/newTab';
import { readSortMode, writeSortMode } from '@/lib/sessionSortMode';
import SessionSortToggle from '@/components/SessionSortToggle';
import { cn } from '@/lib/utils';
import { timeAgo } from '@/lib/timeAgo';
import RenameSessionModal from '@/pages/Chat/RenameSessionModal';

interface FlatSession extends Session {
  _project: string;
}

// STORAGE_SHOW_CRON remembers the toggle across visits: scheduled runs are
// noise for most people, so the default is hidden, but whoever wants them back
// should not have to re-enable the toggle on every page load.
// Cards rendered per page. Large enough that paging is unnoticeable in a
// normal project, small enough that hundreds of sessions do not all get laid
// out at once.
const PAGE_SIZE = 120;

const STORAGE_SHOW_CRON = 'cc.sessions.showCron';

function readShowCron(): boolean {
  try {
    return localStorage.getItem(STORAGE_SHOW_CRON) === '1';
  } catch {
    return false;
  }
}

// highlight wraps every case-insensitive occurrence of `query` in `text` with
// a <mark>, so a history-text hit visibly shows why it matched. Returns the
// plain string when the query is empty.
function highlight(text: string, query: string) {
  const q = query.trim();
  if (!q) return text;
  const lower = text.toLowerCase();
  const lq = q.toLowerCase();
  const parts: ReactNode[] = [];
  let from = 0;
  for (let at = lower.indexOf(lq); at !== -1; at = lower.indexOf(lq, from)) {
    if (at > from) parts.push(text.slice(from, at));
    parts.push(
      <mark key={at} className="bg-amber-200/70 dark:bg-amber-400/30 text-inherit rounded-sm">
        {text.slice(at, at + q.length)}
      </mark>,
    );
    from = at + q.length;
  }
  if (from < text.length) parts.push(text.slice(from));
  return parts;
}

export default function SessionList() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [allData, setAllData] = useState<{ project: string; sessions: Session[] }[]>([]);
  const [projects, setProjects] = useState<ProjectSummary[]>([]);
  const [selectedProject, setSelectedProject] = useState<string>('');
  const [search, setSearch] = useState('');
  const [showCron, setShowCron] = useState<boolean>(readShowCron);
  // Same preference as the chat drawer (lib/sessionSortMode.ts): the two lists
  // show the same sessions, so they must not disagree about the order.
  const [sortMode, setSortMode] = useState<SessionSortMode>(readSortMode);
  const [loading, setLoading] = useState(true);

  // Latest debounced query, read by the poll / refresh paths (which must not
  // be re-created every keystroke). Kept in a ref so fetchData stays stable.
  const searchRef = useRef('');

  // New session modal state
  const [showCreate, setShowCreate] = useState(false);
  const [newProject, setNewProject] = useState('');
  const [newName, setNewName] = useState('');
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState('');

  // Rename modal state
  const [renameTarget, setRenameTarget] = useState<FlatSession | null>(null);

  const fetchData = useCallback(async (q: string) => {
    setLoading(true);
    try {
      const { projects: projs } = await listProjects();
      setProjects(projs || []);
      const results = await Promise.all(
        (projs || []).map(async (p) => {
          try {
            const { sessions } = await listSessions(p.name, q || undefined);
            return { project: p.name, sessions: sessions || [] };
          } catch {
            return { project: p.name, sessions: [] };
          }
        })
      );
      setAllData(results);
    } finally {
      setLoading(false);
    }
  }, []);

  // Toggle pin on a session, then refetch.
  const togglePin = useCallback(async (s: FlatSession) => {
    const next = !s.pinned;
    try {
      await updateSession(s._project, s.id, { pinned: next });
      await fetchData(searchRef.current);
    } catch { /* transient */ }
  }, [fetchData]);

  useEffect(() => {
    fetchData('');
    const handler = () => fetchData(searchRef.current);
    window.addEventListener('cc:refresh', handler);
    return () => window.removeEventListener('cc:refresh', handler);
  }, [fetchData]);

  // Debounce the search box into the backend full-text query. The list endpoint
  // is polled, so a full history scan must never run on every keystroke.
  useEffect(() => {
    const q = search.trim();
    searchRef.current = q;
    const timer = setTimeout(() => { fetchData(q); }, 250);
    return () => clearTimeout(timer);
  }, [search, fetchData]);

  // Poll so execution-status badges (running / waiting permission) stay
  // current while sessions run in parallel. Carries the active query along so
  // a poll tick doesn't silently drop the search filter.
  useEffect(() => {
    const timer = setInterval(() => fetchData(searchRef.current), 5000);
    return () => clearInterval(timer);
  }, [fetchData]);

  // Scheduled runs are split out of the human list: a job with
  // session_mode=new_per_run leaves one isolated session behind per run, so a
  // daily job alone would fill the top of the list (they sort by run time).
  const { human, cron } = useMemo(() => {
    const src = selectedProject
      ? allData.filter((d) => d.project === selectedProject)
      : allData;
    const flat = src.flatMap((d) => d.sessions.map((s) => ({ ...s, _project: d.project })));
    return splitByOrigin(flat);
  }, [allData, selectedProject]);

  // Projects with a single platform do not need a per-card platform badge: the
  // value would be identical on every card of that project.
  const multiPlatformProjects = useMemo(
    () => new Set(projects.filter((p) => (p.platforms?.length ?? 0) > 1).map((p) => p.name)),
    [projects],
  );

  const filtered = useMemo<FlatSession[]>(
    () => sortSessions(showCron ? [...human, ...cron] : human, sortMode),
    [human, cron, showCron, sortMode],
  );

  const changeSortMode = (mode: SessionSortMode) => {
    writeSortMode(mode);
    setSortMode(mode);
  };

  // Paged rendering: `filtered` stays the full result (the counter and the
  // empty state use it), `visible` is what actually reaches the DOM.
  const [visibleCount, setVisibleCount] = useState(PAGE_SIZE);
  useEffect(() => {
    setVisibleCount(PAGE_SIZE);
  }, [search, selectedProject, showCron]);
  const visible = useMemo(() => filtered.slice(0, visibleCount), [filtered, visibleCount]);
  const toggleShowCron = (next: boolean) => {
    setShowCron(next);
    try {
      localStorage.setItem(STORAGE_SHOW_CRON, next ? '1' : '0');
    } catch {
      /* private mode / storage disabled: keep the in-memory choice only */
    }
  };

  const openCreateModal = () => {
    setNewProject(selectedProject || projects[0]?.name || '');
    setNewName('');
    setCreateError('');
    setShowCreate(true);
  };

  // openInNewTab creates the conversation in its own tab and leaves this list
  // where it is, so the new conversation can run while the user keeps browsing.
  // The blank tab is opened synchronously inside the click: once the request
  // below is awaited there is no user gesture left, and browsers block it.
  const handleCreate = async (openInNewTab = false) => {
    if (!newProject) return;
    const newTab = openInNewTab ? reserveTab() : null;
    setCreating(true);
    setCreateError('');
    let sessionId = '';
    try {
      const sessionKey = newConvKey(newProject);
      const res = await createSession(newProject, { session_key: sessionKey, name: newName.trim() || undefined });
      sessionId = res.id;
    } catch (e: any) {
      // Nothing was created, so the tab opened above has nothing to show.
      newTab?.close();
      setCreateError(e.message || String(e));
      return;
    } finally {
      setCreating(false);
    }
    setShowCreate(false);
    const path = `/chat/${newProject}/${sessionId}`;
    // The current tab was asked for, or the popup was blocked — navigating
    // here is what keeps the just-created session reachable.
    if (!openReservedTab(newTab, path)) navigate(path);
  };

  if (loading && allData.length === 0) {
    return <div className="flex items-center justify-center h-64 text-gray-400 animate-pulse">{t('common.loading')}</div>;
  }

  return (
    <div className="space-y-4 animate-fade-in ">
      {/* Two semantic rows. One row used to carry six controls, so on narrow
          screens it folded unpredictably and the create button moved around.
          Row 1 answers "what am I looking at", row 2 "what is filtered out". */}
      <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-3">
        <Filter size={16} className="text-gray-400 shrink-0" />
        <select
          value={selectedProject}
          onChange={(e) => setSelectedProject(e.target.value)}
          className="px-3 py-1.5 text-sm rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-800 text-gray-900 dark:text-white focus:outline-none focus:ring-2 focus:ring-accent/50"
        >
          <option value="">{t('sessions.allProjects')}</option>
          {projects.map((p) => (
            <option key={p.name} value={p.name}>{p.name}</option>
          ))}
        </select>

        <div className="relative flex-1 min-w-[180px] max-w-sm">
          <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
          <input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={t('sessions.searchPlaceholder')}
            className="w-full pl-9 pr-3 py-1.5 text-sm rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-800 text-gray-900 dark:text-white focus:outline-none focus:ring-2 focus:ring-accent/50 placeholder:text-gray-400"
          />
        </div>

        <SessionSortToggle value={sortMode} onChange={changeSortMode} />

        <Button size="sm" onClick={openCreateModal} className="ml-auto">
          <Plus size={14} /> {t('sessions.createNew')}
        </Button>
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <label className="flex items-center gap-1.5 text-xs text-gray-500 dark:text-gray-400 cursor-pointer select-none">
          <input
            type="checkbox"
            checked={showCron}
            onChange={(e) => toggleShowCron(e.target.checked)}
            className="w-3.5 h-3.5 accent-current cursor-pointer"
          />
          {/* The count rides on the toggle instead of a separate "N hidden"
              clause at the end of the row: one short label says both what the
              checkbox does and how many sessions it covers. The count is the
              total number of scheduled-run sessions, so it does not flip to
              zero the moment the box is ticked. */}
          {cron.length > 0
            ? t('sessions.showCronSessions', { count: cron.length })
            : t('sessions.showCronSessionsNone')}
        </label>

        <span className="text-xs text-gray-400">
          {filtered.length} {t('nav.sessions').toLowerCase()}
        </span>
      </div>
      </div>

      {filtered.length === 0 ? (
        <EmptyState message={t('sessions.noSessions')} icon={MessageSquare} />
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4 gap-3">
          {visible.map((s) => {
            const title = sessionTitle(s);
            const source = sessionSource(s);
            return (
            <div key={`${s._project}-${s.id}`} className={cn(
              'group relative rounded-xl border p-4 transition-all duration-200 h-full',
              // No backdrop-blur: with hundreds of cards the blur is a real
              // paint cost and the difference is invisible at this opacity.
              'bg-white/60 dark:bg-white/[0.03]',
              'border-gray-200/80 dark:border-white/[0.06]',
              'hover:border-accent/40 hover:shadow-md hover:shadow-accent/5 hover:-translate-y-0.5',
              // Let the browser skip layout+paint for cards scrolled out of
              // view; the intrinsic size keeps the scrollbar honest.
              '[content-visibility:auto] [contain-intrinsic-size:auto_11rem]',
            )}>
              {/* One link covers the whole card instead of wrapping the action
                  buttons: interactive elements must not nest, so the card is a
                  single tab stop and the buttons come after it. */}
              <Link
                to={`/chat/${s._project}/${s.id}`}
                aria-label={title}
                className="absolute inset-0 z-0 rounded-xl focus:outline-none focus-visible:ring-2 focus-visible:ring-accent/50"
              />
              <div className="relative z-10 pointer-events-none">
                {/* Top: name + time */}
                <div className="flex items-start justify-between gap-2 mb-2">
                  <div className="flex items-center gap-1.5 min-w-0">
                    <MessageSquare size={14} className={s.live ? 'text-accent shrink-0' : 'text-gray-400 shrink-0'} />
                    <span className="text-sm font-medium text-gray-900 dark:text-white truncate">
                      {title}
                    </span>
                    {s.live && <Circle size={5} className="fill-emerald-500 text-emerald-500 shrink-0" />}
                    {s.waiting_permission ? (
                      <Badge variant="warning" className="text-[9px] px-1 py-0 gap-0.5 shrink-0">
                        <Clock size={8} /> {t('sessions.waitingPermission')}
                      </Badge>
                    ) : s.running ? (
                      <Badge variant="success" className="text-[9px] px-1 py-0 gap-0.5 shrink-0">
                        <Loader2 size={8} className="animate-spin" /> {t('sessions.running')}
                      </Badge>
                    ) : null}
                  </div>
                  <div
                    className="flex items-center gap-1 shrink-0 pointer-events-auto"
                    onClick={(e) => { e.preventDefault(); e.stopPropagation(); }}
                  >
                    <button
                      type="button"
                      onClick={() => setRenameTarget(s)}
                      className="p-1 rounded text-gray-400 hover:text-gray-600 dark:hover:text-gray-200 hover:bg-gray-100 dark:hover:bg-white/[0.08] opacity-100 md:opacity-0 md:group-hover:opacity-100 transition-opacity"
                      title={t('sessions.rename')}
                      aria-label={t('sessions.rename')}
                    >
                      <Pencil size={12} />
                    </button>
                    <button
                      type="button"
                      onClick={() => togglePin(s)}
                      className={cn(
                        'p-1 rounded transition-colors',
                        s.pinned ? 'text-accent' : 'text-gray-400 hover:text-gray-600 dark:hover:text-gray-200 opacity-100 md:opacity-0 md:group-hover:opacity-100',
                      )}
                      title={s.pinned ? t('sessions.unpin') : t('sessions.pin')}
                      aria-label={s.pinned ? t('sessions.unpin') : t('sessions.pin')}
                    >
                      {s.pinned ? <Pin size={12} /> : <PinOff size={12} />}
                    </button>
                    <span className="text-[10px] text-gray-400 mt-0.5">
                      {timeAgo(s.last_user_at || s.updated_at || s.created_at, t)}
                    </span>
                  </div>
                </div>

                {/* Preview: a history-match excerpt when searching content,
                    otherwise the session's FIRST user message — so the card
                    says what the session is about rather than echoing the
                    latest line (often an assistant reply or tool output). */}
                <div className="mb-2.5 min-h-[2.5rem]">
                  {s.match_snippet ? (
                    <p className="text-xs text-gray-500 dark:text-gray-400 line-clamp-2 leading-relaxed">
                      <Search size={10} className="inline mr-1 -mt-0.5 opacity-60" />
                      {highlight(s.match_snippet, search)}
                    </p>
                  ) : s.first_user_message ? (
                    <p className="text-xs text-gray-500 dark:text-gray-400 line-clamp-2 leading-relaxed">
                      <User size={10} className="inline mr-1 -mt-0.5 opacity-60" />
                      {highlight(s.first_user_message.replace(/\n/g, ' ').slice(0, 100), search)}
                    </p>
                  ) : (
                    <p className="text-xs text-gray-400 dark:text-gray-500 italic">{t('sessions.noMessages')}</p>
                  )}
                </div>

                {/* Bottom: badges + count */}
                <div className="flex items-center gap-1.5 flex-wrap">
                  {/* Project name only carries information in the all-projects
                      view; in a single-project view it repeats the filter.
                      Platform is only worth a badge when the project actually
                      has more than one attached. */}
                  {selectedProject === '' && <Badge>{s._project}</Badge>}
                  {s.platform && multiPlatformProjects.has(s._project) && (
                    <Badge variant="info">{s.platform}</Badge>
                  )}
                  {source && source !== title && source !== s.platform && (
                    <Badge variant="info">{source}</Badge>
                  )}
                  <span className="text-[10px] text-gray-400 ml-auto">{t('sessions.messageCount', { count: s.history_count })}</span>
                </div>
              </div>
            </div>
            );
          })}
        </div>
      )}

      {/* Paging control. Only appears when the result actually overflows the
          first page, so small projects never see it. */}
      {filtered.length > visible.length && (
        <div className="flex justify-center">
          <Button variant="secondary" size="sm" onClick={() => setVisibleCount((c) => c + PAGE_SIZE)}>
            {t('sessions.loadMore', { count: filtered.length - visible.length })}
          </Button>
        </div>
      )}

      {/* Create session modal */}
      <Modal open={showCreate} onClose={() => setShowCreate(false)} title={t('sessions.createNew')}>
        <div className="space-y-4 py-2">
          <div>
            <label className="block text-xs font-medium text-gray-600 dark:text-gray-400 mb-1">
              {t('sessions.selectProject')}
            </label>
            <select
              value={newProject}
              onChange={(e) => setNewProject(e.target.value)}
              className="w-full px-3 py-2 text-sm rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-800 text-gray-900 dark:text-white focus:outline-none focus:ring-2 focus:ring-accent/50"
            >
              {projects.map((p) => (
                <option key={p.name} value={p.name}>{p.name}</option>
              ))}
            </select>
          </div>
          <Input
            label={t('sessions.sessionNameOptional')}
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            placeholder={t('sessions.title')}
            autoFocus
          />
          {createError && <p className="text-xs text-red-500">{createError}</p>}
          <div className="flex flex-wrap justify-end gap-2 pt-2">
            <Button variant="secondary" onClick={() => setShowCreate(false)}>{t('common.cancel')}</Button>
            <Button
              variant="secondary"
              disabled={!newProject || creating}
              onClick={() => handleCreate(true)}
              title={t('sessions.openInNewTab')}
            >
              <ExternalLink size={14} /> {t('sessions.openInNewTab')}
            </Button>
            <Button disabled={!newProject || creating} loading={creating} onClick={() => handleCreate()}>
              {t('sessions.createNew')}
            </Button>
          </div>
        </div>
      </Modal>

      {/* Rename session modal */}
      {renameTarget && (
        <RenameSessionModal
          open
          project={renameTarget._project}
          session={{ id: renameTarget.id, name: renameTarget.name }}
          onClose={() => setRenameTarget(null)}
          onSaved={async () => { await fetchData(searchRef.current); }}
        />
      )}
    </div>
  );
}
