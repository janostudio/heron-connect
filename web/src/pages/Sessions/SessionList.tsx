import { useEffect, useState, useCallback, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useNavigate } from 'react-router-dom';
import { MessageSquare, Circle, Filter, Search, Plus, User, Bot, Loader2, Clock, Pin, PinOff, Pencil } from 'lucide-react';
import { Badge, Button, EmptyState, Input, Modal } from '@/components/ui';
import { listProjects, type ProjectSummary } from '@/api/projects';
import { listSessions, createSession, updateSession, sortSessions, sessionTitle, sessionSource, type Session } from '@/api/sessions';
import { newConvKey } from '@/lib/webSessionKey';
import { cn } from '@/lib/utils';
import RenameSessionModal from '@/pages/Chat/RenameSessionModal';

interface FlatSession extends Session {
  _project: string;
}

function timeAgo(iso: string, t: (k: string) => string): string {
  if (!iso) return '';
  const diff = Date.now() - new Date(iso).getTime();
  const mins = Math.floor(diff / 60000);
  if (mins < 1) return t('sessions.justNow');
  if (mins < 60) return `${mins}m`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h`;
  const days = Math.floor(hours / 24);
  return `${days}d`;
}

export default function SessionList() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [allData, setAllData] = useState<{ project: string; sessions: Session[] }[]>([]);
  const [projects, setProjects] = useState<ProjectSummary[]>([]);
  const [selectedProject, setSelectedProject] = useState<string>('');
  const [search, setSearch] = useState('');
  const [loading, setLoading] = useState(true);

  // New session modal state
  const [showCreate, setShowCreate] = useState(false);
  const [newProject, setNewProject] = useState('');
  const [newName, setNewName] = useState('');
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState('');

  // Rename modal state
  const [renameTarget, setRenameTarget] = useState<FlatSession | null>(null);

  const fetchData = useCallback(async () => {
    setLoading(true);
    try {
      const { projects: projs } = await listProjects();
      setProjects(projs || []);
      const results = await Promise.all(
        (projs || []).map(async (p) => {
          try {
            const { sessions } = await listSessions(p.name);
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
      await fetchData();
    } catch { /* transient */ }
  }, [fetchData]);

  useEffect(() => {
    fetchData();
    const handler = () => fetchData();
    window.addEventListener('cc:refresh', handler);
    return () => window.removeEventListener('cc:refresh', handler);
  }, [fetchData]);

  // Poll so execution-status badges (running / waiting permission) stay
  // current while sessions run in parallel.
  useEffect(() => {
    const timer = setInterval(fetchData, 5000);
    return () => clearInterval(timer);
  }, [fetchData]);

  const filtered = useMemo<FlatSession[]>(() => {
    const src = selectedProject
      ? allData.filter((d) => d.project === selectedProject)
      : allData;
    const flat = src.flatMap((d) => d.sessions.map((s) => ({ ...s, _project: d.project })));
    const query = search.trim().toLowerCase();
    const matched = query
      ? flat.filter((s) => {
          const haystack = [
            s.name, s.user_name, s.chat_name, s._project,
            s.last_message?.content,
          ].filter(Boolean).join(' ').toLowerCase();
          return haystack.includes(query);
        })
      : flat;
    return sortSessions(matched);
  }, [allData, selectedProject, search]);

  const openCreateModal = () => {
    setNewProject(selectedProject || projects[0]?.name || '');
    setNewName('');
    setCreateError('');
    setShowCreate(true);
  };

  const handleCreate = async () => {
    if (!newProject) return;
    setCreating(true);
    setCreateError('');
    try {
      const sessionKey = newConvKey(newProject);
      const res = await createSession(newProject, { session_key: sessionKey, name: newName.trim() || undefined });
      setShowCreate(false);
      navigate(`/chat/${newProject}/${res.id}`);
    } catch (e: any) {
      setCreateError(e.message || String(e));
    } finally {
      setCreating(false);
    }
  };

  if (loading && allData.length === 0) {
    return <div className="flex items-center justify-center h-64 text-gray-400 animate-pulse">Loading...</div>;
  }

  return (
    <div className="space-y-4 animate-fade-in ">
      {/* Filter + search bar */}
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

        <span className="text-xs text-gray-400">
          {filtered.length} {t('nav.sessions').toLowerCase()}
        </span>

        <Button size="sm" onClick={openCreateModal} className="ml-auto">
          <Plus size={14} /> {t('sessions.createNew')}
        </Button>
      </div>

      {filtered.length === 0 ? (
        <EmptyState message={t('sessions.noSessions')} icon={MessageSquare} />
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4 gap-3">
          {filtered.map((s) => {
            const title = sessionTitle(s);
            const source = sessionSource(s);
            return (
            <Link key={`${s._project}-${s.id}`} to={`/chat/${s._project}/${s.id}`}>
              <div className={cn(
                'group relative rounded-xl border p-4 transition-all duration-200 cursor-pointer h-full',
                'bg-white/60 dark:bg-white/[0.03] backdrop-blur-sm',
                'border-gray-200/80 dark:border-white/[0.06]',
                'hover:border-accent/40 hover:shadow-md hover:shadow-accent/5 hover:-translate-y-0.5',
              )}>
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
                    className="flex items-center gap-1 shrink-0"
                    onClick={(e) => { e.preventDefault(); e.stopPropagation(); }}
                  >
                    <button
                      type="button"
                      onClick={() => setRenameTarget(s)}
                      className="p-1 rounded text-gray-400 hover:text-gray-600 dark:hover:text-gray-200 hover:bg-gray-100 dark:hover:bg-white/[0.08] opacity-0 group-hover:opacity-100 transition-opacity"
                      title="重命名"
                    >
                      <Pencil size={12} />
                    </button>
                    <button
                      type="button"
                      onClick={() => togglePin(s)}
                      className={cn(
                        'p-1 rounded transition-colors',
                        s.pinned ? 'text-accent' : 'text-gray-400 hover:text-gray-600 dark:hover:text-gray-200 opacity-0 group-hover:opacity-100',
                      )}
                      title={s.pinned ? '取消置顶' : '置顶'}
                    >
                      {s.pinned ? <Pin size={12} /> : <PinOff size={12} />}
                    </button>
                    <span className="text-[10px] text-gray-400 mt-0.5">
                      {timeAgo(s.updated_at || s.created_at, t)}
                    </span>
                  </div>
                </div>

                {/* Last message preview */}
                <div className="mb-2.5 min-h-[2.5rem]">
                  {s.last_message ? (
                    <p className="text-xs text-gray-500 dark:text-gray-400 line-clamp-2 leading-relaxed">
                      {s.last_message.role === 'user' ? (
                        <User size={10} className="inline mr-1 -mt-0.5 opacity-60" />
                      ) : (
                        <Bot size={10} className="inline mr-1 -mt-0.5 opacity-60" />
                      )}
                      {s.last_message.content.replace(/\n/g, ' ').slice(0, 100)}
                    </p>
                  ) : (
                    <p className="text-xs text-gray-400 dark:text-gray-500 italic">{t('sessions.noMessages')}</p>
                  )}
                </div>

                {/* Bottom: badges + count */}
                <div className="flex items-center gap-1.5 flex-wrap">
                  <Badge>{s._project}</Badge>
                  {s.platform && <Badge variant="info">{s.platform}</Badge>}
                  {source && source !== title && <Badge variant="info">{source}</Badge>}
                  <span className="text-[10px] text-gray-400 ml-auto">{s.history_count} msgs</span>
                </div>
              </div>
            </Link>
            );
          })}
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
          <div className="flex justify-end gap-2 pt-2">
            <Button variant="secondary" onClick={() => setShowCreate(false)}>{t('common.cancel')}</Button>
            <Button disabled={!newProject || creating} loading={creating} onClick={handleCreate}>
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
          onSaved={async () => { await fetchData(); }}
        />
      )}
    </div>
  );
}
