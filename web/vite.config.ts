import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  build: {
    rollupOptions: {
      // Two entries: the admin SPA and the standalone public share viewer.
      // They must stay separate — the viewer is opened by anonymous
      // recipients, so it must not pull in the router, auth store or
      // websocket that index.html brings along.
      input: {
        main: path.resolve(__dirname, 'index.html'),
        share: path.resolve(__dirname, 'share.html'),
      },
      output: {
        // Without this, Rollup folds React and every other shared module into
        // the same chunk as the SPA (both entries consume React), so opening a
        // share link downloads the whole admin bundle — auth store, router and
        // all — to display one file. Splitting the framework into its own chunk
        // lets the viewer load React alone.
        manualChunks(id) {
          if (!id.includes('node_modules')) return undefined;
          // Markdown rendering is what makes the viewer big; keep it out of the
          // framework chunk so it is shared rather than duplicated per entry.
          if (/node_modules\/(react-markdown|remark-|rehype-|unified|mdast-|hast-|micromark|highlight\.js|lowlight|parse5|property-information|space-separated-tokens|comma-separated-tokens|vfile|unist-|zwitch|character-entities|decode-named-character-reference|trim-lines|devlop|bail|trough|is-plain-obj|extend|longest-streak|ccount|escape-string-regexp|markdown-table|web-namespaces|html-void-elements|stringify-entities|character-reference-invalid|is-alphanumerical|is-decimal|is-hexadecimal)/.test(id)) {
            return 'markdown';
          }
          if (/node_modules\/(react|react-dom|scheduler|react-router|react-router-dom|use-sync-external-store)\//.test(id)) {
            return 'vendor';
          }
          return undefined;
        },
      },
    },
  },
  server: {
    port: 9821,
    proxy: {
      '/api': {
        target: 'http://localhost:9820',
        changeOrigin: true,
        timeout: 45000,
      },
      '/bridge': {
        target: 'http://localhost:9810',
        changeOrigin: true,
        ws: true,
      },
    },
  },
})
