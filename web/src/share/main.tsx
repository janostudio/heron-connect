import React, { useEffect, useState } from 'react';
import ReactDOM from 'react-dom/client';
import { Download, FileText, Loader2, AlertCircle } from 'lucide-react';
import { RenderMarkdown } from '@/pages/Chat/markdownBlocks';
import { fileIsPreviewable, isMarkdown, isHtmlFile } from '@/pages/Chat/chatHelpers';
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

// rawURL points at the byte-serving branch of the share endpoint. ?raw=1 is
// required: without it the server would see this fetch's Accept header and
// could hand back the HTML page instead of the file.
const rawURL = (token: string) => `/api/v1/share/${encodeURIComponent(token)}?raw=1`;
const downloadURL = (token: string) => `/api/v1/share/${encodeURIComponent(token)}?download=1`;

function Centered({ children }: { children: React.ReactNode }) {
  return (
    <div className="min-h-dvh flex items-center justify-center p-6 text-sm text-gray-500 dark:text-gray-400">
      {children}
    </div>
  );
}

function ShareViewer({ meta }: { meta: ShareMeta }) {
  const [text, setText] = useState<string | null>(null);
  const [objectUrl, setObjectUrl] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);

  const { name, contentType, token } = meta;
  const markdown = isMarkdown(name, contentType);
  const htmlFile = isHtmlFile(name, contentType);
  const previewable = fileIsPreviewable(name, contentType);
  // Markdown and other text are fetched as text so they can be rendered;
  // binary previews (images, pdf, media) go through a blob URL — which, unlike
  // a direct <img src>, also keeps the strict CSP (no remote img origins) intact.
  const asText =
    markdown ||
    (previewable &&
      !htmlFile &&
      !/^(image|audio|video)\//.test(contentType) &&
      contentType !== 'application/pdf');

  useEffect(() => {
    let alive = true;
    let url = '';
    fetch(rawURL(token), { cache: 'no-store' })
      .then(async (res) => {
        if (!alive) return;
        if (!res.ok) {
          setError(res.status === 404 ? 'This link is no longer available.' : `Failed to load file (${res.status}).`);
          setLoading(false);
          return;
        }
        if (asText) {
          const body = await res.text();
          if (!alive) return;
          setText(body);
        } else {
          const blob = await res.blob();
          if (!alive) return;
          url = URL.createObjectURL(blob);
          setObjectUrl(url);
        }
        setLoading(false);
      })
      .catch(() => {
        if (!alive) return;
        setError('Failed to load file.');
        setLoading(false);
      });

    return () => {
      alive = false;
      if (url) URL.revokeObjectURL(url);
    };
  }, [token, asText]);

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
  if (loading) {
    body = <Centered><Loader2 size={18} className="animate-spin" /></Centered>;
  } else if (error) {
    body = (
      <Centered>
        <div className="flex items-center gap-2 text-red-600 dark:text-red-400">
          <AlertCircle size={16} /> {error}
        </div>
      </Centered>
    );
  } else if (markdown && text !== null) {
    body = (
      <article className="max-w-3xl mx-auto px-4 py-6">
        <RenderMarkdown content={text} />
      </article>
    );
  } else if (htmlFile && objectUrl) {
    // Same sandbox stance as the in-app preview: scripts may run so charts and
    // inline JS work, but WITHOUT allow-same-origin the frame sits in an
    // opaque origin and cannot reach this origin's storage or APIs.
    body = (
      <iframe
        src={objectUrl}
        title={name}
        sandbox="allow-scripts allow-popups allow-forms"
        className="w-full h-[calc(100dvh-3.25rem)] bg-white"
      />
    );
  } else if (previewable && text !== null) {
    body = (
      <pre className="max-w-5xl mx-auto my-6 px-4 text-[13px] leading-[1.6] font-mono whitespace-pre-wrap break-words text-gray-800 dark:text-gray-100">
        {text}
      </pre>
    );
  } else if (objectUrl) {
    body = (
      <div className="max-w-4xl mx-auto p-6">
        {contentType.startsWith('image/') && (
          <img src={objectUrl} alt={name} className="max-w-full mx-auto rounded-lg border border-gray-200 dark:border-gray-700" />
        )}
        {contentType === 'application/pdf' && (
          <iframe src={objectUrl} title={name} className="w-full h-[80vh] rounded-lg border border-gray-200 dark:border-gray-700" />
        )}
        {contentType.startsWith('audio/') && <audio controls src={objectUrl} className="w-full" />}
        {contentType.startsWith('video/') && (
          <video controls src={objectUrl} className="max-w-full mx-auto rounded-lg" />
        )}
      </div>
    );
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
