---
description: Hand a UI / UX task to the frontend-designer sub-agent
---
Dispatch the task in $ARGUMENTS to the `frontend-designer` sub-agent (defined by the frontend-design plugin).

Use the Agent tool with `subagent_type: "frontend-designer"` and pass the task verbatim. When the agent returns:

1. If the agent surfaced clarifying questions, relay them to the user — do not guess at the answers.
2. If the agent produced a diff, summarise the visible behaviour change in one or two sentences.
3. If the agent reported "tested with X" / "did NOT test the live UI", flag that — type-checking alone is not enough for visual work.

Do not modify files yourself. Your role here is dispatch + summary; the sub-agent owns the implementation.
