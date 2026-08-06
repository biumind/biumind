---
name: with-resources
description: Demonstrates a skill that ships bundled reference files under references/.
when-to-use: When the user asks how to write a skill that references its own bundle.
---

This skill bundles a reference file at `references/checklist.md` that the
agent is expected to read via `skill.read_reference` (PS2.4 wires the tool).

The body itself is short on purpose — most of the meat lives in the
reference. Pattern: keep SKILL.md instructions thin and push detail into
references/ so the prompt budget stays modest.

Argument: `$ARGS`.
