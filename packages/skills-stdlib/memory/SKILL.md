---
name: memory
display_name: 记忆
description: BiuMind 记忆系统（recall / preference / habit）。当用户希望持久记住某事、回忆之前提过的事实，或需要跨会话用户上下文时使用。
icon: 🧠
permissions: ["memory.recall"]
---

# BiuMind Memory tooling

Memory is the durable per-user knowledge that travels across all
sessions. Three kinds — picking the wrong one wastes context budget
and confuses recall.

## The three kinds

  recall      Single fact the user explicitly asked to remember.
              Examples: "remember 2026-03-05 we picked pgvector"
                        "save: my mom's birthday is March 12th"

  preference  User explicitly stated likes / formatting / process.
              Examples: "I prefer concise replies"
                        "always use Conventional Commits"
                        "no emojis in code comments"

  habit       Inferred recurring patterns (renamed from "skill" in
              2026-05; the runtime accepts kind=skill as a 90-day
              alias but new writes should use habit).
              Examples (you write these from observed behaviour, not
              user request): "user always commits with body lines
                              wrapped at 72 chars"

## Tool calls

  memory.recall(query, kind?, limit=10)
                  → returns ranked list of matches with score
  memory.store(content, kind, salience=0.5)
                  → persist one row; salience boosts ranking weight

## Decision tree

  user: "remember X"            → kind=recall, salience=0.7
  user: "I always Y"            → kind=preference, salience=0.9
  user behaved Z three times    → kind=habit, salience=0.5 (you decide)

## Hygiene

  - Don't store secrets (API keys, passwords) — Memory is per-org
    visible to admin; use Identity providers / SecureStorage on
    client.
  - Don't store one-shot context that won't matter next session.
  - Don't proactively store preferences — wait for the user to
    state one explicitly. Inferred *habits* are different (you can
    write those without asking) but only when the pattern is clear
    across multiple sessions.

User's request: $ARGS
