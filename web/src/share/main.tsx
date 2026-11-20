import React, { useEffect, useState } from 'react';
import ReactDOM from 'react-dom/client';
import { Download, FileText, Loader2, AlertCircle } from 'lucide-react';
import { RenderMarkdown } from '@/pages/Chat/markdownBlocks';
import TextPreview from '@/components/TextPreview';
import { readSharedPreviewChunk } from '@/api/files';
import {
  decidePreview, forcePreview, formatBytes, isMarkdown,
  type PreviewPlan,
} from '@/lib/filePreview';
import '../index.css';

// ── Public shared-file viewer ────────────────────────────────
//
// A standalone entry point (second Vite input) so this page does NOT pull in
// the admin SPA, its router, its auth store or its websocket. The recipient is
// anonymous by design: opening a share link must never bounce them to a login
// screen, and a viewer should not ship a megabyte of admin UI to read a file.
//
// The server stamps the token/name/type onto #share-root as data attributes
// (see management.serveSharePage) rather than inline JSON, so the page can run
// under a strict CSP — important, because the content it renders is agent
// output and must be treated as untrusted.
//
// Previewing mirrors the admin UI: probe the size, decide what the browser can
// lay out, and only then fetch. A share link to a huge file used to hang the
// recipient's tab — a bad way to spend someone else's CPU.

interface ShareMeta {
  token: string;
  name: string;
  contentType: string;
}

function readMeta(): ShareMeta | null {
  const root = document.getElementById('share-root');
  if (!root) return null;
  const token = root.getAttribute('data-share-token') || '';
  if (!token) return null;
  return {
    token,
    name: root.getAttribute('data-share-name') || 'file',
    contentType: root.getAttribute('data-share-type') || 'application/octet-stream',
  };
}

// rawPath points at the byte-serving branch of the share endpoint. ?raw=1 is
// required: without it the server would see this fetch's Accept header and
// could hand back the HTML page instead of the file.
const rawPath = (token: string) => `/share/${encodeURIComponent(token)}?raw=1`;
const rawURL = (token: string) => `/api/v1${rawPath(token)}`;
const downloadURL = (token: string) => `/api/v1/share/${encodeURIComponent(token)}?download=1`;

const LABELS = {
  loading: 'Loading…',
  loadMore: 'Load more',
  download: 'Download',
  failed: 'Failed to load file',
};

function Centered({ children }: { children: React.ReactNode }) {
  return (
    <div className="min-h-dvh flex items-center justify-center p-6 text-sm text-gray-500 dark:text-gray-400">
      {children}
    </div>
  );
}

