# Bash 沙箱

`biu` 的 Bash 工具默认把每条命令包在操作系统级的沙箱里执行，让 LLM 生成的命令（以及你确认放行的命令）无法越出项目目录破坏主机。沙箱与权限系统（[permissions.md](permissions.md)）相互独立：权限决定"这次调用能不能跑"，沙箱决定"跑起来之后能碰什么"。

## 平台策略

| 平台 | 机制 | 说明 |
|------|------|------|
| macOS | `sandbox-exec`（SBPL profile） | 系统自带，无需安装 |
| Linux | Bubblewrap（`bwrap`） | 需安装（Debian/Ubuntu：`apt install bubblewrap`）。启动时会实际探测一次用户命名空间是否可用，不可用则回退到不沙箱执行（`bwrap` 存在但被 hardened 内核 / 容器禁用 userns 时不会假装在沙箱里） |
| 其他平台 | 无沙箱 | Bash 直接以子进程运行 |

沙箱工具缺失时 `biu doctor` 会给出 `!` 告警（例如 `bwrap not in PATH — Bash will run unsandboxed`）。

## 默认策略

不配置任何沙箱规则时，每条 Bash 命令在如下约束下运行：

- **读**：全盘可读（模型经常需要查看 `/tmp`、`/etc`、系统框架）。
- **写**：仅当前工作目录（以及 `/tmp`、macOS 的 `/var/folders`、`/dev/null` 与标准流等必要设备）。
- **网络**：默认关闭（macOS 下仅放行 loopback 绑定）。模型可以在单次调用里通过 `allow_network` 参数显式申请联网，该参数会体现在权限询问里。
- **额外可写目录**：通过 `/add-dir`、`--add-dir` 或 settings.json `permissions.additionalDirectories` 注册的工作目录，会自动加入可写根。

## 自定义规则：settings.json 的 `sandbox` 块

四个列表字段构成一张"读 / 写 × 拒绝 / 豁免"矩阵：

```json
{
  "sandbox": {
    "fsReadDeny": [
      "~/.ssh",
      "~/.aws",
      "~/.config/gcloud"
    ],
    "fsReadAllowWithinDeny": [
      "~/.aws/config"
    ],
    "fsWriteAllowExtra": [
      "${PROJECT_ROOT}/../build-output"
    ],
    "fsWriteDenyWithinAllow": [
      "${PROJECT_ROOT}/../build-output/keep-out"
    ]
  }
}
```

| 字段 | 作用 |
|------|------|
| `fsReadDeny` | 禁止读取的绝对路径——典型用途是挡住凭据目录（`~/.ssh`、`~/.aws`、云 CLI 配置等） |
| `fsReadAllowWithinDeny` | 在被禁目录里重新放行个别路径（如禁 `~/.aws` 但允许读 `~/.aws/config`） |
| `fsWriteAllowExtra` | 当前目录之外追加的可写根（例如兄弟目录里的构建产物目录） |
| `fsWriteDenyWithinAllow` | 在可写根里挖洞——即使外层目录放行写入，这些子路径仍然只读 |

### 路径写法

- 必须是**绝对路径**（相对路径会被静默丢弃，防止用 `../` 逃逸）；
- 支持 `~`（主目录）、`${VAR}`（环境变量）、`${PROJECT_ROOT}`（项目根）三种展开。

### 三层合并：只紧不松

`sandbox` 块与权限规则一样分布在三层 settings.json（`~/.biumind/settings.json` → 项目 `.biumind/settings.json` → `.biumind/settings.local.json`），合并规则是**纯并集**：项目层和本地层只能往四个列表里**追加**条目，不能删除或清空用户层的条目。

> [!WARNING]
> 这是刻意为之的安全基线：一个恶意仓库的 `.biumind/settings.json` 无法用 `"fsReadDeny": []` 悄悄撤掉你全局配置里的"禁读 `~/.ssh`"规则。同理，插件禁用列表（`plugins.disabled`）也是只增不减的并集。

合并结果可以随时用 `biu doctor` 验证——`sandbox` 检查项会打印四张列表的条目数：

```text
✓ sandbox            read-deny=3 allow-within=1 write-extra=2 deny-within=1
```

> [!TIP]
> settings.json 的键名是 camelCase（`fsReadDeny`）。写成 `fs_read_deny` 不会报错，只会静默得到一份空规则——`biu doctor` 是发现这类拼写问题的地方。

## 各平台的实现语义

**macOS（SBPL，最后一条匹配的规则生效）：**

1. 基线放行全部读取；
2. `fsReadDeny` 逐条 deny；
3. `fsReadAllowWithinDeny` 逐条重新 allow；
4. deny 全部写入；
5. allow 当前目录 + `/tmp` + `/var/folders` + `fsWriteAllowExtra`；
6. `fsWriteDenyWithinAllow` 逐条 deny（因此同时出现在 allow 与 deny 里的路径最终是**被禁**的——"可写根里挖洞"语义）。

**Linux（bwrap，挂载参数按顺序生效、后者覆盖前者）：**

- `fsReadDeny` → 用空 tmpfs 覆盖该目录（命令看到的是空目录而非内容）；
- `fsReadAllowWithinDeny` → 在掩埋区上重新只读挂载；
- 当前目录与 `fsWriteAllowExtra` → 读写挂载；
- `fsWriteDenyWithinAllow` → 降级为只读挂载；
- 网络关闭 → `--unshare-net`。

## 跳过沙箱

- 模型可以在单次调用里传 `dangerously_disable_sandbox: true`——它跳过所有沙箱层（等价于不沙箱执行）。Bash 默认被权限系统视为破坏性工具、通常需要你确认，但沙箱列表本身无法在单次调用里被放宽——四个列表只在启动时由配置装配，模型传参只能收紧或整体跳过；
- **后台任务例外**：`run_in_background: true` 的命令（dev server、日志跟踪等）不套沙箱——这类场景需要网络与项目外访问，且发起时已经过你的确认；
- 沙箱工具缺失 / 不可用的平台上，命令以未沙箱模式运行，Bash 结果里会标注实际的沙箱模式，模型与你都能看到当前命令是否受约束。

## 相关命令速查

| 命令 | 作用 |
|------|------|
| `biu doctor` | 检查沙箱工具是否可用、打印合并后的规则条数 |
| `/permissions` | 查看权限规则与模式 |
| `/add-dir <path> --remember` | 追加可写工作目录（持久化） |
| `biu config validate` | 加载全部配置层并报告问题 |
