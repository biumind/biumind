---
name: biumind
display_name: BiuMind
description: BiuMind 平台总指南。当用户首次接入或对平台能力不熟悉，需要概览所有核心模块入口和最佳实践时使用。
icon: 🤖
permissions: []
---

# BiuMind 平台速查

You are operating inside BiuMind, a unified AI workplace. This skill
gives you the lay of the land so you don't have to discover each
subsystem the hard way.

## Five core modules

  Wiki         /v1/wiki/*               documents + block editor + multi-view DB
  Graph        /v1/graph/*              knowledge graph (nodes + edges + clustering)
  Memory       /v1/memory/*             long-term memory (recall / preference / habit)
  Sandbox      /v1/sandboxes/*          ephemeral cloud workstations (gVisor / Firecracker)
  App Center   /v1/apps/*               first-class apps (RSS / Email / Stock / PPT)

All five share one identity / authz / billing layer (see Authz §1.2).

## Tool selection cheat sheet

When the user task lands, work backward from the artefact they want:

  document / page → wiki tools (read / write / search)
  semantic recall → memory.recall
  user habit / preference → memory tools with kind=preference|habit
  code execution → bash (ad-hoc) or skill.exec_script (skill bundle)
  external file → skill.export_file (writes to user Files)

## When NOT to use a skill

  - Trivial one-shot questions ("what's 2+2") → answer directly
  - Quoting documentation → answer directly + cite
  - Anything where the user has already given you full context

Skills cost a tool round-trip. Default to direct answers when you
already know how.

User's context: $ARGS
