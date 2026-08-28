import { Outlet, useLocation } from 'react-router-dom';
import { useEffect, useState } from 'react';
import Sidebar from './Sidebar';
import Header from './Header';
import Footer from './Footer';
import { cn } from '@/lib/utils';

export default function Layout() {
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const location = useLocation();

  // Close the mobile drawer whenever the route changes.
  useEffect(() => {
    setSidebarOpen(false);
  }, [location.pathname]);

  // iOS Safari soft-keyboard handling. `dvh` and the `interactive-widget`
  // viewport meta cover most cases, but iOS still won't reflow on keyboard
  // show/hide. Track `window.visualViewport.height` (which DOES shrink when
  // the keyboard opens) and write it to a CSS variable so the layout root
  // resizes to keep the input area above the keyboard.
  useEffect(() => {
    if (typeof window === 'undefined' || !window.visualViewport) return;
    const vv = window.visualViewport;
    const apply = () => {
      // Round to integer px to avoid sub-pixel jitter on every move event.
      document.documentElement.style.setProperty(
        '--app-height',
        `${Math.round(vv.height)}px`,
      );
    };
    apply();
    vv.addEventListener('resize', apply);
    return () => vv.removeEventListener('resize', apply);
  }, []);

  // The chat view is a full-height conversation surface; the global copyright
  // footer is unnecessary there and would eat vertical space, so hide it.
  const showFooter = !location.pathname.startsWith('/chat');

  return (
    <div
      className={cn(
        // Layout height, in priority order:
        // 1. `--app-height` (iOS visualViewport, shrinks with soft keyboard)
        // 2. `100dvh` (modern browsers — excludes URL bar chrome)
        // 3. `h-screen` (fallback = 100vh)
        'flex supports-[height:100dvh]:h-[var(--app-height,100dvh)] overflow-hidden',
        'bg-gradient-to-br from-gray-100 via-white to-gray-100',
        'dark:from-gray-950 dark:via-[#0a0a0c] dark:to-gray-950',
      )}
    >
      <Sidebar mobileOpen={sidebarOpen} onClose={() => setSidebarOpen(false)} />
      <div className="flex-1 flex flex-col overflow-hidden min-w-0">
        <Header onMenuClick={() => setSidebarOpen(true)} />
        {/* On the chat route the conversation surface manages its own scrolling
            (only the message list scrolls, with header + input pinned). Make
            <main> a fixed-height, non-scrolling column there so the nested
            message area becomes the sole scroller. Other routes keep <main>
            scrollable as before. */}
        <main className={cn(
          'flex-1 flex flex-col min-h-0',
          showFooter ? 'overflow-y-auto p-4 md:p-6' : 'overflow-hidden',
        )}>
          {/* `min-h-0` is load-bearing: without it this wrapper's automatic
              minimum size equals its content height (all chat messages), which
              overflows main's fixed height and gets clipped by overflow-hidden
              — the input area vanishes and the message list cannot scroll. */}
          <div className="flex-1 flex flex-col min-h-0">
            <Outlet />
          </div>
          {showFooter && <Footer />}
        </main>
      </div>
    </div>
  );
}
