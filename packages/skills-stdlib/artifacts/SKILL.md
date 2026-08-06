---
name: artifacts
display_name: Artifacts
description: 生成并预览交互式 UI 组件、SVG 图形、图表与可视化内容。当用户需要可视化、仪表盘或可交互组件时使用。
icon: 🎨
permissions: []
---

# BiuMind Artifacts

Artifacts are **dedicated UI panels** that render visual or
interactive output outside the chat stream. They split complex
deliverables from the conversation so the user can read the chat
and inspect / refine the artefact independently.

## When to create an artifact

  - Interactive components (forms, dashboards, widgets)
  - Visual content (SVG, illustrations, diagrams)
  - HTML pages / landing pages / printable layouts
  - Charts / data visualisations the user will tweak
  - Anything the user is likely to iterate on or reuse

## When to stay inline

  - Code snippets shorter than ~30 lines → inline markdown ```fence
  - Documents / articles / explanations → markdown text
  - Trivial answers / math / one-shot replies
  - Meta-commentary about an artefact (e.g. "I'd change the colour to…")
  - Streaming partial content (artefacts render once finalised)

## Artefact kinds

  svg          static graphic / icon / illustration
  html         standalone HTML page; iframe-rendered
  react        React component (JSX, default Tailwind)
  chart        data viz spec (Vega-Lite / observable)
  table        structured grid (sortable, filterable)
  markdown     long-form doc rendered with TOC + nav

## Output protocol

When you decide to render an artefact, emit:

```
{
  "kind": "artifact",
  "type": "svg" | "html" | "react" | "chart" | "table" | "markdown",
  "title": "...",
  "content": "..."
}
```

The platform's chat renderer detects this shape and pops the
artefact panel automatically.

## Constraints

  - Default to ONE artefact per response. Multi-file deliverables
    can split, but only when the user explicitly asks for them.
  - Don't reference window-level globals in React artefacts — they
    run sandboxed.
  - Image / video / audio content → use App Center (artefacts are
    text-only renderable kinds).

User's request: $ARGS
