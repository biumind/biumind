# postgres/init —— 只做引导，不建表

这里的脚本在 Postgres **首次初始化**（空数据目录）时由官方镜像的
`/docker-entrypoint-initdb.d` 按文件名顺序执行。**只保留三个引导脚本**：

| 脚本 | 职责 |
|------|------|
| `00-extensions.sql` | 启用所有需要的扩展（pgvector / citext / ltree / pg_trgm …） |
| `01-schemas.sql`    | 建空 schema + 授权（service migration 依赖 schema 预存在） |
| `02-roles.sql`      | 默认权限 |

## ⚠️ 不要在这里加建表脚本

表结构是**每个服务自己的 goose migration**（`services/<svc>/migrations/`）的
唯一职责，服务启动时 `dbmigrate.Run` 自动 `goose up`。曾经这里有一批
`10-identity.sql` / `30-brain.sql` … 把表也预建了一遍，导致两个真实故障：

1. **与 goose baseline 漂移**：`identity` 用 `baselineMax=17`，预建 `identity.users`
   会误触发 baseline（本是给「有表但无 `goose_db_version`」的遗留生产库升级用的），
   把 1–17 标记为已应用跳过；而预建脚本早已陈旧、缺了 `credit_logs` 等表 →
   后续 migration `ALTER` 缺失表报 `42P01`。
2. **与 baseline=0 服务冲突**：`brain` / `runtime` 等空库直接 goose-up 全量，
   预建表会撞 `already exists`。

结论：**建表只走 goose，单一事实源，零漂移**。全新库（dev / 测试环境）由各服务
goose 从 00001 跑到最新即可建全。要改 schema → 写新的 goose migration，不要碰这里。
