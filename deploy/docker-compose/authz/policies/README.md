# Authz 策略目录

## 加载顺序

按文件名字典序合并；后加载的不覆盖前面的（Cedar 是 union 语义：任一 permit 通过即放行，任一 forbid 命中即拒绝）。

```
00-system.cedar       系统级（编译进 binary，但测试环境从这里读方便迭代）
10-org-*.cedar        组织级（Workspace admin 写）
20-user-*.cedar       用户级（个人分享配置）
99-deny-default.cedar 兜底（无显式 permit 即拒）
```

## 测试

```bash
make authz-eval PRINCIPAL=user:u1 ACTION=wiki:Page::read RESOURCE=page:p1
```

会调用 `POST /v1/authz/check` 并显示决策 + 命中策略。

## 热加载

测试环境：改文件后 `docker compose restart authz` 即生效（启动时载入）。
生产环境：策略放 Postgres，LISTEN/NOTIFY 推变更，运行时增量编译。

## 字段约定

- `principal`：JWT claims 投影到 entity（`User`、`AgentVirtualKey`、`Service`）
- `resource`：服务调 Authz 时携带 entity 元数据（`Page` 含 `owner`/`project`/`share_mode`）
- `action`：`<service>:<resource>::<verb>` 三段命名
