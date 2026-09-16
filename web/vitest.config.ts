import { defineConfig } from 'vitest/config';
import path from 'path';

// Vitest config for the web dashboard. Most modules under test are pure logic
// (the chat per-conversation reducer, race guards, helpers) and run in the node
// environment. `jsdom` is opted into per-file with a `@vitest-environment`
// docblock — used by ChatComposer.test.tsx, which pins the typing-isolation
// behaviour behind the 2026-09-16 input-lag fix.
export default defineConfig({
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  esbuild: {
    jsx: 'automatic',
  },
  test: {
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
    environment: 'node',
  },
});
