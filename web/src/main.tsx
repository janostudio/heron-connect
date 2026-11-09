import React from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import App from './App';
import './index.css';
import './i18n';
import { useAuthStore } from './store/auth';
import { useThemeStore } from './store/theme';
import { api } from './api/client';

useAuthStore.getState().init();
useThemeStore.getState().init();

// A 401 means the credential no longer matches the server's configured token —
// not that it timed out. rejectToken keeps the value for the login form to
// prefill and stops retrying it; only an explicit logout discards it.
api.setOnUnauthorized(() => {
  useAuthStore.getState().rejectToken();
});

// localStorage is not durable — browsers evict it after a few idle days — so a
// missing token does not mean "logged out". The session cookie survives that
// eviction; ask the server before showing the login page.
if (!useAuthStore.getState().isAuthenticated) {
  void useAuthStore.getState().probe();
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <App />
    </BrowserRouter>
  </React.StrictMode>,
);
