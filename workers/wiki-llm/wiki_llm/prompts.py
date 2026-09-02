"""Prompt builders for the wiki-llm pipeline.

P2 #17 ships the two-stage CoT pipeline (mirroring reference llm_wiki's
ingest.ts, reimplemented — no code forked):

  * Stage 1 — ``build_analyze_prompt``: the model reads the source
    TOGETHER with the project's context (purpose page, schema page,
    existing page index) and emits a structured analysis: entities,
    concepts, relations to existing pages, potential conflicts, and a
    suggested page split. Non-streaming; capped output length.
  * Stage 2 — ``build_user_prompt``: the model receives the stage-1
    analysis plus the same wiki context and emits the FILE blocks
    (streaming partial-save unchanged).

The single-stage path (``build_user_prompt`` without analysis/context)
is kept for the BIUMIND_WIKI_LLM_TWO_STAGE=0 kill-switch.

The output contract stage 2 enforces matches
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

from typing import Optional

from .brain import IngestContext


# ─── Stage 1: analysis ─────────────────────────────────────────────

ANALYZE_SYSTEM_PROMPT = (
    "You are a wiki analyst. You read one source document plus the "
    "current state of the wiki it will be ingested into, and produce a "
    "structured analysis that a downstream writer stage will follow to "
    "generate wiki pages. Output ONLY the analysis sections — no "
    "preamble, no commentary."
)

# Fixed section headings of the analysis. The runner does NOT parse the
# analysis — it is fed verbatim into the stage-2 prompt — so the
# structure exists to discipline the model, not to satisfy a parser.
# Keeping it prose-in-fixed-sections (instead of strict JSON) removes a
# whole failure class (JSON truncation / fence wrapping) from the
# quality gate.
_ANALYZE_SECTIONS = """\
## Entities
Named entities (people, products, organizations, datasets) substantive
enough to warrant their own page — one line each, with a few words of
disambiguation.

## Concepts
Key concepts worth a dedicated page — one line each.

## Related existing pages
Which pages from the existing-wiki index this source touches, BY EXACT
TITLE from the index — and how (extends / contradicts / duplicates).
Only list titles that appear in the index; write "none" when unrelated.

## Potential conflicts
Contradictions or heavy overlaps between this source and the existing
wiki (or the project's purpose/schema). Write "none" if clean.

## Suggested page split
The final list of pages the writer stage should produce — one line per
page: `wiki/{sources,entities,concepts}/<kebab-name>.md — one-line
scope`. Follow the project's schema conventions when they exist."""


def build_analyze_prompt(
    *,
    source_title: str,
    source_text: str,
    context: Optional[IngestContext] = None,
) -> str:
    """Build the stage-1 (analysis) user message.

    Carries the wiki context (purpose / schema / existing page index)
    so the analysis can ground relations and conflicts in real pages,
    plus the source itself. Output length is capped by instruction
    (~600 words) — stage 1 doubles token cost per task, so its output
    budget is deliberately tight (the runner also uses a smaller
    max_tokens for this call).
    """
    parts: list[str] = []

    parts.append(
        "Analyze the source below for ingestion into a wiki. Produce "
        "EXACTLY these sections, in this order, with these headings:"
    )
    parts.append("")
    parts.append(_ANALYZE_SECTIONS)
    parts.append("")
    parts.append(
        "Rules:\n"
        "1. Total output ≤ 600 words. Be terse — one line per item.\n"
        "2. Match the language of the source (Chinese source → Chinese "
        "analysis, including page titles).\n"
        "3. Ground every claim in the source or the wiki context — do "
        "not invent pages or facts.\n"
        "4. Page paths use kebab-case under "
        "`wiki/{sources,entities,concepts}/`."
    )
    parts.append("")

    _append_wiki_context(parts, context)

    parts.append("## Source")
    parts.append("")
    if source_title:
        parts.append(f"Title: {source_title}")
        parts.append("")
    parts.append("```")
    parts.append(source_text)
    parts.append("```")

    return "\n".join(parts)


# ─── Stage 2: FILE-block generation ────────────────────────────────

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
    analysis: Optional[str] = None,
    context: Optional[IngestContext] = None,
) -> str:
    """Build the stage-2 (generation) user message.

    ``analysis`` (stage-1 output) and ``context`` (project purpose /
    schema / page index) are None on the legacy single-stage path —
    the prompt then degrades to the P1-8 shape exactly.
    """
    parts: list[str] = []

    parts.append(
        "Generate wiki pages from the source below. Output exactly "
        "the FILE blocks — nothing before, nothing after."
    )
    parts.append("")

    _append_wiki_context(parts, context)

    parts.append("## What to generate")
    parts.append("")
    if analysis:
        parts.append(
            "Follow the planning analysis below for the page split, "
            "the pages to create, and the linking decisions. It was "
            "produced from the same source with full wiki context — "
            "treat its suggested page split as the authoritative plan, "
            "deviating only when a suggested page would be empty or "
            "redundant."
        )
    else:
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
    if context and context.pages:
        parts.append(
            "Do NOT create a page that duplicates an existing one from "
            "the index above — link to it with `[[Exact Title]]` "
            "instead. When your body mentions a topic that has an "
            "existing page, always wikilink it by its exact title."
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

    if analysis:
        parts.append("## Planning analysis (from stage 1)")
        parts.append("")
        parts.append(analysis.strip())
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


# ─── shared context section ────────────────────────────────────────

def _append_wiki_context(parts: list[str],
                         context: Optional[IngestContext]) -> None:
    """Append the project's wiki context sections (purpose / schema /
    existing page index) to a prompt under construction.

    Both stages share this so stage 1's relations/conflicts and stage
    2's wikilinks are grounded in the same snapshot. Sections with no
    content are omitted entirely (blank-template projects have no
    purpose/schema pages).
    """
    if context is None:
        return
    if context.purpose:
        parts.append("## Wiki purpose")
        parts.append("")
        parts.append(
            "The project owner declared this wiki's purpose as follows "
            "— new pages must serve it:"
        )
        parts.append("")
        parts.append(context.purpose.strip())
        parts.append("")
    if context.schema:
        parts.append("## Wiki schema (page conventions)")
        parts.append("")
        parts.append(
            "These are the project's page-type conventions — follow "
            "them when deciding which pages to create and how to name "
            "them:"
        )
        parts.append("")
        parts.append(context.schema.strip())
        parts.append("")
    if context.pages:
        parts.append("## Existing wiki pages (index)")
        parts.append("")
        parts.append(
            "The wiki already contains these pages (`title (type)`). "
            "Reference them by EXACT title:"
        )
        parts.append("")
        for title, typ in context.pages:
            parts.append(f"- {title} ({typ})" if typ else f"- {title}")
        if context.pages_total > len(context.pages):
            parts.append(
                f"- … and {context.pages_total - len(context.pages)} more"
            )
        parts.append("")
