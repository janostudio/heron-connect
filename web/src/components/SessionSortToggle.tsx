import { useTranslation } from 'react-i18next';
import type { SessionSortMode } from '@/api/sessions';
import { cn } from '@/lib/utils';

// Both orders stay visible instead of being collapsed behind one icon: an icon
// that silently flips the list leaves the active order implicit, which is
// exactly the confusion this control removes.
//
// The two modes answer different questions, so each one carries its rule in the
// tooltip rather than the label — "reading order" alone does not say whether
// pinned conversations still come first.
const MODES: { mode: SessionSortMode; labelKey: string; hintKey: string }[] = [
  { mode: 'reading', labelKey: 'sessions.sortReading', hintKey: 'sessions.sortReadingHint' },
  { mode: 'activity', labelKey: 'sessions.sortActivity', hintKey: 'sessions.sortActivityHint' },
];

export default function SessionSortToggle({ value, onChange, className }: {
  value: SessionSortMode;
  onChange: (mode: SessionSortMode) => void;
  className?: string;
}) {
  const { t } = useTranslation();
  return (
    <div
      role="group"
      aria-label={t('sessions.sortOrder')}
      className={cn('flex items-center rounded-lg bg-gray-100 dark:bg-white/[0.06] p-0.5 shrink-0', className)}
    >
      {MODES.map(({ mode, labelKey, hintKey }) => (
        <button
          key={mode}
          type="button"
          onClick={() => onChange(mode)}
          aria-pressed={value === mode}
          title={t(hintKey)}
          className={cn(
            'px-1.5 py-0.5 rounded-md text-[10px] leading-4 whitespace-nowrap transition-colors',
            value === mode
              ? 'bg-white dark:bg-white/[0.14] text-gray-900 dark:text-white shadow-sm'
              : 'text-gray-400 hover:text-gray-600 dark:hover:text-gray-200',
          )}
        >
          {t(labelKey)}
        </button>
      ))}
    </div>
  );
}
