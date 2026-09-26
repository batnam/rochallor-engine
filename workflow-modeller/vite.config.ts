import { URL, fileURLToPath } from 'node:url';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// Expression parser generation is wired via the `predev` / `prebuild` npm
// scripts in package.json (see `build:parser`). Keeping it out of Vite itself
// avoids a custom plugin and makes the generated file trivially cacheable.

export default defineConfig(({ mode }) => ({
  plugins: [react()],
  resolve: {
    alias: {
      '@platform': fileURLToPath(
        new URL(
          mode === 'desktop' ? './src/platform/desktop.ts' : './src/platform/web.ts',
          import.meta.url,
        ),
      ),
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    port: 5173,
    strictPort: mode === 'desktop',
    watch: { ignored: ['**/src-tauri/**'] },
  },
  preview: {
    port: 4173,
    strictPort: true,
  },
  build: {
    target: 'es2022',
    sourcemap: true,
    outDir: mode === 'desktop' ? 'dist-desktop' : 'dist',
    emptyOutDir: true,
  },
}));
