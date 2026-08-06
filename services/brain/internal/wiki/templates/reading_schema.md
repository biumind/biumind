# 阅读项目 · 页面规范

本文件定义读书笔记项目的页面类型与约定。新建页面时按类型填写 frontmatter，并用 `[[反链]]` 关联相关页面。

## 页面类型

| type | 用途 |
|------|------|
| entity | 书中命名对象：地点 / 物件 / 组织 |
| concept | 抽象概念 / 世界观设定 |
| source | 引用来源：书评 / 访谈 / 参考资料 |
| query | 阅读中悬而未决的问题 |
| comparison | 人物或主题的横向对比 |
| synthesis | 跨章节综合结论 |
| overview | 本书总览（每书一篇） |
| character | 书中人物 |
| theme | 反复出现的主题 / 动机 / 象征 |
| plot-thread | 正在追踪的故事线 / 叙事弧 |
| chapter | 逐章笔记与摘要 |

## 命名约定

- slug 一律 `kebab-case`
- 人物用角色名，如 `elizabeth-bennet`
- 主题用名词短语，如 `阶级流动`、`诚实与欺骗`
- 情节线用弧线描述，如 `达西救赎弧`
- 章节用 `ch-NN-短语`，如 `ch-01-开场`

## frontmatter

每页必带：

```yaml
---
type: character
title: 可读标题
tags: []
related: []
created: 2026-07-31
updated: 2026-07-31
---
```

人物页另带 `first_appearance: Ch. N`、`role: protagonist|antagonist|supporting|minor`；章节页带 `chapter: N`、`pages: 1-24`。

## 反链规则

- 用 `[[slug]]` 链接页面
- 章节笔记用 `related` 关联本章出场人物
- 主题页链接到该主题最突出的章节
- 情节线页列出推进该弧的章节

## 阅读专项约定

- 章节页在阅读中或读后立即写，捕捉即时感受
- 区分情节摘要与个人解读
- 主题页追踪主题在全书中的「发展」，而非仅陈述其存在
- 未决情节线标 `status: open`，解决后再改
- 重要引文标注页码以便回找
