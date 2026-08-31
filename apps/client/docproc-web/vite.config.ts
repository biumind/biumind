import { defineConfig } from 'vite'

export default defineConfig({
  base: './',
  build: {
    target: 'es2020',
    // pdf.worker 走 ?url 显式产物文件（InAppLocalhostServer / iframe 都能
    // 按相对路径加载），其余小 asset 也不内联，保持产物可预测。
    assetsInlineLimit: 0,
    sourcemap: false,
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    port: 5175,
    strictPort: true,
  },
  test: {
    environment: 'jsdom',
    globals: true,
  },
})
