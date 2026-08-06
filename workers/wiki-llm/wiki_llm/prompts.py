"""Prompt builder for the wiki-llm pipeline.

P1-8 ships a single-stage prompt: the model is told to plan internally
(chain-of-thought hidden) then emit FILE blocks directly. Two-stage CoT
(separate analyze + generate calls) buys little for streaming
partial-save — the model already does the planning before the first
FILE block lands, and adding a round trip just delays time-to-first-page.

When the wiki gets large enough that the LLM needs a wiki-index in
context to avoid duplicates, we'll add stage 1 back. P2 territory.

The output contract this prompt enforces matches
``wiki_llm.domain.ingest_parse``:

  * the response BEGINS with ``---FILE:`` (no preamble, no apology)
  * each page is delimited by ``---FILE: <path>---`` /
    ``---END FILE---`` markers on their own lines
  * paths live under ``wiki/`` (concept / entity / source subtrees)
  * frontmatter is a strict YAML block bounded by ``---``
  * no trailing commentary after the last ``---END FILE---``

Failure to follow these rules makes the FILE-block parser drop pages
silently — the prompt's "FIRST CHARACTER MUST BE -" rule is what stops
the model from prefixing "Here are the wiki files:" which would push
the first FILE block out of position-1 and confuse downstream tooling.
"""

from __future__ import annotations


SYSTEM_PROMPT = (
    "You are a wiki maintainer. You take one source document and "
    "transform it into a set of structured wiki pages.\n"
    "Reason internally about how to split the source into entity / "
    "concept / source-summary pages, then emit ONLY the FILE blocks. "
    "Do not output chain-of-thought, planning, apology, or preamble — "
    "your entire response must consist of FILE blocks back-to-back."
)


# Concrete page template the model uses as exemplar. We embed it inside
# the user prompt rather than the system prompt because models weight
# user-prompt instructions higher when they're contradicted by training-
# data formatting habits (especially "I want to be helpful and explain
# what I'm about to do" tendencies).
_FILE_BLOCK_EXAMPLE = """\
---FILE: wiki/concepts/example-concept.md---
---
type: concept
title: Example Concept
created: 2026-01-01
updated: 2026-01-01
tags: [example]
---

# Example Concept

Body content goes here. Use [[wikilink]] syntax in the body for
cross-references between pages.
---END FILE---"""


def build_user_prompt(
    *,
    source_title: str,
    source_text: str,
) -> str:
    """Build the user message for one ingest task.

    The system prompt is constant; the user prompt carries the source
    payload + the strict output rules. Keeping rules out of the system
    prompt lets us tune them per-task in the future (e.g. include a
    project-specific schema as part of stage 1).
    """
    parts: list[str] = []

    parts.append(
        "Generate wiki pages from the source below. Output exactly "
        "the FILE blocks — nothing before, nothing after."
    )
    parts.append("")

    parts.append("## What to generate")
    parts.append("")
    parts.append(
        "1. A source summary page at "
        "`wiki/sources/<kebab-case-source-name>.md` summarising the "
        "input (200-400 words, key claims + provenance)."
    )
    parts.append(
        "2. Entity pages in `wiki/entities/<kebab-name>.md` for each "
        "named entity (people, products, organizations, datasets) "
        "that's substantive enough to warrant its own page."
    )
    parts.append(
        "3. Concept pages in `wiki/concepts/<kebab-name>.md` for each "
        "key concept worth its own dedicated page."
    )
    parts.append(
        "Skip topics that wouldn't earn a useful standalone page; "
        "minor mentions can stay as wikilinks inside other pages."
    )
    parts.append("")

    parts.append("## Frontmatter rules (STRICT — parser is anchored)")
    parts.append("")
    parts.append(
        "Every page begins with a YAML frontmatter block. Format rules, "
        "in order of importance:\n"
        "1. The VERY FIRST line of the page body MUST be exactly `---`.\n"
        "2. Each frontmatter line is a `key: value` pair on its own line.\n"
        "3. The frontmatter ends with another `---` line on its own.\n"
        "4. Arrays use inline form: `tags: [a, b, c]`.\n"
        "5. Wikilinks belong in the BODY only — never in frontmatter."
    )
    parts.append("")
    parts.append("Required frontmatter fields:")
    parts.append("  * type     — one of: source | entity | concept")
    parts.append("  * title    — string (quote it if it contains a colon)")
    parts.append("  * created  — date in YYYY-MM-DD form")
    parts.append("  * updated  — same as created")
    parts.append("  * tags     — array of bare strings, e.g. `tags: [ai, ml]`")
    parts.append("")

    parts.append("## Source")
    parts.append("")
    if source_title:
        parts.append(f"Title: {source_title}")
        parts.append("")
    parts.append("```")
    parts.append(source_text)
    parts.append("```")
    parts.append("")

    parts.append("## Output format")
    parts.append("")
    parts.append("Concrete example of one well-formed FILE block:")
    parts.append("")
    parts.append(_FILE_BLOCK_EXAMPLE)
    parts.append("")
    parts.append(
        "Emit MULTIPLE such blocks back-to-back — one per page. Use "
        "blank lines between blocks, no prose between them."
    )
    parts.append("")

    parts.append("## STRICT output requirements")
    parts.append("")
    parts.append(
        "1. The FIRST character of your response MUST be `-` (the start "
        "of `---FILE:`). No preamble.\n"
        "2. DO NOT echo the source. DO NOT explain your plan. Reason "
        "silently and emit the blocks.\n"
        "3. DO NOT output any text after the last `---END FILE---`.\n"
        "4. Use kebab-case filenames inside `wiki/{sources,entities,concepts}/`.\n"
        "5. Match the language of the source. If the source is in Chinese, "
        "every title and body MUST be in Chinese — no exceptions, "
        "including page names and section headings."
    )

    return "\n".join(parts)
