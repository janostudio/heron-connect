import { memo, useEffect, useState } from 'react';
import { Code2, Eye } from 'lucide-react';
import { useThemeStore } from '@/store/theme';
import { renderMermaid } from '@/lib/mermaidRenderer';
import { CopyButton } from '@/components/CopyButton';
import { PRE_CLASS } from '@/components/markdownConfig';

// Plain-source view for a mermaid block: shown while the fence is still
// streaming, when mermaid rejects the syntax, and when the reader toggles the
// diagram back to its source. Uses the same <pre> styling and copy button as
// any other fenced code block.
function FallbackSource({ code }: { code: string }) {
  return (
    <div className="not-prose relative group my-4">
      <div className="absolute top-0 left-0 px-2.5 py-1 text-[10px] font-medium uppercase tracking-wider text-gray-400 dark:text-gray-500 bg-gray-100 dark:bg-gray-800 rounded-tl-lg rounded-br-lg border-b border-r border-gray-200 dark:border-gray-700 font-mono">
        mermaid
      </div>
      <CopyButton code={code} />
      <pre className={PRE_CLASS}>
        <code>{code}</code>
      </pre>
    </div>
  );
}

type RenderState =
  | { status: 'idle' }
  | { status: 'ok'; svg: string }
  | { status: 'err'; error: string };

function MermaidBlockInner({ code, streaming }: { code: string; streaming?: boolean }) {
  const theme = useThemeStore((s) => s.resolved);
  const [state, setState] = useState<RenderState>({ status: 'idle' });
  const [showSource, setShowSource] = useState(false);

  useEffect(() => {
    // While the reply is still streaming the fence may not have closed yet, so
    // the source we see is a truncated prefix that can never parse. Skip and
    // keep showing the source; once streaming ends this effect re-runs with the
    // complete block.
    if (streaming) return;
    const src = code.trim();
    if (!src) return;

    // Renders are async and the component can unmount or receive new source
    // mid-flight. mermaid has no abort API, so instead of cancelling we drop
    // the result when the render is no longer current.
    let alive = true;
    setState({ status: 'idle' });
    renderMermaid(src, theme).then((r) => {
      if (!alive) return;
      setState('svg' in r ? { status: 'ok', svg: r.svg } : { status: 'err', error: r.error });
    });
    return () => { alive = false; };
  }, [code, theme, streaming]);

  if (streaming || state.status === 'idle') return <FallbackSource code={code} />;

  if (state.status === 'err') {
    return (
      <div>
        <div className="not-prose text-[11px] text-red-500 dark:text-red-400 px-1 pt-1 pb-0.5 font-mono">
          mermaid: {state.error}
        </div>
        <FallbackSource code={code} />
      </div>
    );
  }

  return (
    <div className="not-prose relative group my-4">
      <button
        type="button"
        onClick={() => setShowSource((v) => !v)}
        title={showSource ? 'Show diagram' : 'Show source'}
        className="absolute top-2 right-2 p-1.5 rounded-md bg-gray-200/80 dark:bg-gray-700/80 hover:bg-gray-300 dark:hover:bg-gray-600 text-gray-500 dark:text-gray-400 opacity-0 group-hover:opacity-100 transition-opacity z-10"
      >
        {showSource ? <Eye size={12} /> : <Code2 size={12} />}
      </button>
      {showSource
        ? <FallbackSource code={code} />
        : (
          // The SVG markup comes from mermaid running in this same process with
          // securityLevel 'strict', which escapes diagram labels into <text>
          // and strips scripts and event handlers. An SVG string cannot be
          // expressed as a React element tree, so innerHTML is the only way to
          // mount it. Every non-success path (streaming, parse error, render
          // error) shows the plain source instead — nothing unvalidated is ever
          // injected.
          <div className="mmd-svg" dangerouslySetInnerHTML={{ __html: state.svg }} />
        )}
    </div>
  );
}

export const MermaidBlock = memo(MermaidBlockInner);
