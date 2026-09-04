import { useEffect, useState, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useSearchParams } from 'react-router-dom';
import { FileText, FileType2, ArrowLeft, ExternalLink, X } from 'lucide-react';
import { Card, Badge, Button, EmptyState } from '@/components/ui';
import { listReports, type ReportEntry } from '@/api/dashboard';
import { listProjects, type ProjectSummary } from '@/api/projects';
import { useAuthStore } from '@/store/auth';
import { formatTime } from '@/lib/utils';

// ── Report list ───────────────────────────────────────────────────────────

export function ReportsList() {
  const { t } = useTranslation();
  const [projects, setProjects] = useState<ProjectSummary[]>([]);
  const [project, setProject] = useState('');
  const [reports, setReports] = useState<ReportEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    listProjects()
      .then((r) => {
        const projs = r.projects || [];
        setProjects(projs);
        if (projs.length > 0) setProject((p) => p || projs[0].name);
      })
      .catch((e) => setError(e.message));
  }, []);

  const load = useCallback(async () => {
    if (!project) return;
    try {
      setLoading(true);
      setError('');
      const r = await listReports(project);
      setReports(r.reports || []);
    } catch (e: any) {
      setError(e.message);
      setReports([]);
    } finally {
      setLoading(false);
    }
  }, [project]);

  useEffect(() => { load(); }, [load]);

  return (
    <div className="space-y-6 animate-fade-in">
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-bold text-gray-900 dark:text-white">{t('reports.title')}</h2>
        <div className="flex gap-2">
          {projects.map((p) => (
            <button
              key={p.name}
              onClick={() => setProject(p.name)}
              className={`px-3 py-1.5 rounded-lg text-xs font-medium border transition-all ${
                project === p.name
                  ? 'border-accent/50 bg-accent/10 text-accent'
                  : 'border-gray-200 dark:border-white/[0.08] text-gray-500 dark:text-gray-400 hover:border-accent/30'
              }`}
            >
              {p.name}
            </button>
          ))}
        </div>
      </div>

      {loading && reports.length === 0 ? (
        <Card><p className="text-xs text-gray-400 text-center py-8">{t('common.loading')}</p></Card>
      ) : error ? (
        <Card><p className="text-xs text-red-500 text-center py-8">{error}</p></Card>
      ) : reports.length === 0 ? (
        <Card><EmptyState message={t('reports.noReports')} icon={FileText} /></Card>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-3">
          {reports.map((r) => (
            <Link
              key={r.path}
              to={`/reports/preview?project=${encodeURIComponent(project)}&path=${encodeURIComponent(r.path)}&url=${encodeURIComponent(r.url || '')}&format=${r.format}`}
              className="block"
            >
              <Card hover className="p-4">
                <div className="flex items-center gap-2 mb-2">
                  {r.format === 'html' ? <FileType2 size={14} className="text-accent" /> : <FileText size={14} className="text-accent" />}
                  <p className="text-sm font-medium text-gray-900 dark:text-white truncate flex-1">{r.title}</p>
                </div>
                <div className="flex flex-wrap gap-1.5 items-center">
                  {r.type && <Badge className="text-[10px]">{r.type}</Badge>}
                  {r.date && <Badge className="text-[10px]">{r.date}</Badge>}
                </div>
                <p className="text-[10px] text-gray-400 mt-2 font-mono truncate">{r.path}</p>
                <p className="text-[10px] text-gray-400 mt-1">{formatTime(new Date(r.mtime * 1000).toISOString())}</p>
              </Card>
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}

// ── Report preview (iframe for HTML, raw text for MD) ────────────────────

export function ReportPreview() {
  const { t } = useTranslation();
  const [params] = useSearchParams();
  const project = params.get('project') || '';
  const path = params.get('path') || '';
  const url = params.get('url') || '';
  const format = params.get('format') || 'html';
  const fullscreen = params.get('fullscreen') === '1';
  const token = useAuthStore((s) => s.token);
  const [mdText, setMdText] = useState<string | null>(null);
  const [error, setError] = useState('');

  // `url` (relative to the project work dir) is authoritative when present;
  // fall back to the legacy `path` so older entries still resolve.
  const filePath = url || path;
  const fileUrl = `/api/v1/files/${project}/${filePath}`;

  useEffect(() => {
    if (format !== 'md') return;
    import('@/api/client').then(({ api }) =>
      api.raw(`/files/${project}/${filePath}`)
        .then(setMdText)
        .catch((e) => setError(e.message))
    );
  }, [project, filePath, format]);

  if (fullscreen) {
    return (
      <div className="fixed inset-0 z-50 bg-white dark:bg-gray-950">
        <iframe
          src={`${fileUrl}?token=${token}`}
          title={path}
          className="w-full h-full border-0"
          sandbox="allow-scripts allow-same-origin allow-popups"
        />
        <Link
          to={`/reports/preview?project=${encodeURIComponent(project)}&path=${encodeURIComponent(path)}&format=${format}`}
          className="absolute top-3 right-3 p-2 rounded-lg bg-white/90 dark:bg-black/70 border border-gray-200 dark:border-white/10 text-gray-500 hover:text-accent"
        >
          <X size={16} />
        </Link>
      </div>
    );
  }

  return (
    <div className="space-y-4 animate-fade-in">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2 min-w-0">
          <Link to="/reports"><Button variant="ghost" size="sm"><ArrowLeft size={14} /></Button></Link>
          <h2 className="text-sm font-semibold text-gray-900 dark:text-white truncate">{path.split('/').pop()}</h2>
          <Badge className="text-[10px]">{project}</Badge>
        </div>
        <div className="flex gap-2">
          <a href={`${fileUrl}?token=${token}`} target="_blank" rel="noreferrer">
            <Button variant="secondary" size="sm"><ExternalLink size={14} className="mr-1" />{t('reports.openNew')}</Button>
          </a>
          {format === 'html' && (
            <Link to={`/reports/preview?project=${encodeURIComponent(project)}&path=${encodeURIComponent(path)}&format=html&fullscreen=1`}>
              <Button variant="primary" size="sm">{t('reports.fullscreen')}</Button>
            </Link>
          )}
        </div>
      </div>

      <Card className="p-0 overflow-hidden">
        {format === 'html' ? (
          <iframe
            src={`${fileUrl}?token=${token}`}
            title={path}
            className="w-full border-0"
            style={{ height: '75vh' }}
            sandbox="allow-scripts allow-same-origin allow-popups"
          />
        ) : error ? (
          <p className="text-xs text-red-500 p-8 text-center">{error}</p>
        ) : mdText === null ? (
          <p className="text-xs text-gray-400 p-8 text-center">{t('common.loading')}</p>
        ) : (
          <pre className="p-6 text-xs text-gray-700 dark:text-gray-300 whitespace-pre-wrap break-words max-h-[75vh] overflow-y-auto">{mdText}</pre>
        )}
      </Card>
    </div>
  );
}
