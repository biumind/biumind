"""YAML frontmatter parsing for wiki pages.

LLM-generated wiki pages prepend a YAML frontmatter block::

    ---
    title: RoPE
    related:
      - rotary-embedding
      - transformers
    ---

    # Body starts here

This module turns that into a flat ``dict[str, str | list[str]]`` so
the brain page writer can fan it into ``brain.pages.frontmatter`` (jsonb)
and the editor surface.

Three real-world resilience features kept from llm_wiki:

  1. Strict locator first (``---\\n…---``) anchored at start; if absent,
     a fallback locator scans for the FIRST block whose opener sits in
     the top few lines, recovering from LLM-corrupted pages that prefix
     a stray code-fence wrapper or a misformatted nested-document
     attempt.

  2. Wikilink-list repair: LLMs sometimes emit
     ``related: [[a]], [[b]]`` which is not valid YAML. We retry the
     parse once after wrapping such lines into quoted flow arrays
     (``related: ["[[a]]", "[[b]]"]``). Only that exact shape gets
     touched — legitimate nested arrays are left alone.

  3. ``raw_block`` is returned alongside the parsed dict so callers
     editing only the body (e.g. WikiEditor PUT) can write back
     ``raw_block + body`` and preserve the user's exact YAML serialization
     including comments and key order.

Re-implemented from llm_wiki ``frontmatter.ts`` under Apache-2.0; the
contract is the regex shapes + the LLM repair behaviour, not the
implementation.
"""

from __future__ import annotations

import json
import re
from dataclasses import dataclass
from typing import Any

import yaml


# Frontmatter values are flattened to strings or lists of strings — the
# UI panel can't render arbitrary nested objects, and a YAML date / int
# is more useful as its string form for display + writeback symmetry.
FrontmatterValue = str | list[str]


@dataclass(frozen=True)
class FrontmatterParseResult:
    frontmatter: dict[str, FrontmatterValue] | None
    body: str
    raw_block: str


# Anchored: the strict shape we prefer. Both fence lines on their own
# line; YAML payload between is delegated to PyYAML.
_FM_STRICT_RE = re.compile(r"^---[ \t]*\r?\n(.*?)\r?\n---[ \t]*(?:\r?\n|$)", re.DOTALL)

# Unanchored fallback: same shape but allowed anywhere. Lazy match means
# the FIRST closing `---` wins, but the OPENING `---` must sit within
# the first few lines (see _MAX_PREFIX_LINES) so a `---` horizontal-rule
# section divider deep in the body can't be mistaken for frontmatter.
_FM_FALLBACK_RE = re.compile(r"\n---[ \t]*\r?\n(.*?)\r?\n---[ \t]*(?:\r?\n|$)", re.DOTALL)
_MAX_PREFIX_LINES_BEFORE_FRONTMATTER = 6

# Code-fence prefix that the fallback recovery should also strip from
# the body so an orphan closing ``` doesn't render as raw source after
# the frontmatter block.
_YAML_FENCE_PREFIX_RE = re.compile(r"^\s*```(?:yaml|yml)?\s*\r?\n$", re.IGNORECASE)
_TRAILING_FENCE_RE = re.compile(r"^\s*```\s*(?:\r?\n|$)")

# Wikilink-list repair pattern: matches a single line of the form
# ``key: [[a]], [[b]], [[c]]`` (two or more wikilinks separated by
# commas) and only that exact shape — anything else passes through.
_WIKILINK_LIST_LINE_RE = re.compile(
    r"^(\s*[A-Za-z_][\w-]*\s*:\s*)"
    r"(\[\[[^\]]+\]\](?:\s*,\s*\[\[[^\]]+\]\])+)\s*$",
)


def parse_frontmatter(content: str) -> FrontmatterParseResult:
    """Parse a YAML frontmatter block from the head of `content`.

    Always returns a result; on absence or unrecoverable parse error the
    `frontmatter` field is None and `body` is the original input.
    """
    located = _locate_block(content)
    if located is None:
        return FrontmatterParseResult(frontmatter=None, body=content, raw_block="")

    yaml_payload, raw_block, body = located

    # Two-pass parse: try as-is, then run wikilink-list repair once.
    parsed: Any
    try:
        parsed = yaml.safe_load(yaml_payload)
    except yaml.YAMLError:
        try:
            parsed = yaml.safe_load(_repair_wikilink_lists(yaml_payload))
        except yaml.YAMLError:
            return FrontmatterParseResult(frontmatter=None, body=body, raw_block=raw_block)

    return FrontmatterParseResult(
        frontmatter=_normalize(parsed),
        body=body,
        raw_block=raw_block,
    )


