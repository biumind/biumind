export type Locale = 'zh-CN' | 'en';

export interface Translation {
  // meta
  siteTitle: string;
  siteDescription: string;

  // nav
  nav: {
    product: string;
    scenarios: string;
    download: string;
    docs: string;
    github: string;
    tryFree: string;
    openApp: string;
  };

  // hero
  hero: {
    title: string;
    subtitle: string;
    description: string;
    ctaPrimary: string;
    ctaSecondary: string;
    ctaWeb: string;
    platforms: string;
  };

  // scenarios
  scenarios: {
    eyebrow: string;
    title: string;
    items: { title: string; body: string }[];
  };

  // features
  features: {
    eyebrow: string;
    title: string;
    items: { title: string; body: string }[];
  };

  // users
  users: {
    eyebrow: string;
    title: string;
    items: { title: string; body: string; quote: string }[];
    cta: string;
  };

  // multiplatform
  platforms: {
    eyebrow: string;
    title: string;
    description: string;
    items: { name: string; sub: string }[];
  };

  // deploy
  deploy: {
    eyebrow: string;
    title: string;
    cloud: { title: string; body: string; cta: string; openWeb: string };
    selfhost: { title: string; body: string; cta: string };
  };

  // final cta
  finalCta: {
    title: string;
    subtitle: string;
    primary: string;
    secondary: string;
    webHint: string;
  };

  // footer
  footer: {
    tagline: string;
    productCol: string;
    productLinks: { label: string; href?: string }[];
    resourcesCol: string;
    resourcesLinks: { label: string; href: string }[];
    companyCol: string;
    companyLinks: { label: string; href: string }[];
    copyright: string;
  };

  // app launch modal
  appLaunch: {
    close: string;
    remember: string;
    desktop: {
      title: string;
      subtitle: string;
      perks: { label: string; sub: string }[];
      primary: string;
      secondary: string;
    };
    mobile: {
      title: string;
      subtitle: string;
      ios: string;
      android: string;
      secondary: string;
    };
  };

  // download page
  download: {
    title: string;
    subtitle: string;
    desktop: { title: string; body: string; macIntel: string; macSilicon: string; windows: string; linuxDeb: string; linuxAppImage: string };
    mobile: { title: string; body: string; ios: string; android: string; androidApk: string };
    web: { title: string; body: string; cta: string };
    cli: { title: string; body: string; install: string; manual: string; login: string; loginHint: string; verify: string };
    requirements: { title: string; rows: { platform: string; req: string }[] };
    soon: string;
    // 动态下载 (阶段 4): 运行期 fetch /downloads/releases.json 填充按钮
    version: string;        // "版本" 前缀
    size: string;           // "大小" 前缀
    detected: string;       // 含 {os} 占位: "检测到你的系统是 {os},推荐下载:"
    recommended: string;    // 推荐 badge 文案
    unsignedTitle: string;  // 未签名说明标题
    unsignedMac: string;   // macOS 默认绕行说明 (releases.json asset.installHint.zh 优先)
    unsignedWin: string;   // Windows 默认 SmartScreen 说明
    fetchFailed: string;   // releases.json 拉取失败兜底
  };
}
