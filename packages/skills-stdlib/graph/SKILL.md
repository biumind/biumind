---
name: graph
display_name: 知识图谱
description: BiuMind 知识图谱操作。当用户想梳理实体关系、追踪依赖，或从积累的笔记里分析主题全景时使用。
icon: 🌐
permissions: ["wiki.read"]
---

# BiuMind Graph tooling

Graph is automatically built from Wiki + chat + Memory via the
ingest worker. Its strength is finding **non-obvious connections**
the user accumulated over time but never explicitly linked.

## Endpoints

  GET  /v1/graph/projects/{id}/nodes?type=...&limit=...   node list (filter by type)
  GET  /v1/graph/nodes/{id}                                one node + neighbours
  GET  /v1/graph/nodes/{id}/path?to={otherId}              shortest path between two nodes
  GET  /v1/graph/projects/{id}/insights                    Louvain-clustered topic clusters
  GET  /v1/graph/search?q=...                              fuzzy node lookup

## Node types

  Page        a Wiki page
  Block       one block within a page
  Chat        a chat message
  Topic       inferred cluster from Louvain
  Person      named entity (extracted from content)
  Concept     domain concept (e.g. "TypeScript", "DDD")
  Source      external URL or file

## When to call which

  user: "how are React and Vue mentioned together in my notes?"
    → /v1/graph/search?q=React → take node id → /v1/graph/nodes/{id}/path?to=<vue-id>

  user: "what topics am I writing about lately?"
    → /v1/graph/projects/{id}/insights → return top-N clusters with node counts

  user: "who do I keep mentioning in 1:1 docs?"
    → /v1/graph/projects/{id}/nodes?type=Person + sort by neighbour count

## Caveats

  - Graph is eventually consistent — recent Wiki edits show up after
    the ingest worker catches up (~30s).
  - Edges have weights based on co-occurrence frequency. Don't
    assume an edge means "user explicitly linked these".
  - For exact text matches, prefer wiki.search; graph is for
    *connections*, not retrieval.

User's request: $ARGS
