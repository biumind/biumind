// vitest config —— 单测专用，不走 uni-app 插件链。
//
// vite.config.ts 那条加载链跑在 uni dev/build 命令下；vitest 直接 require
// vite.config.ts 会 trip uni-cli-shared 的 hbx 引导（要 uni 全局），所以
// 这里给 vitest 单独一份配置避开 uni 插件。
//
// 范围：apps/miniapp/test/**/*.test.ts
// 入口：pnpm test  → vitest run
//      pnpm test:watch → vitest 守护

import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    include: ['test/**/*.test.ts'],
    environment: 'node',
    // miniapp 业务代码 import.meta.env.VITE_BIU_API_BASE 要 stub —— vitest
    // 不跑 vite plugin chain 时这些值 undefined，BiuClient 默认走 fallback
    globals: false,
  },
});
