import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import AutoImport from 'unplugin-auto-import/vite'
import Components from 'unplugin-vue-components/vite'
import { ElementPlusResolver } from 'unplugin-vue-components/resolvers'

// 路径前缀部署: 用户访问 https://your-biumind.example.com/admin/
//   - 浏览器 base href = /admin/
//   - 静态资源走 /admin/assets/...
//   - SPA 路由 history mode base 也是 /admin/
//   - API 调用走 /v1/* (同源, site nginx 已经按路径分发到各后端)
export default defineConfig({
  base: '/admin/',
  plugins: [
    vue(),
    // Element Plus 按需引入 (减小 bundle)
    AutoImport({ resolvers: [ElementPlusResolver()] }),
    Components({ resolvers: [ElementPlusResolver()] }),
  ],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    port: 5173,
    // dev 阶段把 /v1/* 反代到本地 site nginx
    // SITE_PORT 默认 8088（见 web/site/docker-compose.yml）；如果改了就 export
    // VITE_API_PROXY=http://localhost:你的端口 来覆盖。
    proxy: {
      '/v1': {
        target: process.env.VITE_API_PROXY || 'http://localhost:8088',
        changeOrigin: true,
      },
    },
  },
  build: {
    target: 'es2020',
    sourcemap: false,
    chunkSizeWarningLimit: 1000,
  },
})
