import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// The Go binary embeds internal/web/dist and serves it from "/", so assets use
// absolute paths. In dev, /api goes to a running `ajai serve` on port 7077.
export default defineConfig({
  plugins: [react()],
  base: '/',
  server: {
    proxy: { '/api': 'http://127.0.0.1:7077' },
  },
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
  },
});
