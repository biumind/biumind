# 个人成长项目 · 页面规范

本文件定义个人成长 / 自我管理项目的页面类型与约定。新建页面时按类型填写 frontmatter，并用 `[[反链]]` 关联相关页面。

## 页面类型

| type | 用途 |
|------|------|
| entity | 相关人物 / 工具 / 资源 |
| concept | 理念 / 方法论 / 框架 |
| source | 引用来源：书 / 文章 / 课程 |
| query | 自我探索中的开放问题 |
| comparison | 方案 / 习惯的横向对比 |
| synthesis | 跨周期综合洞察 |
| overview | 成长总览（每项目一篇） |
| goal | 具体想达成的结果 |
| habit | 重复性行为及其追踪 |
| reflection | 阶段性回顾与教训 |
| journal | 自由形式的日记 / 单次记录 |

## 命名约定

- slug 一律 `kebab-case`
- 目标用结果短语，如 `跑完马拉松`、`学会西班牙语`
- 习惯用行为名，如 `每日冥想`、`晨间书写`
- 回顾用 `类型-日期`，如 `周记-2026-07`、`季度-2026-q3`
- 日记用日期，如 `2026-07-31`

## frontmatter

每页必带：

```yaml
---
type: goal
title: 可读标题
tags: []
related: []
created: 2026-07-31
updated: 2026-07-31
---
```

目标页另带 `target_date`、`status: active|paused|achieved|abandoned`、`progress: 0-100`；习惯页带 `frequency: daily|weekly|monthly`、`streak`、`status`；回顾页带 `period: weekly|monthly|quarterly|annual`。

## 反链规则

- 用 `[[slug]]` 链接页面
- 回顾页关联本期复盘的目标与习惯
- 目标页用 `related` 链接支撑它的习惯
- 日记可内联 `[[slug]]` 引用目标 / 回顾

## 个人成长约定

- 日记与回顾对自己诚实——这个 wiki 是给你自己的，不是给观众
- 定期更新目标进度字段；过时数据比没数据更糟
- 区分结果目标（想要什么）与过程目标（将做什么）
- 复盘习惯成败的「为什么」，而不只是「是否」
- 用 synthesis 类型记录跨多个目标 / 周期的洞察
