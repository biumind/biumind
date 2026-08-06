# 研究项目 · 页面规范

本文件定义本项目用到的页面类型与约定。新建页面时按类型填写 frontmatter，并用 `[[反链]]` 关联相关页面。

## 页面类型

| type | 用途 |
|------|------|
| entity | 命名实体：人物 / 工具 / 机构 / 数据集 |
| concept | 概念：思想 / 技术 / 现象 / 框架 |
| source | 来源：论文 / 文章 / 演讲 / 书籍 / 博客 |
| query | 待解问题，正在主动调查 |
| comparison | 相关实体的横向对比 |
| synthesis | 跨页面综合结论 |
| overview | 项目总览（每项目一篇） |
| thesis | 工作假设及其随证据的演化 |
| methodology | 研究方法 / 协议 / 实验设计 |
| finding | 单条实证结果或观察 |

## 命名约定

- slug 一律 `kebab-case`
- 来源用 `作者-年份-短语`，如 `wei-2022-cot`
- 问题用问句作 slug，如 `scale-是否提升推理`
- 假设用假设短语，如 `scaling-improves-reasoning`

## frontmatter

每页必带：

```yaml
---
type: thesis
title: 可读标题
tags: []
related: []
created: 2026-07-31
updated: 2026-07-31
---
```

来源页另带 `authors / year / url / venue`；假设页带 `confidence: low|medium|high` 与 `status: speculative|supported|refuted|settled`；发现页带 `source`（反链来源）、`confidence`、`replicated`。

## 反链规则

- 用 `[[slug]]` 链接页面，每个 entity / concept 都应在总览出现
- 发现页用 frontmatter `source` 回链其来源
- 假设页用 `related` 关联支持与反驳它的发现
- 来源相互矛盾时：在概念页标注 → 建问题页追踪 → 证据充分后在综述页定论

## 研究专项约定

- 假设页是活文档，证据累积后持续更新
- 每条发现尽量评估可复现性
- 方法论页解释「为什么」这么做，不只是「怎么做」
- 发现页区分直接证据与推断
