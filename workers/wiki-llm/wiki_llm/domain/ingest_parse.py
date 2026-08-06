"""Streaming FILE-block parser for the wiki-llm protocol.

The LLM emits zero or more wiki pages as
``---FILE: <path>---`` / ``---END FILE---`` delimited blocks. The
parser must:

  1. Be tolerant of whitespace, case, and CRLF line endings
  2. Recognise marker lines ONLY when they sit on their own line —
     literal markers quoted in prose or fenced code blocks must pass
     through as body text (the LLM legitimately writes a "what is the
     ingest format" page that quotes the markers in code examples)
  3. Honour CommonMark fence-length matching — a 3-tick fence cannot
     close a 4-tick opener, so the parser keeps state about which fence
     it's inside (char + length)
  4. Surface stream truncation as a warning: when the input stops
     mid-block, the partial block is dropped but a warning is emitted
     so the worker can flip the task to ``failed`` instead of silently
     half-saving
  5. Drop blocks whose path fails the ``is_safe_ingest_path`` whitelist,
     surfacing them as warnings — this is the runtime defense against
     prompt-injection-driven path traversal

Re-implemented from llm_wiki ``parseFileBlocks`` (in ``ingest.ts``)
under Apache-2.0; the contract is its ingest-parse regression suite.

Why this is a single-shot parser, not a streaming consumer:
  Streaming partial-save in the wiki-llm worker means receiving an
  in-progress LLM stream and emitting each completed block as it
  closes. We accomplish that by passing the *cumulative* buffer so far
  to ``parse_file_blocks`` after each chunk arrives — the parser
  re-runs from the top, but on identical prefix input the output is
  identical, so the worker emits only the *new* blocks since the last
  call. This is O(n²) in chunk count, which is fine because n stays
  small (a typical CoT response emits 5-15 blocks). When that stops
  being true we'll move to a true incremental parser.
"""

from __future__ import annotations

import re
from dataclasses import dataclass

from .path_safety import is_safe_ingest_path


@dataclass(frozen=True)
class FileBlock:
    """One ``---FILE: path---`` … ``---END FILE---`` block."""

    path: str
    content: str


@dataclass(frozen=True)
class ParseResult:
    blocks: list[FileBlock]
    warnings: list[str]


# Opener: ``^---\s*FILE\s*:\s*<path>\s*---\s*$`` — the line must start
# with ``---`` (no leading whitespace allowed; otherwise an indented
# quote of the marker in prose would match), but inner whitespace and
# trailing whitespace are tolerated for LLMs that pretty-print.
_OPENER_RE = re.compile(r"^---\s*FILE\s*:\s*(.+?)\s*---\s*$", re.IGNORECASE)

# Closer: same line-anchored shape. Case-insensitive matches ``END FILE``
# in any casing the LLM emits.
_CLOSER_RE = re.compile(r"^---\s*END\s+FILE\s*---\s*$", re.IGNORECASE)

# Fence opener: 3+ backticks or tildes at line start. Everything after
# the fence run is the optional info string (e.g. ``python``); we don't
# care about its content, only that the line opens a fence.
_FENCE_OPEN_RE = re.compile(r"^(`{3,}|~{3,})")


def parse_file_blocks(text: str) -> ParseResult:
    """Extract all complete FILE blocks from `text`.

    Always returns a result; partial / unsafe blocks become warnings.
    """
    if not text:
        return ParseResult(blocks=[], warnings=[])

    # CRLF / lone-CR normalization. We do this once up front so every
    # subsequent state machine transition assumes ``\n`` line endings.
    normalized = text.replace("\r\n", "\n").replace("\r", "\n")
    lines = normalized.split("\n")

    blocks: list[FileBlock] = []
    warnings: list[str] = []

    state = "outside"          # "outside" | "block" | "block_in_fence"
    cur_path: str | None = None
    cur_lines: list[str] = []
    fence_char: str | None = None
    fence_len = 0

    for line in lines:
        if state == "outside":
            m = _OPENER_RE.match(line)
            if m is not None:
                cur_path = m.group(1).strip()
                cur_lines = []
                state = "block"
            # else: preamble prose, ignored
            continue

        if state == "block":
            # Fence opener takes priority — once we're inside a fence,
            # marker recognition pauses entirely.
            fm = _FENCE_OPEN_RE.match(line)
            if fm is not None:
                fence_marker = fm.group(1)
                fence_char = fence_marker[0]
                fence_len = len(fence_marker)
                cur_lines.append(line)
                state = "block_in_fence"
                continue
            if _CLOSER_RE.match(line) is not None:
                content = "\n".join(cur_lines)
                _emit_block(cur_path or "", content, blocks, warnings)
                state = "outside"
                cur_path = None
                cur_lines = []
                continue
            cur_lines.append(line)
            continue

        # state == "block_in_fence": pass everything through to body and
        # only watch for a matching closer (same char, length ≥ open len,
        # only whitespace after).
        cur_lines.append(line)
        if fence_char is not None and _is_fence_close(line, fence_char, fence_len):
            state = "block"
            fence_char = None
            fence_len = 0

    # Stream ended. An open block is a truncation: emit warning, drop
    # the partial content (we can't fabricate the rest the LLM never
    # sent; surfacing the drop is the next-best signal).
    if state in ("block", "block_in_fence") and cur_path is not None:
        warnings.append(
            f'block "{cur_path}" not closed (stream truncated)'
        )

    return ParseResult(blocks=blocks, warnings=warnings)


def _emit_block(
    path: str,
    content: str,
    blocks: list[FileBlock],
    warnings: list[str],
) -> None:
    """Validate + emit one closed block, or surface a warning."""
    if not path:
        warnings.append("empty path on FILE block — dropped")
        return
    if not is_safe_ingest_path(path):
        warnings.append(f'unsafe path: {path} — dropped')
        return
    blocks.append(FileBlock(path=path, content=content))


def _is_fence_close(line: str, char: str, length: int) -> bool:
    """A line closes a fence iff it's `length`+ of `char` then whitespace.

    We compute this without a per-call regex so the inner loop stays
    cheap on long pages — a fence body of 1000 lines would otherwise
    compile or cache the same pattern repeatedly.
    """
    n = 0
    for ch in line:
        if ch == char:
            n += 1
        else:
            break
    if n < length:
        return False
    # Everything after the fence run must be whitespace.
    rest = line[n:]
    return rest.strip() == ""
