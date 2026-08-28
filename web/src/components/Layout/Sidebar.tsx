import { NavLink } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import {
  LayoutDashboard,
  FolderKanban,
  MessageSquare,
  History,
  Clock,
  Settings,
  ChevronLeft,
  ChevronRight,
  Plug,
  Puzzle,
  X,
} from 'lucide-react';
import { cn } from '@/lib/utils';
import { useState } from 'react';

const navItems = [
  { key: 'dashboard', path: '/', icon: LayoutDashboard },
  { key: 'projects', path: '/projects', icon: FolderKanban },
  { key: 'providers', path: '/providers', icon: Plug },
  { key: 'skills', path: '/skills', icon: Puzzle },
  { key: 'chat', path: '/chat', icon: MessageSquare },
  { key: 'sessions', path: '/sessions', icon: History },
  { key: 'cron', path: '/cron', icon: Clock },
  { key: 'system', path: '/system', icon: Settings },
];

interface Props {
  /** Mobile drawer open state (controlled by Layout). Ignored on desktop. */
  mobileOpen?: boolean;
  onClose?: () => void;
}

export default function Sidebar({ mobileOpen = false, onClose }: Props) {
  const { t } = useTranslation();
  const [collapsed, setCollapsed] = useState(false);

  const navList = (
    <nav className="flex-1 py-4 space-y-1 px-2 overflow-y-auto">
      {navItems.map(({ key, path, icon: Icon }) => (
        <NavLink
          key={key}
          to={path}
          end={path === '/'}
          onClick={onClose}
          title={collapsed ? t(`nav.${key}`) : undefined}
          className={({ isActive }) =>
            cn(
              'flex items-center gap-3 px-3 py-2.5 rounded-xl text-sm font-medium transition-all duration-200',
              collapsed && 'justify-center px-0',
              isActive
                ? 'bg-accent/12 text-accent ring-1 ring-accent/25'
                : 'text-gray-600 dark:text-gray-400 hover:bg-gray-100/80 dark:hover:bg-white/[0.06] hover:text-gray-900 dark:hover:text-white',
            )
          }
        >
          <Icon size={18} className="shrink-0" />
          {!collapsed && <span className="truncate">{t(`nav.${key}`)}</span>}
        </NavLink>
      ))}
    </nav>
  );

  return (
    <>
      {/* Desktop: fixed sidebar (always visible >= md). Collapse logic unchanged. */}
      <aside
        className={cn(
          'hidden md:flex h-dvh supports-[height:100dvh]:h-[100dvh] flex-col border-r transition-all duration-300 ease-out',
          'bg-white/75 backdrop-blur-xl border-gray-200/80',
          'dark:bg-[rgba(0,0,0,0.85)] dark:backdrop-blur-xl dark:border-white/[0.08]',
          collapsed ? 'w-16' : 'w-56',
        )}
      >
        {/* Brand */}
        <div
          className={cn(
            'flex items-center px-4 h-14 border-b transition-colors shrink-0',
            'border-gray-200/80 dark:border-white/[0.08]',
            collapsed ? 'justify-center' : 'gap-0',
          )}
        >
          {collapsed ? (
            <span className="text-base font-bold tracking-tighter text-gray-900 dark:text-white">
              HC
            </span>
          ) : (
            <span className="text-base font-bold tracking-tight text-gray-900 dark:text-white">
              Heron<span className="text-accent">-</span>Connect
            </span>
          )}
        </div>

        {navList}

        {/* Collapse toggle */}
        <div className={cn('border-t p-2', 'border-gray-200/80 dark:border-white/[0.08]')}>
          <button
            type="button"
            onClick={() => setCollapsed(!collapsed)}
            className={cn(
              'flex items-center justify-center w-full px-3 py-2 rounded-xl transition-colors duration-200',
              'text-gray-400 hover:bg-gray-100/80 dark:hover:bg-white/[0.06]',
            )}
          >
            {collapsed ? <ChevronRight size={18} /> : <ChevronLeft size={18} />}
          </button>
        </div>
      </aside>

      {/* Mobile: off-canvas left drawer (< md). */}
      {mobileOpen && (
        <div
          className="fixed inset-0 bg-black/40 z-40 md:hidden"
          onClick={onClose}
          aria-hidden="true"
        />
      )}
      <aside
        className={cn(
          'fixed top-0 left-0 h-dvh w-64 z-50 flex flex-col md:hidden transition-transform duration-300 ease-out',
          'bg-white/95 backdrop-blur-xl border-r border-gray-200/80 shadow-2xl shadow-black/15',
          'dark:bg-[rgba(15,15,15,0.97)] dark:border-white/[0.08] dark:shadow-black/50',
          mobileOpen ? 'translate-x-0' : '-translate-x-full',
        )}
      >
        {/* Brand + close */}
        <div
          className={cn(
            'flex items-center justify-between px-4 h-14 border-b shrink-0',
            'border-gray-200/80 dark:border-white/[0.08]',
          )}
        >
          <span className="text-base font-bold tracking-tight text-gray-900 dark:text-white">
            Heron<span className="text-accent">-</span>Connect
          </span>
          <button
            type="button"
            onClick={onClose}
            className="p-1.5 rounded-lg text-gray-400 hover:text-gray-600 dark:hover:text-gray-200 hover:bg-gray-100 dark:hover:bg-white/[0.06] transition-colors"
            aria-label={t('common.close')}
          >
            <X size={18} />
          </button>
        </div>

        {navList}
      </aside>
    </>
  );
}
