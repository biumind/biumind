# 商务团队项目 · 页面规范

本文件定义团队知识库的页面类型与约定。新建页面时按类型填写 frontmatter，并用 `[[反链]]` 关联相关页面。

## 页面类型

| type | 用途 |
|------|------|
| entity | 产品 / 系统 / 服务等命名对象 |
| concept | 流程 / 模式 / 框架 |
| source | 引用来源：规格 / 外部文档 / 链接 |
| query | 团队开放问题 |
| comparison | 方案横向对比（选型） |
| synthesis | 跨项目 / 跨决策综合结论 |
| overview | 团队 / 项目总览 |
| index | 项目页面索引（起手 seed，每项目一篇） |
| log | 项目变更日志（起手 seed，每项目一篇） |
| meeting | 会议纪要 / 议程 / 行动项 |
| decision | 架构或战略决策（ADR 风格） |
| project | 项目简报 / 状态 / 复盘 |
| stakeholder | 涉及的人员 / 团队 / 组织 |

## 命名约定

- slug 一律 `kebab-case`
- 会议用 `YYYY-MM-DD-短语`，如 `2026-07-31-迭代规划`
- 决策用 `NNN-短语`，如 `001-采用-typescript`
- 项目用描述性 slug，如 `支付重构`
- 干系人用姓名或团队名，如 `张三`、`平台组`

## frontmatter

每页必带：

```yaml
---
type: meeting
title: 可读标题
tags: []
related: []
created: 2026-07-31
updated: 2026-07-31
---
```

会议页另带 `date`、`attendees: []`、`action_items: []`；决策页带 `status: proposed|accepted|deprecated|superseded`、`deciders: []`、`date`、`supersedes`（被替代的决策 slug）；项目页带 `status: planned|active|on-hold|complete|cancelled`、`owner`、`start_date`、`target_date`。

## 反链规则

- 用 `[[slug]]` 链接页面
- 会议纪要用 `attendees` frontmatter + `[[干系人]]` 链接与会者
- 决策页链接到讨论它的会议
- 项目页用 `related` 关联关键决策
- 干系人页列出其参与的项目与决策

## 商务专项约定

- 会议纪要在 24 小时内写完——记忆衰减很快
- 行动项必须有具名负责人与截止日期才算可执行
- 决策页记录「背景与后果」，不只是决策本身
- 废弃决策应链接到取代它的新决策
- 项目完成时补写复盘章节
