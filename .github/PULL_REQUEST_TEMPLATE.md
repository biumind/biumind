## 改动说明 / Summary

<!-- 这个 PR 做了什么、为什么。如果有关联 issue 请链接。 -->
<!-- What & why. Link related issues. -->

## 类型 / Type

- [ ] feat（新功能）
- [ ] fix（缺陷修复）
- [ ] refactor（重构）
- [ ] docs（文档）
- [ ] test（测试）
- [ ] chore / ci

## 检查清单 / Checklist

- [ ] 本地 `task test` 通过
- [ ] 本地 `task lint` 通过（含 `task lint:invariants` 架构不变量）
- [ ] 仅 stage 本轮自己改的文件（未用 `git add -A` / `.` / `-u`）
- [ ] 跨服务 / 跨端 / proto / schema / events 改动已联动上下游（`go-sdk/biu` / `biumindkit` / Dart / TS）
- [ ] 后端改动已本地 `docker-compose` 重部署验证（`make build-images` → `up -d` → `tail` → `health`）
- [ ] [CHANGELOG.md](../CHANGELOG.md) 已更新（用户可见变更）
- [ ] 无密钥 / token / 内部域名泄漏（日志、截图、配置）

## 影响范围 / Impact

<!-- 破坏性变更？迁移？性能？安全？/ Breaking changes? Migrations? Perf? Security? -->