function ShareViewer({ meta }: { meta: ShareMeta }) {
  const [phase, setPhase] = useState<'head' | 'ready' | 'error'>('head');
  const [plan, setPlan] = useState<PreviewPlan | null>(null);
  const [size, setSize] = useState(0);
  const [contentType, setContentType] = useState(meta.contentType);
  const [forced, setForced] = useState(false);
  const [text, setText] = useState<string | null>(null);
  const [objectUrl, setObjectUrl] = useState('');
  const [error, setError] = useState('');

  const { name, token } = meta;

  // Probe first: the size decides whether this page may render anything at all.
  useEffect(() => {
    let alive = true;
    setPhase('head');
    setError('');
    setPlan(null);
    setText(null);
    setObjectUrl('');
    setForced(false);

    fetch(rawURL(token), { method: 'HEAD', cache: 'no-store' })
      .then((res) => {
        if (!alive) return;
        if (!res.ok) {
          setError(res.status === 404 ? 'This link is no longer available.' : `Failed to load file (${res.status}).`);
          setPhase('error');
          return;
        }
        const len = Number(res.headers.get('Content-Length') || '0');
        const bytes = Number.isFinite(len) ? len : 0;
        const ct = res.headers.get('Content-Type') || meta.contentType;
        setSize(bytes);
        setContentType(ct);
        setPlan(decidePreview(name, ct, bytes));
        setPhase('ready');
      })
      .catch(() => {
        if (!alive) return;
        setError('Failed to load file.');
        setPhase('error');
      });

    return () => { alive = false; };
  }, [token, name, meta.contentType]);

  const effective = plan && forced ? forcePreview(plan) : plan;

  // Whole-document and binary paths fetch once; slices are pulled by the viewer.
  useEffect(() => {
    if (!effective) return;
    if (effective.mode !== 'full' && effective.mode !== 'object-url') return;

    let alive = true;
    let url = '';
    fetch(rawURL(token), { cache: 'no-store' })
      .then(async (res) => {
        if (!alive) return;
        if (!res.ok) {
          setError(res.status === 404 ? 'This link is no longer available.' : `Failed to load file (${res.status}).`);
          setPhase('error');
          return;
        }
        if (effective.mode === 'full') {
          const body = await res.text();
          if (!alive) return;
          setText(body);
        } else {
          // A blob URL, unlike a data: URL, does not duplicate the bytes as a
          // base64 string. It also keeps the strict CSP (no remote img origins).
          url = URL.createObjectURL(await res.blob());
          if (!alive) {
            URL.revokeObjectURL(url);
            return;
          }
          setObjectUrl(url);
        }
      })
      .catch(() => {
        if (!alive) return;
        setError('Failed to load file.');
        setPhase('error');
      });

    return () => {
      alive = false;
      if (url) URL.revokeObjectURL(url);
    };
  }, [effective, token]);

  const header = (
    <header className="sticky top-0 z-10 flex items-center gap-3 px-4 py-3 border-b border-gray-200 dark:border-gray-800 bg-white/90 dark:bg-gray-900/90 backdrop-blur">
      <FileText size={16} className="shrink-0 text-gray-400" />
      <span className="flex-1 min-w-0 truncate text-sm font-medium text-gray-800 dark:text-gray-100" title={name}>
        {name}
      </span>
      <a
        href={downloadURL(token)}
        className="shrink-0 flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-accent text-black hover:bg-accent-dim transition-colors"
      >
        <Download size={14} /> Download
      </a>
    </header>
  );

  let body: React.ReactNode;
  if (phase === 'head') {
    body = <Centered><Loader2 size={18} className="animate-spin" /></Centered>;
  } else if (phase === 'error') {
    body = (
      <Centered>
        <div className="flex items-center gap-2 text-red-600 dark:text-red-400">
          <AlertCircle size={16} /> {error}
        </div>
      </Centered>
    );
  } else if (effective && effective.mode === 'download-only') {
    body = (
      <Centered>
        <div className="text-center space-y-3">
          <p>
            {effective.reason === 'unsupported'
              ? 'This file type can’t be previewed.'
              : `This file is ${formatBytes(size)} — too large to open in the preview.`}
          </p>
          <div className="flex items-center justify-center gap-2">
            <a
              href={downloadURL(token)}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-accent text-black hover:bg-accent-dim transition-colors"
            >
              <Download size={14} /> Download
            </a>
            {plan && forcePreview(plan).mode !== 'download-only' && (
              <button
                type="button"
                onClick={() => setForced(true)}
                className="rounded-lg border border-gray-200 px-3 py-1.5 text-xs text-gray-600 hover:bg-gray-100 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-white/[0.06]"
              >
                Preview anyway
              </button>
            )}
          </div>
        </div>
      </Centered>
    );
  } else if (effective && effective.mode === 'windowed') {
    body = (
      <div className="h-[calc(100dvh-3.25rem)] px-4 py-4">
        <TextPreview
          totalSize={size}
          readChunk={(offset, limit, signal) => readSharedPreviewChunk(rawPath(token), offset, limit, signal)}
          labels={LABELS}
          onDownload={() => { window.location.href = downloadURL(token); }}
          className="h-full"
        />
      </div>
    );
  } else if (effective?.mode === 'full' && text !== null) {
    body = isMarkdown(name, contentType) ? (
      <article className="max-w-3xl mx-auto px-4 py-6">
        <RenderMarkdown content={text} />
      </article>
    ) : effective.kind === 'html' ? (
      // Small HTML renders from srcDoc; the source toggle lives in the admin UI,
      // not here. Larger documents are handed over as a blob URL below.
      <iframe
        srcDoc={text}
        title={name}
        sandbox="allow-scripts allow-popups allow-forms"
        className="w-full h-[calc(100dvh-3.25rem)] bg-white"
      />
    ) : (
      <pre className="max-w-5xl mx-auto my-6 px-4 text-[13px] leading-[1.6] font-mono whitespace-pre-wrap break-words text-gray-800 dark:text-gray-100">
        {text}
      </pre>
    );
  } else if (effective?.mode === 'object-url' && objectUrl) {
    body = (
      <div className="max-w-4xl mx-auto p-6">
        {effective.kind === 'html' && (
          <iframe src={objectUrl} title={name} sandbox="allow-scripts allow-popups allow-forms" className="w-full h-[calc(100dvh-6rem)] rounded-lg border border-gray-200 dark:border-gray-700 bg-white" />
        )}
        {effective.kind === 'image' && (
          <img src={objectUrl} alt={name} className="max-w-full mx-auto rounded-lg border border-gray-200 dark:border-gray-700" />
        )}
        {effective.kind === 'pdf' && (
          <iframe src={objectUrl} title={name} className="w-full h-[80vh] rounded-lg border border-gray-200 dark:border-gray-700" />
        )}
        {effective.kind === 'audio' && <audio controls src={objectUrl} className="w-full" />}
        {effective.kind === 'video' && (
          <video controls src={objectUrl} className="max-w-full mx-auto rounded-lg" />
        )}
      </div>
    );
  } else if (effective?.mode === 'full') {
    // The body is still in flight.
    body = <Centered><Loader2 size={18} className="animate-spin" /></Centered>;
  } else {
    body = (
      <Centered>
        <div className="text-center space-y-3">
          <p>This file type can’t be previewed.</p>
          <a
            href={downloadURL(token)}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-accent text-black hover:bg-accent-dim transition-colors"
          >
            <Download size={14} /> Download
          </a>
        </div>
      </Centered>
    );
  }

  return (
    <div className="min-h-dvh">
      {header}
      {body}
    </div>
  );
}

function Root() {
  const [meta] = useState<ShareMeta | null>(() => readMeta());
  if (!meta) {
    return (
      <Centered>
        <div className="flex items-center gap-2">
          <AlertCircle size={16} /> This link is missing its share token.
        </div>
      </Centered>
    );
  }
  return <ShareViewer meta={meta} />;
}

const container = document.getElementById('share-root');
if (container) {
  ReactDOM.createRoot(container).render(
    <React.StrictMode>
      <Root />
    </React.StrictMode>,
  );
}
