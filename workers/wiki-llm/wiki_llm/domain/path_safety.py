"""Wiki path validation for the FILE-block protocol.

A malicious source document can carry prompt injection that tries to
redirect generated FILE blocks outside the ``wiki/`` tree. Without this
guard, the LLM could be coerced into writing to ``../../etc/passwd``
and the brain page writer would happily accept it (page rows are not
sandboxed by path — pages live in the database, but the path becomes
the canonical title/URL, so a leaked traversal attempt corrupts UI and
wikilink resolution).

The whitelist this module enforces:

  * non-empty (after strip)
  * no NUL or control chars
  * not absolute (POSIX ``/`` or Windows ``C:`` / leading ``\\``)
  * starts with ``wiki/`` after backslash → forward-slash normalization
  * no path segment exactly equal to ``..`` (forward or backslash)
  * no segment ends with ``.`` or `` `` (Windows quirk)
  * no segment uses Windows-invalid chars: ``: " | ? * < >``
  * no segment whose dotless head matches a Windows reserved device name
    (CON, NUL, PRN, AUX, COM1-9, LPT1-9) — even with file extensions
    appended (``CON.md`` is rejected; ``auxiliary.md`` is fine because
    the head is ``auxiliary``, not ``AUX``)

Re-implemented from llm_wiki's ``isSafeIngestPath`` test contract
(ingest-parse.test.ts:320). Pure function;
no I/O, no globals.
"""

from __future__ import annotations

# Windows reserved device names. The check compares the segment's
# pre-first-dot head to this set, case-insensitively, so `con.md` and
# `nul.pdf.md` are rejected while `auxiliary.md` is accepted.
_WINDOWS_RESERVED: frozenset[str] = frozenset({
    "CON", "NUL", "PRN", "AUX",
    "COM1", "COM2", "COM3", "COM4", "COM5",
    "COM6", "COM7", "COM8", "COM9",
    "LPT1", "LPT2", "LPT3", "LPT4", "LPT5",
    "LPT6", "LPT7", "LPT8", "LPT9",
})

# Characters that Windows refuses in filenames. We also include `:`
# even though POSIX accepts it — wiki paths are meant to round-trip
# across both filesystems, and `:` in a wiki title looks like a drive
# letter to many tools downstream.
_WINDOWS_INVALID_CHARS: frozenset[str] = frozenset(':"|?*<>')


def is_safe_ingest_path(path: str) -> bool:
    """Return True iff `path` is a safe wiki/* destination.

    Always returns False for unsafe inputs — the caller is expected to
    log + drop blocks where this returns False. We don't raise: an LLM
    can emit dozens of paths per stream and we want to keep the fast
    path linear.
    """
    if not isinstance(path, str):
        return False
    if not path or not path.strip():
        return False

    # NUL + control chars (covers \x00..\x1f including \n, \r, \t, \x07).
    # Done before any other check so a control char in a "safe-looking"
    # path can't slip through.
    for ch in path:
        if ord(ch) < 0x20:
            return False

    # Reject absolute paths up front (in their pre-normalization form)
    # so we don't accidentally accept `/wiki/foo.md` after stripping the
    # leading slash.
    if path.startswith("/") or path.startswith("\\"):
        return False
    # Drive letters: `C:`, `c:`, ... at the start.
    if len(path) >= 2 and path[1] == ":":
        return False

    # Normalise backslashes for segment-level checks. After this we deal
    # only with forward slashes; the absolute-path check above already
    # rejected UNC `\\server\share` style.
    normalized = path.replace("\\", "/")

    # Must be confined to the wiki/ subtree. Anything else (notes/, raw/,
    # src-tauri/, …) is a red flag — even when it's a "real" project
    # directory, we ingest into wiki/ ONLY.
    if not normalized.startswith("wiki/"):
        return False

    # Per-segment validation.
    segments = normalized.split("/")
    for seg in segments:
        if not seg:
            # Empty segment means double slash (`wiki//foo.md`) or trailing
            # slash. Either is a red flag.
            return False
        if seg == "..":
            return False
        # Trailing `.` or ` ` confuses Windows path resolution; on POSIX
        # they're legal but also useless and likely a typo / injection.
        if seg.endswith(".") or seg.endswith(" "):
            return False
        # Windows-invalid characters anywhere in the segment.
        for ch in seg:
            if ch in _WINDOWS_INVALID_CHARS:
                return False
        # Windows reserved device names — match the segment's dotless
        # head, case-insensitively. `auxiliary.md` → head `auxiliary`,
        # passes; `aux.md` → head `aux`, fails.
        head = seg.split(".", 1)[0].upper()
        if head in _WINDOWS_RESERVED:
            return False

    return True
