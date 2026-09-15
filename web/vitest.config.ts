import { defineConfig } from 'vitest/config';
import path from 'path';

// Vitest config for the web dashboard. Only the pure modules are unit-tested
// today (the chat per-conversation reducer and its race guards) — there is no
// DOM test environment configured, so keep tests to pure logic. If component
// tests are ever needed, add `environment: 'jsdom'` and @testing-library/react.
export default defineConfig({
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  test: {
    include: ['src/**/*.test.ts'],
    environment: 'node',
  },
});