def _locate_block(content: str) -> tuple[str, str, str] | None:
    """Locate the frontmatter block, return ``(yaml_payload, raw_block, body)``.

    Tries the anchored shape first, falls back to a window search.
    Returns None when neither matches.
    """
    strict = _FM_STRICT_RE.match(content)
    if strict is not None:
        return strict.group(1), strict.group(0), content[strict.end():]

    fallback = _FM_FALLBACK_RE.search(content)
    if fallback is None:
        return None

    open_idx = fallback.start() + 1  # past the leading \n that the regex anchored on
    if _line_number_at(content, open_idx) > _MAX_PREFIX_LINES_BEFORE_FRONTMATTER:
        return None

    raw_block_len = fallback.end() - fallback.start() - 1  # drop the leading \n
    raw_block = content[open_idx:open_idx + raw_block_len]
    body_after = content[open_idx + raw_block_len:]

    # If the prefix is exactly a `\`\`\`yaml`-style code fence opener,
    # strip the matching closing fence at the head of the body so an
    # orphan ``` doesn't break the renderer.
    prefix = content[:open_idx]
    if _YAML_FENCE_PREFIX_RE.match(prefix):
        stripped = _TRAILING_FENCE_RE.sub("", body_after, count=1)
        return fallback.group(1), raw_block, stripped

    return fallback.group(1), raw_block, body_after


def _line_number_at(s: str, index: int) -> int:
    """1-based line number that `index` sits on within `s`."""
    if index <= 0:
        return 1
    return s.count("\n", 0, index) + 1


def _repair_wikilink_lists(payload: str) -> str:
    """Wrap unbracketed wikilink lists into quoted flow arrays.

    Touches only lines that match the exact shape; any other YAML
    (including legitimate nested arrays like ``tags: [[red, blue], [green]]``)
    passes through unchanged.
    """
    out_lines: list[str] = []
    for line in payload.split("\n"):
        m = _WIKILINK_LIST_LINE_RE.match(line)
        if m is None:
            out_lines.append(line)
            continue
        prefix = m.group(1)
        items = [s.strip() for s in m.group(2).split(",") if s.strip()]
        quoted = ", ".join(f'"{s}"' for s in items)
        out_lines.append(f"{prefix}[{quoted}]")
    return "\n".join(out_lines)


def _normalize(parsed: Any) -> dict[str, FrontmatterValue] | None:
    """Coerce PyYAML's loose output into the flat frontmatter shape.

    Top-level non-dict (None / list / scalar) returns None — there's no
    sensible projection. Inside a dict, non-string scalars become str()'d
    and nested objects are JSON'd so they remain visible to the user
    rather than disappearing silently.
    """
    if not isinstance(parsed, dict):
        return None
    out: dict[str, FrontmatterValue] = {}
    for key, value in parsed.items():
        # YAML may emit non-string keys (numbers, bools); coerce to str.
        skey = str(key)
        if isinstance(value, list):
            out[skey] = [_stringify_scalar(v) for v in value]
        else:
            out[skey] = _stringify_scalar(value)
    return out


def _stringify_scalar(v: Any) -> str:
    if v is None:
        return ""
    if isinstance(v, str):
        return v
    if isinstance(v, bool):
        # Must precede int branch: bool is an int subclass in Python.
        return "true" if v else "false"
    if isinstance(v, (int, float)):
        return str(v)
    # date / datetime — ISO string limited to the date portion (matches
    # the JS Date.toISOString().slice(0, 10) behaviour from llm_wiki).
    iso = getattr(v, "isoformat", None)
    if callable(iso):
        try:
            return iso()[:10]
        except (TypeError, ValueError):
            pass
    # Object / nested array → JSON so the user still sees something.
    try:
        return json.dumps(v, ensure_ascii=False, default=str)
    except (TypeError, ValueError):
        return str(v)
