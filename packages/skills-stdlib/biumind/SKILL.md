---
name: biumind
display_name: BiuMind
description: BiuMind 平台总指南与任务路由。当用户首次接入、对平台能力不熟悉，或一个任务不知道该用哪个模块 / 工具 / skill 时使用。
icon: 🤖
permissions: []
---

# BiuMind 平台速查

You are operating inside BiuMind, a unified AI workplace. This skill
is the map: what the platform can do, which surface serves it, and
which bundled skill holds the detailed manual.

## Module map

Knowledge & files (brain service):

  Wiki      /v1/wiki/*        documents + block editor; AI surface is MCP (below)
  Graph     /v1/graph/*       knowledge graph (nodes / edges / related walk)
  Memory    /v1/memory/*      long-term per-user memory (recall / preference / habit)
  Notes     /v1/notes/*       quick notes + notebooks
  Search    POST /v1/search   RRF-fused hybrid search (BM25 + vector + web)
  Files     /v1/files/*       user file blobs (upload / download / presign)

Execution & apps:

  Sandbox     /v1/sandboxes/*   ephemeral cloud workstations (+ /{id}/exec)
  App Center  /v1/apps/*        first-class apps (RSS / Email / Stock / PPT)
  AIGC        /v1/models        image / video / avatar model catalog
              /v1/generations   submit + poll generation tasks

Two API surfaces:

  - MCP tools at POST /v1/mcp — canonical for AI agents (structured
    JSON-RPC envelopes); prefer these whenever one covers the call
  - REST endpoints above — for typed clients; call them from agent
    code only when no MCP tool exists

All modules share one identity / authz / billing layer: a single user
JWT works across every prefix, and usage is metered centrally.

## Which bundled skill to activate

This skill is only the map. For real work, skill.activate the manual:

  wiki           read / write / search wiki pages; ingest long articles
  memory         store + recall durable facts, preferences, habits
  graph          query the knowledge graph; extract nodes from content
  sandbox        create cloud workstations; run code at scale
  app-center     drive installed BiuApps (RSS / Email / Stock / PPT)
  artifacts      produce user-facing files (exports, downloads)
  skill-creator  author a new skill when none of the above fits

## Tool selection cheat sheet

Work backward from the artefact the user wants:

  document / page      → wiki.* MCP tools (activate wiki first)
  semantic recall      → memory.recall
  persist a fact       → memory.store (kind = recall | preference | habit)
  ad-hoc command       → bash
  skill-bundle script  → skill.exec_script
  file for the user    → skill.export_file (lands in user Files)

## When NOT to use a skill

  - Trivial one-shot questions ("what's 2+2") → answer directly
  - Quoting documentation → answer directly + cite
  - Anything where the user has already given you full context

Skills cost a tool round-trip. Default to direct answers when you
already know how.

User's context: $ARGS
