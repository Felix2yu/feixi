import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

export default defineConfig({
  base: '/',
  plugins: [react(), tailwindcss()],
  build: { outDir: 'dist', emptyOutDir: true },
  test: { environment: 'node' },
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:8088',
      '/ping': 'http://localhost:8088',
      '/front': 'http://localhost:8088',
      '/badge': 'http://localhost:8088',
      '/b/': 'http://localhost:8088'
    }
  }
});
