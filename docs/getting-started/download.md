# 下载与安装

BiuMind 一个账号多端通用。各端安装渠道与注意事项如下，也可以直接在官网[下载页](https://biumind.ai/download)获取（下载页会自动检测你的系统并标出推荐项）。

> [!NOTE]
> 下载页的链接由服务端发布清单（`releases.json`）动态生成。某个平台本轮没有发布产物时，对应按钮会自动标注"即将上线"并置灰——以下每个小节都注明了当前的实际发布状态。

## 桌面端

### macOS

- 提供 **Apple Silicon（arm64）** 的 `.dmg` 安装包，文件名形如 `biumind-{版本}-macos-arm64.dmg`。
- Intel 芯片的 Mac 暂无独立安装包，下载页对应按钮显示"即将上线"；可先用 [Web 版](https://biumind.ai/app)。
- 系统要求：macOS 12.0（Monterey）及以上。

安装：打开 `.dmg`，把 `biumind.app` 拖进「应用程序」文件夹即可。

> [!WARNING]
> 安装包的代码签名与公证取决于发布时是否配置了签名证书。如果下载页出现"未签名包"的安装说明，首次打开请**右键点击应用 →「打开」**（不要直接双击），或在「系统设置 → 隐私与安全性」里点「仍要打开」。

桌面端内置了 `biu` CLI 作为本机守护进程，开箱即起；如果本机没有单独安装 biu，客户端也会按需自动下载安装。

### Windows / Linux

Windows（`.msix`）与 Linux（`.deb` / AppImage）安装包尚未发布，下载页对应按钮显示"即将上线"。在此之前可以先用浏览器里的 [Web 版](https://biumind.ai/app)，账号与数据完全一致。

## 移动端

### Android

直接下载 **APK** 安装（下载页"下载 Android APK"按钮，或从 GitHub Releases 获取 `biumind-{版本}-android.apk`）。系统要求：Android 10.0（API 29）及以上。

安装时系统会提示允许"安装未知来源应用"，按引导开启即可。

### iOS

尚未发布，下载页显示"即将上线"。可先用 Web 版。

## Web 版

不想装任何客户端，浏览器打开 [biumind.ai/app](https://biumind.ai/app) 直接使用，账号与桌面端、CLI 完全互通。

## biu 命令行

`biu` 是 BiuMind 的终端 AI 编码代理，单个静态二进制，无运行时依赖。安装方式三选一：

**Homebrew（推荐，macOS / Linux）：**

```bash
brew install biumind/tap/biu
```

**从 GitHub Releases 下载预编译二进制**（覆盖 darwin / linux × amd64 / arm64 四个平台）：

```bash
tar -xzf biu_*_$(uname -s)_$(uname -m).tar.gz
install -m 0755 biu /usr/local/bin/biu
```

**从源码编译**（Go 1.22+）：

```bash
go install github.com/biumind/biumind/apps/cli/biu/cmd/biu@latest
```

验证安装：

```bash
biu version --short
```

首次使用运行 `biu init` 完成配置（云端账号走浏览器授权登录，凭证存入系统钥匙串），然后输入 `biu` 进入 REPL。完整教程见 [CLI 上手指南](../cli/getting-started.md)。

## 浏览器扩展（BiuMind Clipper）

把当前网页或选中文字一键保存进 BiuMind 知识库的 Chrome / Edge / Brave 扩展（Manifest V3）。

扩展尚未上架应用商店，需以开发者模式侧载安装：

1. 下载仓库（`git clone https://github.com/biumind/biumind.git`），扩展源码在 `apps/webclip/` 目录。
2. 打开浏览器的扩展管理页（Chrome / Edge 输入 `chrome://extensions/`），开启**开发者模式**。
3. 点击**加载已解压的扩展程序**，选择 `apps/webclip/` 目录。
4. 在扩展的**选项**页配置 BiuMind 服务器地址与 JWT Token（Token 从客户端的设置页复制），点"测试连接"确认连通。

装好后：点扩展图标打开弹窗选择目标项目后保存；或选中网页文字后右键 →「保存选中文字到 BiuMind」。默认快捷键 `Ctrl+Shift+S`（macOS 为 `Cmd+Shift+S`），可在浏览器的扩展快捷键设置中修改。剪藏的内容会进入对应 Wiki 项目的来源列表，可再触发 AI 摄取整理成 Wiki 页面。

## 下一步

- [快速开始](quickstart.md) —— 注册云端账号或自托管试用
- [什么是 BiuMind](index.md) —— 产品与六大模块总览
