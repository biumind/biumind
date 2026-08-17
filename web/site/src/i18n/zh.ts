import type { Translation } from './types';

export const zh: Translation = {
  siteTitle: 'BiuMind — 你的 AI 工作台',
  siteDescription: '把写文档、跑 Agent、写代码、做客服收进同一个工作台。8 个 SaaS 收成 1 个，数据通在一起。',

  nav: {
    product: '产品',
    scenarios: '场景',
    download: '下载',
    docs: '文档',
    github: 'GitHub',
    tryFree: '免费试用',
    openApp: '打开应用',
  },

  hero: {
    title: '你的 AI 工作台',
    subtitle: '写文档 · 跑 Agent · 写代码 · 做客服',
    description: '把分散的 AI 工具收成一个工作台，数据通在一起。一个账号，桌面、手机、浏览器、命令行随时切换。',
    ctaPrimary: '免费试用',
    ctaSecondary: '看看能做什么',
    ctaWeb: '打开 Web 版',
    platforms: 'macOS · Windows · Linux · iOS · Android · Web',
  },

  scenarios: {
    eyebrow: '场景',
    title: '一句话描述任务，BiuMind 帮你做完',
    items: [
      {
        title: '调研一个赛道',
        body: 'Web 搜索 + 文档解析 + 自动建知识图谱。告诉你这个行业谁在做什么、关键玩家是谁、趋势往哪走。',
      },
      {
        title: '修一个 Bug',
        body: '在编码工作台派一个 AI 工程师，它自己定位问题、改代码、跑测试、提 PR。多个任务并行跑，改哪些文件、跑什么命令都先问你。',
      },
      {
        title: '做一张图',
        body: '文生图、文生视频、爆款拆解，一句话把灵感变成成品。产物自动沉淀进知识库，随时复用、二次创作。',
      },
      {
        title: '沉淀看到的一切',
        body: '网页剪藏、对话、文档自动进知识库，AI 帮你连成图谱。下周再想起某篇文章，搜一句话就能找回来。',
      },
    ],
  },

  features: {
    eyebrow: '功能',
    title: '六块核心，覆盖一天的全部 AI 工作',
    items: [
      {
        title: '知识中枢',
        body: '块编辑器写文档，知识图谱看全局，AI 自动整理你说过的每句话。文档、对话、网页剪藏沉淀到同一个地方。',
      },
      {
        title: '编码工作台',
        body: '多个 AI 工程师同时帮你写代码、跑测试、提 PR。桌面、手机、命令行随时切换，任务在云端继续跑。',
      },
      {
        title: '创作',
        body: '文生图、文生视频、爆款拆解，一句话把灵感变成成品。产物沉淀进知识库，随时复用、二次创作。',
      },
      {
        title: '云端工位',
        body: '会话和记忆存在云端，桌面、手机、命令行随时接续。换个设备打开，上下文一点不丢。',
      },
      {
        title: '消息接入',
        body: '把 AI 接进飞书、Telegram、Slack、Discord、邮件，自动回复常见问题。带知识库召回，回答不跑偏。',
      },
      {
        title: '应用中心',
        body: 'RSS 订阅、邮件总结、股票动态、论文追踪……开箱即用的专业 AI 助手，按需开启。',
      },
    ],
  },

  users: {
    eyebrow: '用户',
    title: '一个工作台，三种用法',
    items: [
      {
        title: '创作者',
        body: '把写作、知识管理和 AI 助手收进一个地方。再也不在 Notion 和 ChatGPT 之间复制粘贴。',
        quote: '"个人写作 + 知识管理 一站搞定"',
      },
      {
        title: '开发者',
        body: '命令行和图形界面共享同一个内核。代码、知识库、AI Agent 全在一起，多 Agent 并行干活。',
        quote: '"代码、知识、AI 全在一起"',
      },
      {
        title: '团队 / 企业',
        body: '自托管部署，数据留在自己服务器。多渠道客服、知识共享、权限可控，预算和用量看得见。',
        quote: '"数据不出墙，预算和权限可控"',
      },
    ],
    cta: '查看场景',
  },

  platforms: {
    eyebrow: '跨端',
    title: '一个账号，处处可用',
    description: '同一份工作记忆，云端同步，任何设备打开都能继续。',
    items: [
      { name: '桌面', sub: 'macOS · Windows · Linux' },
      { name: '手机', sub: 'iOS · Android' },
      { name: '浏览器', sub: 'Chrome · Safari · Edge' },
      { name: '命令行', sub: 'biu CLI' },
    ],
  },

  deploy: {
    eyebrow: '部署',
    title: '云端注册即用，也能私有部署',
    cloud: {
      title: '注册即用',
      body: '几秒钟开账号，自带每月免费额度。适合个人和小团队，开箱即用。',
      cta: '免费注册',
      openWeb: '或直接在浏览器中打开',
    },
    selfhost: {
      title: '私有部署',
      body: '一行命令拉起全栈，数据全程留在自己服务器。适合企业和数据敏感场景。',
      cta: '查看部署文档',
    },
  },

  finalCta: {
    title: '把分散的 AI 收进同一个工作台',
    subtitle: '免费试用，无需信用卡',
    primary: '免费试用',
    secondary: '在 GitHub 上 Star',
    webHint: '或直接打开 Web 版',
  },

  footer: {
    tagline: '你的 AI 工作台',
    productCol: '产品',
    productLinks: [
      { label: 'Web 版', href: '/app' },
      { label: '下载客户端', href: '/download' },
      { label: '知识中枢' },
      { label: '编码工作台' },
      { label: '创作' },
      { label: '云端工位' },
    ],
    resourcesCol: '资源',
    resourcesLinks: [
      { label: '文档', href: '/docs' },
      { label: '下载', href: '/download' },
      { label: '更新日志', href: '#' },
      { label: 'GitHub', href: 'https://github.com/biumind/biumind' },
    ],
    companyCol: '公司',
    companyLinks: [
      { label: '关于', href: '#' },
      { label: '博客', href: '#' },
      { label: '联系', href: 'mailto:hello@biumind.ai' },
      { label: '隐私', href: '#' },
    ],
    copyright: '© 2026 BiuMind. All rights reserved.',
  },

  appLaunch: {
    close: '关闭',
    remember: '不再提示',
    desktop: {
      title: '桌面客户端体验更完整',
      subtitle: '为长时间工作准备的旗舰端 — 你会用得更顺手。',
      perks: [
        { label: '全局快捷键', sub: '随手呼出，零鼠标操作' },
        { label: '原生通知', sub: 'Agent 跑完了立刻知道' },
        { label: '本地渲染快', sub: '大文档、知识图谱也不卡' },
        { label: '离线可用', sub: '断网时仍能阅读和编辑' },
      ],
      primary: '下载桌面客户端',
      secondary: '继续打开 Web 版',
    },
    mobile: {
      title: '装个 App 更顺手',
      subtitle: '推送通知、原生手势、离线查看 — 移动端的最佳体验。',
      ios: 'App Store',
      android: 'Google Play',
      secondary: '在浏览器中继续',
    },
  },

  download: {
    title: '下载 BiuMind',
    subtitle: '一个账号，桌面、手机、浏览器、命令行处处可用。',
    desktop: {
      title: '桌面版',
      body: '主力工作端。块编辑器、知识图谱、多 Agent 编码、云沙箱全部在桌面打开。',
      macSilicon: 'macOS · Apple Silicon',
      macIntel: 'macOS · Intel',
      windows: 'Windows 10/11',
      linuxDeb: 'Linux · .deb',
      linuxAppImage: 'Linux · AppImage',
    },
    mobile: {
      title: '移动端',
      body: '随时审批 AI Agent 的工作、看会话、查知识库。',
      ios: 'App Store',
      android: 'Google Play',
      androidApk: '下载 Android APK',
    },
    web: {
      title: '浏览器',
      body: '不想装客户端，直接打开网页就用。功能与桌面端一致。',
      cta: '在浏览器中打开',
    },
    cli: {
      title: 'biu 命令行',
      body: '终端里直接对话、跑 Agent、操作知识库。CLI 与 GUI 共享同一内核，会话互通。',
      install: '一键安装',
      verify: '验证安装',
    },
    requirements: {
      title: '系统要求',
      rows: [
        { platform: 'macOS', req: '12.0 Monterey 及以上' },
        { platform: 'Windows', req: '10 / 11，64 位' },
        { platform: 'Linux', req: 'Ubuntu 22.04 / Debian 12 / Fedora 38 及以上' },
        { platform: 'iOS', req: '16.0 及以上' },
        { platform: 'Android', req: '10.0（API 29）及以上' },
      ],
    },
    soon: '即将上线',
    version: '版本',
    size: '大小',
    detected: '检测到你的系统是 {os}，推荐下载：',
    recommended: '推荐',
    unsignedTitle: '安装说明（未签名包）',
    unsignedMac: '首次打开：右键点击应用 →「打开」，或在「系统设置 → 隐私与安全性」点「仍要打开」。',
    unsignedWin: '运行时若被 SmartScreen 拦截，点「更多信息」→「仍要运行」。',
    fetchFailed: '下载链接暂时不可用，请稍后重试或前往 GitHub Releases。',
  },
};
