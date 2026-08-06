# Contributing to BiuMind

感谢你有兴趣为 BiuMind 贡献代码！本文档说明开发约定与提交流程。

Thank you for your interest in contributing to BiuMind!

## 开发环境 / Setup

需要 / Requires：Go 1.26+、Flutter（stable）、buf、uv（Python）、Docker。

```bash
task bootstrap         # 一次性安装依赖（buf / goimports / dart / uv ...）
make up-infra          # cd deploy/docker-compose && make up-infra  起本地 PG/Redis/MinIO/NATS/SearXNG
task proto:generate    # buf 生成 Go / Dart / TS
task model-relay:run   # 起模型网关
task cli:install       # biu CLI 装到 ~/.local/bin
task test              # 全量（Go + Dart + Python）
task lint              # lint:go + lint:dart + lint:proto
task lint:invariants   # 架构不变量校验
task --list            # 完整任务列表
```

## 架构约定 / Architecture

仓库有一组架构约束由 `task lint:invariants`（CI 强制）校验，PR 必须通过。具体约束在 review 时反馈。

## 提交流程 / Pull request flow

1. Fork 仓库，从 `main` 切特性分支。
2. 写代码 + 测试。`task test` 与 `task lint` 必须本地通过。
3. Conventional Commits（`feat:` / `fix:` / `docs:` / `refactor:` / `test:` / `chore:` …）。**只 stage 本轮自己改的文件**（明确文件名 `git add <file>...`），不要 `git add -A`。
4. PR 描述说明：改了什么、为什么、影响范围、如何测试。勾选 PR 模板清单。
5. CI（go / flutter / lint / invariants）全绿后 review。

## 安全 / Security

发现安全漏洞请**不要**开公开 issue，按 [SECURITY.md](./SECURITY.md) 私下上报。

## 行为准则 / Code of Conduct

参与即代表同意遵守 [Code of Conduct](./CODE_OF_CONDUCT.md)。

## 许可证 / License

BiuMind 采用**分层许可**：

- **平台 / 产品**（`apps/client`、`apps/cli`、`apps/webclip`、`apps/miniapp`、`services/*`、`workers/*`、`packages/*`、`schema/`、`deploy/`）：**BiuMind Community License**（source-available）——允许个人/研究/教育与企业内部使用（含内部商用），但**禁止把 BiuMind 作为公开 SaaS / 托管服务提供给第三方**、禁止商业分发衍生品。详见根 [LICENSE](./LICENSE)。
- **SDK + 扩展**（`sdks/go`、`sdks/python`、`sdks/node`、`extensions/vscode`）：**Apache-2.0**——鼓励嵌入集成。

提交的贡献按上述许可授权（贡献者同意生产商可商用 + 可调整许可条款，见 LICENSE §5）。
