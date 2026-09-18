import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

const appVersion = readFileSync(resolve(__dirname, '../../VERSION'), 'utf-8').trim();

export default defineConfig({
  plugins: [react()],
  define: {
    'import.meta.env.VITE_APP_VERSION': JSON.stringify(appVersion),
  },
  build: {
    rollupOptions: {
      // Multi-page build (item 268): a handful of public routes get their
      // own real HTML entry, each loading the same React app (src/main.tsx)
      // but with unique, human-written crawler-visible content and
      // title/meta — so a non-JS client sees genuinely distinct pages
      // instead of the same SPA shell at every URL (the root cause of two
      // straight AdSense "low value content" rejections). Each entry's
      // source path mirrors its desired dist/ output path exactly
      // (leaderboard/index.html -> dist/leaderboard/index.html), which is
      // what CloudFront's clean-URL rewrite (canonical-host-redirect.js)
      // expects for a request to e.g. /leaderboard. React Router still
      // owns actual routing once the bundle loads — these are only the
      // pre-hydration HTML shells.
      input: {
        main: resolve(__dirname, 'index.html'),
        leaderboard: resolve(__dirname, 'leaderboard/index.html'),
        gamedays: resolve(__dirname, 'gamedays/index.html'),
        legal: resolve(__dirname, 'legal/index.html'),
        help: resolve(__dirname, 'help/index.html'),
      },
    },
  },
});
