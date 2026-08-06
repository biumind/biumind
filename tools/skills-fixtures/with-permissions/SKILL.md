---
name: with-permissions
description: Declares the permissions a skill needs so Authz can translate them to Cedar (PS3.4).
permissions: ["sandbox.exec", "network.fetch", "wiki.read"]
---

This skill declares it needs sandbox execution, outbound network, and
read access to the user's Wiki. Until PS3.4 lands these strings are
advisory metadata; afterwards Authz translates them into Cedar policies
that gate the matching builtin tools (`skill.exec_script` checks
sandbox.exec, etc.).

Skills that omit `permissions:` are treated as "read-only prompt
injection" — the high-risk tools soft-error when invoked from such a
skill's context.

Argument forwarded: `$ARGS`.
