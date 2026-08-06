# BiuMind MiniApp

BiuMind 跨平台小程序客户端 — 基于 **uni-app + Vue 3 + TypeScript**，一套代码编译 9 端：

- 微信小程序 (`mp-weixin`)
- 支付宝小程序 (`mp-alipay`)
- 抖音小程序 (`mp-toutiao`)
- 百度小程序 (`mp-baidu`)
- QQ 小程序 (`mp-qq`)
- 快手小程序 (`mp-kuaishou`)
- 京东小程序 (`mp-jd`)
- 飞书小程序 (`mp-lark`)
- H5 (`h5`)

完整设计见 [`../../docs/BiuMind-MiniApp-Design.md`](../../docs/BiuMind-MiniApp-Design.md)。

## W1 范围（当前）

- ✅ 工程脚手架（package.json / manifest.json / pages.json / vite.config.ts / tsconfig.json）
- ✅ 平台识别 + chunked-SSE 能力探测（`src/core/platform/detect.ts`）
- ✅ token_manager（access/refresh + 平台 storage）
- ✅ 微信端 `wx.login` → `POST /v1/auth/wechat/mp-login` 端到端通
- ✅ me 页面：列已绑定第三方账号（`GET /v1/identity/me/providers`）
- ✅ 5 个 tab（chat / threads / wiki / notify / me）路由 + 4 个分包占位

后续：W2 抽 `packages/ts-sdk/biu` + AiSurface + 对话页；W3 RealtimeHub 平台分流；…

## 开发

```bash
# 安装依赖
pnpm install

# 启动微信小程序开发模式（用微信开发者工具打开 dist/dev/mp-weixin）
pnpm dev:mp-weixin

# 启动 H5 开发模式
pnpm dev:h5

# 类型检查
pnpm type-check
```

## 配置

各平台 appid 在 `manifest.json` 中以 `PLACEHOLDER_*` 占位，发布前替换为真实值。

后端 API 地址通过环境变量注入：

```bash
# .env.local
VITE_BIU_API_BASE=https://mp.biumind.cn
```

## 端到端联调

1. 后端 identity 服务起来，`WECHAT_MP_APPID` / `WECHAT_MP_APPSECRET` 已配置
2. `manifest.json` 的 `mp-weixin.appid` 替换为真实小程序 AppID
3. `pnpm dev:mp-weixin`，用微信开发者工具打开 `dist/dev/mp-weixin`
4. 点"微信一键登录" → 后端应返 200 + token，跳转 `pages-chat/index`
