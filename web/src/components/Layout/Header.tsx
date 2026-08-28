import { useTranslation } from 'react-i18next';
import { useState, useRef, useEffect } from 'react';
import { useLocation } from 'react-router-dom';
import {
  RefreshCw, Sun, Moon, Monitor, LogOut, Languages, ChevronDown, Menu, MoreHorizontal, ChevronUp,
} from 'lucide-react';
import { cn } from '@/lib/utils';
import { useThemeStore } from '@/store/theme';
import { useAuthStore } from '@/store/auth';

const languages = [
  { code: 'en', label: 'EN' },
  { code: 'zh', label: '中文' },
  { code: 'zh-TW', label: '繁體' },
  { code: 'ja', label: '日本語' },
  { code: 'es', label: 'ES' },
];

interface HeaderProps {
  onMenuClick?: () => void;
}

export default function Header({ onMenuClick }: HeaderProps) {
  const { t, i18n } = useTranslation();
  const location = useLocation();
  const { theme, setTheme } = useThemeStore();
  const logout = useAuthStore((s) => s.logout);
  const [spinning, setSpinning] = useState(false);
  const [langOpen, setLangOpen] = useState(false);
  const [utilOpen, setUtilOpen] = useState(false);
  // Mobile-only: collapse the whole top bar to free vertical space for chat.
  // On the chat route the bar starts collapsed (more room for messages);
  // elsewhere it starts expanded. Resetting on route change means entering a
  // chat auto-collapses and leaving auto-expands — no manual toggle needed.
  const [collapsed, setCollapsed] = useState(() => location.pathname.startsWith('/chat'));
  const langRef = useRef<HTMLDivElement>(null);
  const utilRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    setCollapsed(location.pathname.startsWith('/chat'));
  }, [location.pathname]);

  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (langRef.current && !langRef.current.contains(e.target as Node)) setLangOpen(false);
      if (utilRef.current && !utilRef.current.contains(e.target as Node)) setUtilOpen(false);
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, []);

  const handleRefresh = () => {
    setSpinning(true);
    window.dispatchEvent(new CustomEvent('cc:refresh'));
    setTimeout(() => setSpinning(false), 1000);
  };

  const themeIcons = { light: Sun, dark: Moon, system: Monitor };
  const nextTheme = { light: 'dark' as const, dark: 'system' as const, system: 'light' as const };
  const ThemeIcon = themeIcons[theme];

  const changeLang = (code: string) => {
    i18n.changeLanguage(code);
    localStorage.setItem('cc_lang', code);
    setLangOpen(false);
  };

  const btnCls = cn(
    'p-2 rounded-lg transition-all duration-200',
    'text-gray-500 dark:text-gray-400',
    'hover:bg-gray-100/90 dark:hover:bg-white/[0.08] hover:text-gray-800 dark:hover:text-white',
  );

  // The 4 utility actions (refresh / language / theme / logout), shown inline on
  // desktop and inside the mobile "⋯" kebab menu (layer 2 of the top bar).
  const utilItems = (
    <>
      <button type="button" onClick={handleRefresh} className={cn(btnCls, 'w-full flex items-center gap-2 justify-start')} aria-label={t('common.refresh')}>
        <RefreshCw size={16} className={spinning ? 'animate-spin' : ''} />
        <span className="text-sm">{t('common.refresh')}</span>
      </button>

      <div className="relative" ref={langRef}>
        <button type="button" onClick={() => setLangOpen(!langOpen)} className={cn(btnCls, 'w-full flex items-center gap-2 justify-start')}>
          <Languages size={16} />
          <span className="text-sm">{languages.find(l => l.code === i18n.language)?.label}</span>
          <ChevronDown size={14} className="ml-auto opacity-60" />
        </button>
        {langOpen && (
          <div className={cn(
            'absolute left-0 top-full mt-1 w-36 rounded-xl py-1 z-50 overflow-hidden',
            'bg-white/95 backdrop-blur-xl border border-gray-200/80 shadow-xl shadow-black/10',
            'dark:bg-[rgba(0,0,0,0.88)] dark:border-white/[0.1] dark:shadow-black/40',
          )}>
            {languages.map(l => (
              <button
                key={l.code}
                type="button"
                onClick={() => changeLang(l.code)}
                className={cn(
                  'w-full text-left px-3 py-1.5 text-sm transition-colors',
                  i18n.language === l.code
                    ? 'text-accent font-medium bg-accent/10'
                    : 'text-gray-700 dark:text-gray-300 hover:bg-gray-100/80 dark:hover:bg-white/[0.06]',
                )}
              >
                {l.label}
              </button>
            ))}
          </div>
        )}
      </div>

      <button type="button" onClick={() => setTheme(nextTheme[theme])} className={cn(btnCls, 'w-full flex items-center gap-2 justify-start')} aria-label="Theme">
        <ThemeIcon size={16} />
        <span className="text-sm">{t('common.theme')}</span>
      </button>

      <button
        type="button"
        onClick={logout}
        className={cn(
          'w-full flex items-center gap-2 justify-start px-2 py-2 rounded-lg transition-all duration-200',
          'text-gray-400 hover:bg-red-500/10 hover:text-red-600 dark:hover:text-red-400',
        )}
        aria-label={t('login.logout')}
      >
        <LogOut size={16} />
        <span className="text-sm">{t('login.logout')}</span>
      </button>
    </>
  );

  // Collapsed (mobile): the bar is removed from layout so the chat fills the
  // full height. A single floating hamburger (top-right) keeps the nav drawer
  // reachable. The ⋯ utility kebab was removed here on purpose: on the chat
  // route the bar stays collapsed with no expand toggle, keeping the mobile
  // top state simple. Utilities (refresh/theme/lang/logout) remain available
  // on the expanded bar of non-chat routes.
  if (collapsed) {
    return (
      <div className="md:hidden fixed top-2 right-2 z-30 flex items-center gap-1">
        <button
          type="button"
          onClick={onMenuClick}
          className={cn(btnCls, 'bg-white/80 dark:bg-black/70 backdrop-blur-xl shadow-sm')}
          aria-label={t('common.menu')}
        >
          <Menu size={16} />
        </button>
      </div>
    );
  }

  return (
    <header
      className={cn(
        'h-14 flex items-center justify-end gap-1 px-4 shrink-0 relative z-20',
        'border-b border-gray-200/80 dark:border-white/[0.08]',
        'bg-white/70 backdrop-blur-xl dark:bg-[rgba(0,0,0,0.72)]',
      )}
    >
      {/* Hamburger (mobile only) — opens the off-canvas sidebar drawer (layer 1) */}
      <button
        type="button"
        onClick={onMenuClick}
        className={cn(btnCls, 'md:hidden mr-1')}
        aria-label={t('common.menu')}
      >
        <Menu size={16} />
      </button>

      {/* Desktop utilities — inline (md and up). */}
      <div className="hidden md:flex items-center gap-1">
        <button type="button" onClick={handleRefresh} className={btnCls} aria-label={t('common.refresh')}>
          <RefreshCw size={16} className={spinning ? 'animate-spin' : ''} />
        </button>
        <div className="relative" ref={langRef}>
          <button type="button" onClick={() => setLangOpen(!langOpen)} className={cn(btnCls, 'flex items-center gap-1')}>
            <Languages size={16} />
            <span className="text-xs hidden lg:inline">{languages.find(l => l.code === i18n.language)?.label}</span>
          </button>
          {langOpen && (
            <div className={cn(
              'absolute right-0 top-full mt-1 w-36 rounded-xl py-1 z-50 overflow-hidden',
              'bg-white/95 backdrop-blur-xl border border-gray-200/80 shadow-xl shadow-black/10',
              'dark:bg-[rgba(0,0,0,0.88)] dark:border-white/[0.1] dark:shadow-black/40',
            )}>
              {languages.map(l => (
                <button
                  key={l.code}
                  type="button"
                  onClick={() => changeLang(l.code)}
                  className={cn(
                    'w-full text-left px-3 py-1.5 text-sm transition-colors',
                    i18n.language === l.code
                      ? 'text-accent font-medium bg-accent/10'
                      : 'text-gray-700 dark:text-gray-300 hover:bg-gray-100/80 dark:hover:bg-white/[0.06]',
                  )}
                >
                  {l.label}
                </button>
              ))}
            </div>
          )}
        </div>
        <button type="button" onClick={() => setTheme(nextTheme[theme])} className={btnCls} aria-label="Theme">
          <ThemeIcon size={16} />
        </button>
        <button
          type="button"
          onClick={logout}
          className={cn(
            'p-2 rounded-lg transition-all duration-200',
            'text-gray-400 hover:bg-red-500/10 hover:text-red-600 dark:hover:text-red-400',
          )}
          aria-label={t('login.logout')}
        >
          <LogOut size={16} />
        </button>
      </div>

      {/* Mobile utilities — collapsed into a ⋯ kebab (layer 2). */}
      <div className="relative md:hidden" ref={utilRef}>
        <button
          type="button"
          onClick={() => setUtilOpen(!utilOpen)}
          className={cn(btnCls, 'flex items-center gap-1')}
          aria-label={t('common.more')}
        >
          <MoreHorizontal size={16} />
        </button>
        {utilOpen && (
          <div className={cn(
            'absolute right-0 top-full mt-1 w-44 rounded-xl py-1 z-50 overflow-hidden',
            'bg-white/95 backdrop-blur-xl border border-gray-200/80 shadow-xl shadow-black/10',
            'dark:bg-[rgba(0,0,0,0.9)] dark:border-white/[0.1] dark:shadow-black/40',
          )}>
            {utilItems}
          </div>
        )}
      </div>

      {/* Collapse toggle (mobile only) — hides the whole bar for more chat height. */}
      <button
        type="button"
        onClick={() => { setUtilOpen(false); setCollapsed(true); }}
        className={cn(btnCls, 'md:hidden ml-0.5')}
        aria-label={t('common.collapse')}
      >
        <ChevronUp size={16} />
      </button>
    </header>
  );
}
