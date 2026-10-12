import { createContext } from 'react';
import remarkGfm from 'remark-gfm';
import rehypeHighlight from 'rehype-highlight';

// Module-level plugin arrays shared by every <Markdown> call site. A fresh
// `[remarkGfm]` literal per render defeats react-markdown's internal
// memoization and re-runs the whole unified pipeline (parse + remark + rehype +
// serialize) on every render, which dominated main-thread time on long
// conversations. Keeping one identity for the process lifetime fixes that.
export const REMARK_PLUGINS = [remarkGfm];
export const REHYPE_PLUGINS = [rehypeHighlight];

// Whether the markdown currently being rendered is part of a still-streaming
// assistant reply. A mermaid fence that has not closed yet must NOT be handed
// to mermaid (it would parse-fail on every delta), so the code-block renderer
// reads this flag and falls back to plain source until the fence closes.
//
// Delivered via context rather than as a prop threaded through the `components`
// map: a components map is resolved per tag name only, so the flag would have to
// be baked into the map's closure — which would rebuild the map on every
// streaming toggle and break react-markdown's memoization. Context keeps the
// map identity constant.
export const StreamingContext = createContext(false);

// Styling for a fenced code block's <pre>. Shared by the chat code-block
// renderer and by MermaidBlock's plain-source fallback so a mermaid block that
// is not (or not yet) drawn as a diagram looks identical to any other block.
export const PRE_CLASS =
  'overflow-x-auto rounded-lg bg-[#fafafa] dark:bg-[#0d1117] border border-gray-200 dark:border-gray-700/60 p-4 pt-8 text-[13px] leading-[1.6] font-mono';
