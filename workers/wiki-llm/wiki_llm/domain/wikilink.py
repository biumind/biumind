"""Convert Obsidian-style wikilinks into commonmark links.

Input:  free-form markdown body that may contain ``[[target]]`` and
        ``[[target|alias]]`` references.
Output: same body with each wikilink rewritten as ``[label](#target)``,
        EXCEPT inside fenced code blocks (\\`\\`\\`…\\`\\`\\`) and inline
        code spans (\\`…\\`) which pass through untouched so wikilinks
        shown as code examples in documentation aren't mangled.

The fragment-href form (``#target``) keeps clicks in-app on any
renderer; the brain-side wiki UI can wire `<a href="#…">` → page
navigation later. For now the goal is just to stop wikilinks from
rendering as raw bracket syntax.

Re-implemented from llm_wiki ``wikilink-transform.ts`` under Apache-2.0.
Pure function; no I/O.
"""

from __future__ import annotations

import re
from urllib.parse import quote


# Match `[[target]]` or `[[target|alias]]`. Both halves stop at `]`,
# `|`, or a newline so a stray bracket / pipe in the surrounding text
# can't span across paragraphs. ``+`` (not ``*``) on target keeps empty
# `[[]]` from being rewritten — that's just garbage and stays as-is.
_WIKILINK_RE = re.compile(r"\[\[([^\]|\n]+)(?:\|([^\]\n]*))?\]\]")

# Splitter for fenced code blocks. Captures the whole fenced section so
# the output preserves the fence marker.
_FENCE_SPLIT_RE = re.compile(r"(```.*?```)", re.DOTALL)

# Splitter for inline code spans within prose. A backticked run with no
# embedded backtick or newline; spans are short by convention so this
# greedy-ish match is fine.
_INLINE_CODE_SPLIT_RE = re.compile(r"(`[^`\n]+`)")


def transform_wikilinks(body: str) -> str:
    """Rewrite all wikilinks in `body` to commonmark links.

    Returns the transformed body. Inputs without `[[` are returned
    unchanged for the common-case fast path (typical wiki page may have
    no wikilinks at all).
    """
    if "[[" not in body:
        return body

    # Split on triple-backtick fences. Odd-index parts are inside a
    # fence and pass through; even-index parts are prose.
    parts = _FENCE_SPLIT_RE.split(body)
    return "".join(
        part if (idx % 2) == 1 else _transform_outside_fence(part)
        for idx, part in enumerate(parts)
    )


def _transform_outside_fence(text: str) -> str:
    if "[[" not in text:
        return text
    # Inline code spans get the same odd/even pass-through treatment.
    parts = _INLINE_CODE_SPLIT_RE.split(text)
    return "".join(
        part if (idx % 2) == 1 else _replace_wikilinks(part)
        for idx, part in enumerate(parts)
    )


def _replace_wikilinks(text: str) -> str:
    return _WIKILINK_RE.sub(_render_one, text)


def _render_one(match: re.Match[str]) -> str:
    raw_target = match.group(1).strip()
    raw_alias = match.group(2)
    alias = raw_alias.strip() if raw_alias is not None else ""
    label = alias if alias else raw_target

    # Encode the target so spaces / parens / hashes survive the markdown
    # parser. quote() with empty safe-set is `encodeURIComponent`-equivalent
    # for ASCII; non-ASCII is %-encoded too.
    href = "#" + quote(raw_target, safe="")
    # Escape any brackets in the visible label that would otherwise
    # terminate the markdown link text.
    escaped = label.replace("[", r"\[").replace("]", r"\]")
    return f"[{escaped}]({href})"
