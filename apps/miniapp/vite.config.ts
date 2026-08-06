import { defineConfig } from 'vite';
import uni from '@dcloudio/vite-plugin-uni';

// base — H5 部署到 https://your-biumind.example.com/m/ 子路径下, 资源 URL 必须带
// /m/ 前缀, 否则 site nginx 路由不到本容器. docker build --build-arg BASE=/m/
// → ENV UNI_H5_BASE → 这里读. dev 时不传, 默认 /, 不影响本地.
const base = process.env.UNI_H5_BASE || '/';

export default defineConfig({
  base,
  plugins: [uni()],
  resolve: {
    alias: {
      '@': '/src',
    },
  },
});
