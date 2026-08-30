import { Check } from 'lucide-react';
import { cn } from '@/lib/utils';

export interface SelectListOption {
  value: string;
  text: string;
}

// Custom scrollable select list. Replaces the native <select> element in
// bridge cards (e.g. /model): the native popup layer is not reliably
// scrollable on mobile webviews and its onChange fires as soon as scrolling
// passes over an option, which aborts the list. This renders an inline list
// with its own overflow container and explicit click-to-select.
export default function SelectList({
  options, value, onChange,
}: {
  options: SelectListOption[];
  value?: string;
  onChange: (v: string) => void;
}) {
  return (
    <div className="rounded-lg border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800/80 overflow-y-auto max-h-64 divide-y divide-gray-100 dark:divide-white/[0.04]">
      {options.map((opt) => {
        const active = opt.value === value;
        return (
          <button
            key={opt.value}
            type="button"
            onClick={() => onChange(opt.value)}
            className={cn(
              'w-full flex items-center justify-between gap-2 px-3 py-2 text-sm text-left transition-colors',
              active
                ? 'bg-accent/10 text-accent font-medium'
                : 'text-gray-700 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-white/[0.04]',
            )}
          >
            <span className="truncate">{opt.text}</span>
            {active && <Check size={14} className="shrink-0" />}
          </button>
        );
      })}
    </div>
  );
}
