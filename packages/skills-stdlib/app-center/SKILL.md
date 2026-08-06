---
name: app-center
display_name: 应用中心
description: 调用 BiuMind 应用中心的一等公民应用（RSS / 邮件 / 股票 / PPT 等）。当用户请求映射到具体应用而非通用对话任务时使用。
icon: 💬
permissions: []
---

# BiuMind App Center tooling

App Center hosts the platform's first-class **小程序级 applications**
— each one bundles its own UI, backend, and data source. Skills are
parallel to App Center: a skill is a markdown package; an app is a
full mini-program. Don't confuse the two.

## When to invoke an app

User intent → matching app:

  "订阅 X" / "subscribe to feed"          → rss
  "总结本周邮件" / "email digest"          → email
  "分析 X 股票" / "stock analysis"         → stock
  "做个 PPT 关于 Y" / "slide deck"        → ppt
  "存到剪藏" / "clip this URL"             → webclip
  "提醒我" / "schedule reminder"          → tasks
  "翻译 X" / "translate"                   → translate

## API

  GET    /v1/apps                       list installed apps with manifests
  GET    /v1/apps/{name}                one app's manifest
  POST   /v1/apps/{name}/invoke         run an action: { action, input }

## Inbox of common actions

  rss / subscribe       { url }                    add a feed
  rss / summarize_feed  { feed_id, last_n }        summarise recent items
  email / digest        { since: "1d" }            generate a brief
  stock / quote         { ticker }                 latest price + chart hint
  ppt / outline         { topic, slide_count }     scaffold an outline

## When NOT to use App Center

  - Generic markdown writing → answer directly + propose a Wiki page
  - One-shot calculation / parsing → bash or runtime tools
  - Anything the user explicitly asked you (the agent) to do —
    don't redirect them to an app

## App vs Skill (don't conflate)

  App     full mini-program (frontend + backend + data source);
          installed via App Center; ships with its own UI
  Skill   markdown indirection (this file); just teaches the LLM
          how to drive existing tools

If a workflow needs UI, propose an App. If it's all behaviour, it's
a Skill.

User's request: $ARGS
