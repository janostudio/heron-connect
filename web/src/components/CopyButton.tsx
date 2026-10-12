import { useState, memo } from 'react';
import { Copy, Check, X } from 'lucide-react';
import { copyText } from '@/lib/utils';

// Small copy-to-clipboard button overlaid on a code block. Own file (rather
// than living in markdownBlocks.tsx) because MermaidBlock renders the same
// button for its source fallback, and markdownBlocks imports MermaidBlock —
// keeping the button here avoids a circular import between the two.
function CopyButtonInner({ code }: { code: string }) {
  // null = idle, 'ok' = copied, 'fail' = copy attempt failed
  const [state, setState] = useState<'ok' | 'fail' | null>(null);
  const handleCopy = () => {
    copyText(code).then((ok) => {
      setState(ok ? 'ok' : 'fail');
      setTimeout(() => setState(null), 2000);
    });
  };
  return (
    <button
      onClick={handleCopy}
      className="absolute top-2 right-2 p-1.5 rounded-md bg-gray-200/80 dark:bg-gray-700/80 hover:bg-gray-300 dark:hover:bg-gray-600 text-gray-500 dark:text-gray-400 opacity-0 group-hover:opacity-100 transition-opacity z-10"
    >
      {state === 'ok' ? <Check size={12} /> : state === 'fail' ? <X size={12} className="text-red-500" /> : <Copy size={12} />}
    </button>
  );
}
export const CopyButton = memo(CopyButtonInner);
