// Mermaid rendering, kept free of React so it can be unit-tested in the node
// environment. The React wrapper lives in components/MermaidBlock.tsx.
//
// The library is loaded lazily with a dynamic import: it is by far the biggest
// dependency in the bundle (mermaid + dagre/cytoscape/etc), and the vast
// majority of conversations contain no diagrams. A static import would make
// every page — including the anonymous share viewer — pay for it up front.
// Rollup turns the dynamic import into its own async chunk automatically.

export type MermaidTheme = 'light' | 'dark';

export type MermaidResult = { svg: string } | { error: string };

// Keyed by theme + source, so the same diagram reused across messages (or
// re-rendered on a streaming toggle) resolves from memory. FIFO-bounded: long
// conversations with many diagrams must not grow this without limit.
const cache = new Map<string, string>();
const MAX_CACHE = 80;

let mod: typeof import('mermaid').default | null = null;
let initedTheme: MermaidTheme | null = null;
let seq = 0;

function cacheKey(code: string, theme: MermaidTheme): string {
  return `${theme}\u0000${code}`;
}

async function getMermaid(theme: MermaidTheme) {
  if (!mod) mod = (await import('mermaid')).default;
  if (initedTheme !== theme) {
    // initialize() is process-global and re-callable; re-running it on a theme
    // change resets the config, so previously cached SVGs (rendered with the
    // other theme's colours) are no longer valid and are dropped.
    mod.initialize({
      startOnLoad: false,
      securityLevel: 'strict',
      theme: theme === 'dark' ? 'dark' : 'default',
      fontFamily: 'inherit',
    });
    initedTheme = theme;
    cache.clear();
  }
  return mod;
}

/**
 * Renders a mermaid diagram to an SVG string.
 *
 * Never throws: callers get `{ error }` so the block can fall back to showing
 * the source instead of taking down the surrounding message tree.
 */
export async function renderMermaid(code: string, theme: MermaidTheme): Promise<MermaidResult> {
  const key = cacheKey(code, theme);
  const hit = cache.get(key);
  if (hit) return { svg: hit };

  const mermaid = await getMermaid(theme);

  // Parse first. mermaid.render() appends an error diagram to the document when
  // it fails, which would leak a stray node next to our component; parse() only
  // validates and lets us bail before touching the DOM.
  try {
    await mermaid.parse(code);
  } catch (e) {
    return { error: (e as Error)?.message || 'Invalid mermaid syntax' };
  }

  try {
    // v11+ render() manages its own temporary element and resolves with the SVG
    // markup — no hidden container needs to be mounted. The id must be unique
    // per call: concurrent renders sharing an id collide inside the library.
    const { svg } = await mermaid.render(`mmd-${++seq}`, code);
    if (cache.size >= MAX_CACHE) {
      const oldest = cache.keys().next().value;
      if (oldest !== undefined) cache.delete(oldest);
    }
    cache.set(key, svg);
    return { svg };
  } catch (e) {
    return { error: (e as Error)?.message || 'Render failed' };
  }
}

export function clearMermaidCache(): void {
  cache.clear();
}
