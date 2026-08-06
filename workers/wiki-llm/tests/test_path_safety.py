"""Path-safety contract — every traversal vector we've thought of.

Mirrors the assertion set in
llm_wiki's ingest-parse.test.ts (the `isSafeIngestPath`
describe block). When this file goes red the validator was loosened —
revert it.
"""

from __future__ import annotations

from wiki_llm.domain.path_safety import is_safe_ingest_path


def test_accepts_canonical_wiki_paths():
    assert is_safe_ingest_path("wiki/concepts/foo.md")
    assert is_safe_ingest_path("wiki/index.md")
    assert is_safe_ingest_path("wiki/sources/some-paper.md")
    assert is_safe_ingest_path("wiki/entities/transformer.md")


def test_rejects_empty_or_whitespace():
    assert not is_safe_ingest_path("")
    assert not is_safe_ingest_path("   ")
    assert not is_safe_ingest_path("\t\n")


def test_rejects_paths_outside_wiki_subtree():
    assert not is_safe_ingest_path("notes/foo.md")
    assert not is_safe_ingest_path("foo.md")
    assert not is_safe_ingest_path("raw/sources/leaked.md")
    assert not is_safe_ingest_path("src-tauri/src/main.rs")


def test_rejects_posix_absolute_paths():
    assert not is_safe_ingest_path("/etc/passwd")
    assert not is_safe_ingest_path("/Users/victim/.ssh/authorized_keys")
    assert not is_safe_ingest_path("/wiki/foo.md")


def test_rejects_windows_absolute_and_unc():
    assert not is_safe_ingest_path("C:/Windows/System32/config")
    assert not is_safe_ingest_path("c:\\Users\\victim\\evil.txt")
    assert not is_safe_ingest_path("\\Users\\victim\\evil.txt")
    assert not is_safe_ingest_path("\\\\server\\share\\file.md")


def test_rejects_dotdot_segment_in_any_position():
    assert not is_safe_ingest_path("wiki/../etc/passwd")
    assert not is_safe_ingest_path("wiki/concepts/../../etc/passwd")
    assert not is_safe_ingest_path("wiki/..")
    assert not is_safe_ingest_path("..")
    # Backslash-form traversal must also be caught after normalization.
    assert not is_safe_ingest_path("wiki\\..\\etc\\passwd")


def test_accepts_filenames_containing_double_dots_as_substring():
    # `..` is a path SEGMENT, not a substring. A filename like
    # `qwen-2.5..notes.md` is unusual but legal.
    assert is_safe_ingest_path("wiki/concepts/qwen-2.5..notes.md")
    assert is_safe_ingest_path("wiki/concepts/foo..bar.md")


def test_rejects_nul_and_control_chars():
    assert not is_safe_ingest_path("wiki/concepts/foo\x00.md")
    assert not is_safe_ingest_path("wiki/concepts/foo\nbar.md")
    assert not is_safe_ingest_path("wiki/\x07alarm.md")


def test_rejects_windows_invalid_chars():
    assert not is_safe_ingest_path("wiki/concepts/Article: Why It Matters.md")
    assert not is_safe_ingest_path('wiki/concepts/quoted"name.md')
    assert not is_safe_ingest_path("wiki/concepts/a|b.md")
    assert not is_safe_ingest_path("wiki/concepts/a?b.md")
    assert not is_safe_ingest_path("wiki/concepts/a*b.md")
    assert not is_safe_ingest_path("wiki/concepts/a<b>.md")


def test_rejects_windows_reserved_device_names():
    assert not is_safe_ingest_path("wiki/concepts/con.md")
    assert not is_safe_ingest_path("wiki/concepts/NUL.pdf.md")
    assert not is_safe_ingest_path("wiki/concepts/com1.md")
    assert not is_safe_ingest_path("wiki/concepts/LPT9.notes.md")
    # `auxiliary` is not exactly `AUX`, so it should be accepted.
    assert is_safe_ingest_path("wiki/concepts/auxiliary.md")


def test_rejects_segments_ending_in_dot_or_space():
    # Trailing space BEFORE the extension is fine — `topic .md` ends in
    # `d`, not a space.
    assert is_safe_ingest_path("wiki/concepts/topic .md")
    # But the segment itself ending in `.` or ` ` is not.
    assert not is_safe_ingest_path("wiki/concepts/topic.")
    assert not is_safe_ingest_path("wiki/concepts/topic ")
    assert not is_safe_ingest_path("wiki/concepts/folder./topic.md")
    assert not is_safe_ingest_path("wiki/concepts/folder /topic.md")


def test_non_string_inputs_are_rejected():
    assert not is_safe_ingest_path(None)  # type: ignore[arg-type]
    assert not is_safe_ingest_path(42)    # type: ignore[arg-type]
    assert not is_safe_ingest_path([])    # type: ignore[arg-type]
