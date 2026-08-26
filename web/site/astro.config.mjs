import { defineConfig } from 'astro/config';
import tailwind from '@astrojs/tailwind';
import sitemap from '@astrojs/sitemap';

// https://astro.build/config
export default defineConfig({
  site: 'https://biumind.ai',
  trailingSlash: 'never',
  i18n: {
    defaultLocale: 'zh-CN',
    locales: ['zh-CN', 'en'],
    routing: {
      prefixDefaultLocale: false,
      redirectToDefaultLocale: false,
    },
  },
  integrations: [
    tailwind({ applyBaseStyles: false }),
    sitemap({
      // 笔记分享落地页一律 noindex（设计 D3），不进 sitemap
      filter: (page) => !page.includes('/s/') && !page.endsWith('/s'),
    }),
  ],
  build: {
    format: 'directory',
  },
});
