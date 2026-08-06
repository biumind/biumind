# Changelog

客户端发版历史。每条与 `git tag client-vX.Y.Z` 一一对应。

版本号规范见 [`docs/BiuMind-Client-Release-Manifest.md`](../../docs/BiuMind-Client-Release-Manifest.md) §4:
- `pubspec.yaml version: X.Y.Z+N`(N = build number,可手动 +1)
- `git tag client-vX.Y.Z`(CI 从 tag 读版本,校验 == pubspec X.Y.Z)
- `releases.json version: X.Y.Z`(无 v 无 build)

格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/),日期用 ISO 8601。

---

## [Unreleased]

等待首个正式 tag `client-v0.1.0`。本版本起客户端支持官网直接下载分发:

- **分发链路**:CI(Jenkins 主路径 / GitHub Actions 备用)→ releases.json 清单 → MinIO `releases` bucket → site nginx `/downloads/` 反代 → 官网下载页 + 客户端更新检测。
- **平台**:macOS(arm64 DMG)、Windows(便携 ZIP/NSIS,3c 待补)、Linux(AppImage + .deb,3c 待补)、Android(APK)官网直下;iOS 走 App Store/TestFlight。
- **签名**:本轮未签名(无 Apple Developer ID / Windows 代码签名证书 / Android 生产 keystore)。官网下载页自动展示安装绕行说明(macOS 右键打开 / Windows SmartScreen 仍要运行)。
- **更新检测**:客户端启动拉 releases.json 比对版本,有新版弹 banner 跳官网 `/download`(本轮只做检测提示,不做静默自动更新)。

### 已知缺口(发版前评估)
- [ ] 阶段 3b:Jenkins manifest stage 配 MinIO 凭据(`minio-access-key`/`minio-secret-key`/`minio-endpoint`),否则 releases.json 生成了但产物没镜像到 MinIO → 官网下载 404。
- [ ] 阶段 3c:Windows job(便携 ZIP + NSIS .exe)+ Linux .deb。`apps/client/windows/` 目录已初始化(阶段 0),Jenkins `windows` agent 待就绪。
- [ ] macOS Intel DMG(需 `macos-13` runner / Jenkins intel agent)。当前只产 arm64。
- [ ] iOS 走 TestFlight(签名资产待配)。

## [0.1.0] - TBD

首个官网直下版本(待打 tag)。
