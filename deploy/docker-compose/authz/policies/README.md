# Authz 策略目录

这个目录是 authz 服务的**唯一策略来源**，启动硬依赖：

- compose 以只读方式挂载到容器 `/etc/biumind/authz/policies`（见 `docker-compose.yml` 的 `authz` 服务）。
- authz 启动时读取该目录下所有 `*.cedar` 文件、按文件名字典序拼接后载入（`services/authz/internal/policies/loader.go`）。
- 目录缺失或没有任何 `.cedar` 文件，authz 直接退出（binary 内没有编译进去的策略副本）。

## 语义

Cedar 标准语义：默认拒绝（deny-by-default）；任一 permit 命中即放行，但任一 forbid 命中则拒绝（forbid 优先）。不需要"兜底拒绝"文件。

## 热加载

改文件后无需重建容器，二选一：

```bash
docker compose restart authz
# 或调用管理端点
curl -X POST http://localhost:7009/v1/authz/reload
```

## 文件

- `policies.cedar` — 全部授权规则，分四节：System（wiki / graph / hub / agent / sandbox / realtime topic）→ Skills → App Center → RSS org-scope。

授权逻辑只许放这里（I9：业务代码零授权逻辑）。新增规则时在对应小节追加，并在 `services/authz/internal/engine/` 补测试——测试直接读这个文件，保证部署与测试零漂移。
