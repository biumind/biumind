import type { Translation } from './types';

export const en: Translation = {
  siteTitle: 'BiuMind — Your AI Workbench',
  siteDescription: 'Write docs, run agents, ship code, handle support — in one workbench. Eight SaaS apps collapsed into one, with shared data.',

  nav: {
    product: 'Product',
    scenarios: 'Scenarios',
    download: 'Download',
    docs: 'Docs',
    github: 'GitHub',
    tryFree: 'Try free',
    openApp: 'Open App',
  },

  hero: {
    title: 'Your AI Workbench',
    subtitle: 'Write docs · Run agents · Ship code · Handle support',
    description: 'BiuMind collapses scattered AI tools into one workbench with shared data. One account — desktop, mobile, browser, CLI, all in sync.',
    ctaPrimary: 'Try free',
    ctaSecondary: 'See what it does',
    ctaWeb: 'Open in Browser',
    platforms: 'macOS · Windows · Linux · iOS · Android · Web',
  },

  scenarios: {
    eyebrow: 'Scenarios',
    title: 'Describe the task in one sentence — BiuMind gets it done',
    items: [
      {
        title: 'Research a market',
        body: 'Web search + document ingestion + auto knowledge graph. Get the players, the moves, and where the trend is going.',
      },
      {
        title: 'Fix a bug',
        body: 'Dispatch an AI engineer in the coding workbench — it locates the issue, writes the patch, runs tests and opens a PR. Multiple tasks run in parallel; every file edit and command asks you first.',
      },
      {
        title: 'Make an image',
        body: 'Text-to-image, text-to-video, viral-video breakdown — turn one prompt into finished work. Outputs archive to your knowledge base, ready to reuse and remix.',
      },
      {
        title: 'Remember everything',
        body: 'Web clips, chats and docs flow into your knowledge base automatically; AI connects them into a graph. Weeks later, one search brings any article back.',
      },
    ],
  },

  features: {
    eyebrow: 'Features',
    title: 'Six pillars, one workday covered',
    items: [
      {
        title: 'Knowledge Hub',
        body: 'Block editor, knowledge graph, AI memory of every conversation. Notes, chats, web clips — all flow into the same place.',
      },
      {
        title: 'Coding Workbench',
        body: 'A team of AI engineers writing, testing and shipping in parallel. Switch seamlessly between desktop, mobile and CLI; tasks keep running in the cloud.',
      },
      {
        title: 'Creation',
        body: 'Text-to-image, text-to-video, viral-video breakdown — turn one prompt into finished work. Outputs archive to your knowledge base, ready to reuse and remix.',
      },
      {
        title: 'Cloud Workspace',
        body: 'Sessions and memory live in the cloud — pick up the same context on desktop, mobile or CLI. Switch devices, lose nothing.',
      },
      {
        title: 'Channels',
        body: 'Plug AI into Lark, Telegram, Slack, Discord and email. Common questions answered automatically, with knowledge-base recall to keep replies on track.',
      },
      {
        title: 'App Center',
        body: 'RSS digests, email summaries, market watchers, paper trackers — production-ready specialist agents, one click to enable.',
      },
    ],
  },

  users: {
    eyebrow: 'Users',
    title: 'One workbench, three ways to work',
    items: [
      {
        title: 'Creators',
        body: 'Writing, knowledge management and AI assistance in one place. No more copy-pasting between Notion and ChatGPT.',
        quote: '"Notes and AI, finally in the same place"',
      },
      {
        title: 'Developers',
        body: 'CLI and GUI share the same kernel. Code, knowledge and agents live together; multiple agents run in parallel.',
        quote: '"Code, context and AI all together"',
      },
      {
        title: 'Teams & Enterprises',
        body: 'Self-host so data never leaves your servers. Multi-channel support, shared knowledge, fine-grained permissions, visible budgets.',
        quote: '"Data on your servers, control in your hands"',
      },
    ],
    cta: 'Explore scenarios',
  },

  platforms: {
    eyebrow: 'Cross-platform',
    title: 'One account. Everywhere.',
    description: 'The same working memory, synced across every device. Pick up wherever you left off.',
    items: [
      { name: 'Desktop', sub: 'macOS · Windows · Linux' },
      { name: 'Mobile', sub: 'iOS · Android' },
      { name: 'Browser', sub: 'Chrome · Safari · Edge' },
      { name: 'CLI', sub: 'biu' },
    ],
  },

  deploy: {
    eyebrow: 'Deploy',
    title: 'Sign up and go — or self-host on your own metal',
    cloud: {
      title: 'Cloud',
      body: 'An account in seconds, with a free monthly allowance. Made for individuals and small teams.',
      cta: 'Sign up free',
      openWeb: 'or open straight in the browser',
    },
    selfhost: {
      title: 'Self-hosted',
      body: 'One command to bring up the full stack. Your data stays on your servers — built for enterprises and sensitive workloads.',
      cta: 'Read the deploy guide',
    },
  },

  finalCta: {
    title: 'Collapse your AI tools into one workbench',
    subtitle: 'Free to try. No credit card required.',
    primary: 'Try free',
    secondary: 'Star on GitHub',
    webHint: 'or open the web app',
  },

  footer: {
    tagline: 'Your AI Workbench',
    productCol: 'Product',
    productLinks: [
      { label: 'Web App', href: '/app' },
      { label: 'Download', href: '/en/download' },
      { label: 'Knowledge Hub' },
      { label: 'Coding Workbench' },
      { label: 'Creation' },
      { label: 'Cloud Workspace' },
    ],
    resourcesCol: 'Resources',
    resourcesLinks: [
      { label: 'Docs', href: '/docs' },
      { label: 'Download', href: '/en/download' },
      { label: 'Changelog', href: '#' },
      { label: 'GitHub', href: 'https://github.com/biumind/biumind' },
    ],
    companyCol: 'Company',
    companyLinks: [
      { label: 'About', href: '#' },
      { label: 'Blog', href: '#' },
      { label: 'Contact', href: 'mailto:hello@biumind.ai' },
      { label: 'Privacy', href: '#' },
    ],
    copyright: '© 2026 BiuMind. All rights reserved.',
  },

  appLaunch: {
    close: 'Close',
    remember: "Don't show again",
    desktop: {
      title: 'The desktop app feels better',
      subtitle: 'Built for long sessions — fewer paper cuts, more flow.',
      perks: [
        { label: 'Global shortcuts', sub: 'Summon BiuMind from anywhere' },
        { label: 'Native notifications', sub: 'Know the moment an agent finishes' },
        { label: 'Faster rendering', sub: 'Big docs and graphs stay smooth' },
        { label: 'Offline access', sub: 'Read and edit without a connection' },
      ],
      primary: 'Download Desktop App',
      secondary: 'Continue to Web',
    },
    mobile: {
      title: 'BiuMind feels better as an app',
      subtitle: 'Push notifications, native gestures, offline access — the way mobile should feel.',
      ios: 'App Store',
      android: 'Google Play',
      secondary: 'Continue in browser',
    },
  },

  download: {
    title: 'Download BiuMind',
    subtitle: 'One account. Desktop, mobile, browser, CLI — everywhere.',
    desktop: {
      title: 'Desktop',
      body: 'Your main workstation. Block editor, knowledge graph, multi-agent coding and cloud sandboxes — all in the desktop app.',
      macSilicon: 'macOS · Apple Silicon',
      macIntel: 'macOS · Intel',
      windows: 'Windows 10/11',
      linuxDeb: 'Linux · .deb',
      linuxAppImage: 'Linux · AppImage',
    },
    mobile: {
      title: 'Mobile',
      body: 'Approve agent work, browse sessions and search the knowledge base — from anywhere.',
      ios: 'App Store',
      android: 'Google Play',
      androidApk: 'Download Android APK',
    },
    web: {
      title: 'Browser',
      body: 'No install needed. Same features as the desktop app, straight from your browser.',
      cta: 'Open in browser',
    },
    cli: {
      title: 'biu CLI',
      body: 'Chat, run agents and query your knowledge base from the terminal. CLI and GUI share the same kernel; sessions are interchangeable.',
      install: 'One-line install',
      verify: 'Verify install',
    },
    requirements: {
      title: 'System requirements',
      rows: [
        { platform: 'macOS', req: '12.0 Monterey or later' },
        { platform: 'Windows', req: '10 / 11, 64-bit' },
        { platform: 'Linux', req: 'Ubuntu 22.04 / Debian 12 / Fedora 38 or later' },
        { platform: 'iOS', req: '16.0 or later' },
        { platform: 'Android', req: '10.0 (API 29) or later' },
      ],
    },
    soon: 'Coming soon',
    version: 'Version',
    size: 'Size',
    detected: 'Detected {os}. Recommended download:',
    recommended: 'Recommended',
    unsignedTitle: 'Install note (unsigned build)',
    unsignedMac: 'First launch: right-click the app → "Open", or System Settings → Privacy & Security → "Open Anyway".',
    unsignedWin: 'If SmartScreen blocks it, click "More info" → "Run anyway".',
    fetchFailed: 'Download links are temporarily unavailable. Please retry or visit GitHub Releases.',
  },
};
