# biu daemon system integration

桌面端用户想让 `biu serve` 跟系统一起启动的现成模板。

| 平台              | 模板                              | 安装路径                                     |
| ----------------- | --------------------------------- | -------------------------------------------- |
| macOS (LaunchAgent) | `launchd/com.biumind.biu.plist` | `~/Library/LaunchAgents/com.biumind.biu.plist` |
| Linux (systemd user)| `systemd/biu.service`           | `~/.config/systemd/user/biu.service`         |

每个文件顶部有详细安装指引。两份模板覆盖的功能一致：

- 用户登录后自动起 `biu serve`
- 端口固定 7173（Flutter desktop 默认探活端口）
- PID 文件 `~/.biumind/biu.pid`
- SIGTERM graceful shutdown
- 可选 `--register` 让远端 brain 客户端调度本机

> 不强制装 —— Flutter desktop app（S6-3 落地后）会自己 spawn `biu serve`
> 子进程，跟手动装的 daemon 通过 PID 文件互斥。两种方式择一。

## 跟 Flutter 桌面 app 共存

Flutter app 启动时会先看 `~/.biumind/biu.pid` 是否指向运行中进程：

- **进程在跑**（手动装了 launchd / systemd unit）→ Flutter 复用，不 spawn
- **进程不在 / 没装**（典型）→ Flutter spawn `biu serve --port 0 --pid-file ...`，
  从 stdout 解析 `BIU_BRIDGE_URL=` 拿端口

两种部署形态走相同的 daemon 协议（HTTP + healthz），客户端代码无差异。
