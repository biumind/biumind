import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vite'

export default defineConfig({
  base: './',
  resolve: {
    alias: {
      // Crepe 静态 import katex，但 latex feature 已关闭（见 src/main.ts），
      // alias 到 stub 才能把 KaTeX (~586KB) 真正移出产物。
      katex: fileURLToPath(new URL('./src/stubs/katex.ts', import.meta.url)),
    },
  },
  build: {
    target: 'es2020',
    assetsInlineLimit: 0,
    sourcemap: false,
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    port: 5174,
    strictPort: true,
  },
  test: {
    environment: 'jsdom',
    globals: true,
  },
})
