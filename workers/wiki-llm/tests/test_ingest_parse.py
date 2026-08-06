"""FILE-block parser regression suite.

Mirrors the H1 / H2 / H3 / H5 / H6 + path-traversal cases pinned by
llm_wiki's ingest-parse.test.ts. A failing test here
means the parser is back to silently dropping pages or letting
prompt-injection paths through.
"""

from __future__ import annotations

from wiki_llm.domain.ingest_parse import parse_file_blocks


# ── Happy paths ───────────────────────────────────────────────────────

def test_single_well_formed_block():
    text = "\n".join([
        "---FILE: wiki/concepts/rope.md---",
        "# RoPE",
        "Rotary positional embedding.",
        "---END FILE---",
    ])
    res = parse_file_blocks(text)
    assert res.warnings == []
    assert len(res.blocks) == 1
    assert res.blocks[0].path == "wiki/concepts/rope.md"
    assert "# RoPE" in res.blocks[0].content


def test_multiple_consecutive_blocks():
    text = "\n".join([
        "---FILE: wiki/entities/qwen.md---",
        "# Qwen",
        "---END FILE---",
        "",
        "---FILE: wiki/concepts/moe.md---",
        "# MoE",
        "---END FILE---",
        "",
        "---FILE: wiki/sources/paper.md---",
        "# Source summary",
        "---END FILE---",
    ])
    res = parse_file_blocks(text)
    assert res.warnings == []
    assert [b.path for b in res.blocks] == [
        "wiki/entities/qwen.md",
        "wiki/concepts/moe.md",
        "wiki/sources/paper.md",
    ]


def test_hyphenated_paths():
    text = "\n".join([
        "---FILE: wiki/concepts/multi-head-attention.md---",
        "body",
        "---END FILE---",
    ])
    assert len(parse_file_blocks(text).blocks) == 1


def test_preamble_prose_before_first_block_is_ignored():
    text = "\n".join([
        "Here are the wiki files:",
        "",
        "---FILE: wiki/concepts/foo.md---",
        "body",
        "---END FILE---",
    ])
    assert len(parse_file_blocks(text).blocks) == 1


# ── H1: CRLF normalization ────────────────────────────────────────────

def test_crlf_input_normalized_to_lf():
    text = "\r\n".join([
        "---FILE: wiki/entities/qwen.md---",
        "# Qwen",
        "---END FILE---",
        "",
        "---FILE: wiki/concepts/moe.md---",
        "# MoE",
        "---END FILE---",
    ])
    res = parse_file_blocks(text)
    assert res.warnings == []
    assert len(res.blocks) == 2
    for b in res.blocks:
        assert "\r" not in b.content


def test_mixed_crlf_body_with_lf_markers():
    text = "---FILE: wiki/concepts/foo.md---\nline1\r\nline2\r\n---END FILE---"
    res = parse_file_blocks(text)
    assert len(res.blocks) == 1
    assert res.blocks[0].content == "line1\nline2"


# ── H2: stream truncation ─────────────────────────────────────────────

def test_truncated_final_block_emits_warning():
    text = "\n".join([
        "---FILE: wiki/entities/qwen.md---",
        "# Qwen",
        "---END FILE---",
        "",
        "---FILE: wiki/concepts/moe.md---",
        "# Mixture of Exp",  # cut off mid-stream
    ])
    res = parse_file_blocks(text)
    assert len(res.blocks) == 1
    assert res.blocks[0].path == "wiki/entities/qwen.md"
    assert len(res.warnings) == 1
    assert "wiki/concepts/moe.md" in res.warnings[0]
    assert "not closed" in res.warnings[0].lower()


def test_only_block_unclosed_warns():
    text = "---FILE: wiki/concepts/rope.md---\n# RoPE\nIt rotates"
    res = parse_file_blocks(text)
    assert res.blocks == []
    assert len(res.warnings) == 1
    assert "rope.md" in res.warnings[0]


# ── H3: tolerant marker matching ──────────────────────────────────────

def test_inner_spaces_in_end_marker():
    text = "\n".join([
        "---FILE: wiki/concepts/foo.md---",
        "body",
        "--- END FILE ---",
    ])
    assert len(parse_file_blocks(text).blocks) == 1


def test_lowercase_end_marker():
    text = "\n".join([
        "---FILE: wiki/concepts/foo.md---",
        "body",
        "---end file---",
    ])
    assert len(parse_file_blocks(text).blocks) == 1


def test_inner_spaces_in_opener():
    text = "\n".join([
        "--- FILE: wiki/concepts/foo.md ---",
        "body",
        "---END FILE---",
    ])
    res = parse_file_blocks(text)
    assert len(res.blocks) == 1
    assert res.blocks[0].path == "wiki/concepts/foo.md"


def test_trailing_whitespace_on_opener():
    text = "---FILE: wiki/concepts/foo.md---   \nbody\n---END FILE---"
    assert len(parse_file_blocks(text).blocks) == 1


def test_marker_in_prose_or_list_item_is_body():
    """A literal marker embedded in a list item / prose line must NOT
    end the block — the line doesn't START with ``---``."""
    text = "\n".join([
        "---FILE: wiki/concepts/foo.md---",
        "Not to be written:",
        "- `---END FILE---` in backticks (this is prose)",
        "real content continues",
        "---END FILE---",
    ])
    res = parse_file_blocks(text)
    assert len(res.blocks) == 1
    assert "real content continues" in res.blocks[0].content


# ── H5: code-fence awareness ──────────────────────────────────────────

def test_end_marker_inside_fenced_block_is_body():
    text = "\n".join([
        "---FILE: wiki/concepts/ingest-format.md---",
        "# Ingest Format",
        "",
        "Example of a FILE block:",
        "",
        "```plaintext",
        "---FILE: wiki/path/to/page.md---",
        "body content",
        "---END FILE---",   # inside fence — must be ignored
        "```",
        "",
        "More explanation after the example.",
        "---END FILE---",   # the real closer
    ])
    res = parse_file_blocks(text)
    assert res.warnings == []
    assert len(res.blocks) == 1
    assert res.blocks[0].path == "wiki/concepts/ingest-format.md"
    assert "```plaintext" in res.blocks[0].content
    assert "More explanation" in res.blocks[0].content


def test_multiple_fenced_blocks_in_one_page():
    text = "\n".join([
        "---FILE: wiki/concepts/foo.md---",
        "```",
        "---END FILE---",
        "```",
        "",
        "prose",
        "",
        "~~~",
        "---END FILE---",
        "~~~",
        "",
        "more prose",
        "---END FILE---",
    ])
    res = parse_file_blocks(text)
    assert len(res.blocks) == 1
    assert "more prose" in res.blocks[0].content


def test_nested_fence_lengths_outer_4_inner_3():
    text = "\n".join([
        "---FILE: wiki/concepts/foo.md---",
        "````markdown",
        "```",
        "---END FILE---",
        "```",
        "````",
        "",
        "real content after the outer fence closes",
        "---END FILE---",
    ])
    res = parse_file_blocks(text)
    assert len(res.blocks) == 1
    assert "real content after the outer fence closes" in res.blocks[0].content


def test_3_tick_fence_does_not_close_4_tick_opener():
    text = "\n".join([
        "---FILE: wiki/concepts/foo.md---",
        "````",
        "```",
        "---END FILE---",  # still inside the 4-tick fence
        "```",
        "````",
        "",
        "real content",
        "---END FILE---",
    ])
    res = parse_file_blocks(text)
    assert len(res.blocks) == 1
    assert "real content" in res.blocks[0].content


# ── H6: empty path ────────────────────────────────────────────────────

def test_empty_path_warns_and_drops():
    text = "---FILE:   ---\nsome body\n---END FILE---"
    res = parse_file_blocks(text)
    assert res.blocks == []
    assert len(res.warnings) > 0


# ── Path traversal guard end-to-end ──────────────────────────────────

def test_dotdot_path_drops_with_warning():
    text = "\n".join([
        "---FILE: wiki/concepts/legit.md---",
        "Real page.",
        "---END FILE---",
        "---FILE: ../../etc/passwd---",
        "attacker:x:0:0::/root:/bin/bash",
        "---END FILE---",
    ])
    res = parse_file_blocks(text)
    assert len(res.blocks) == 1
    assert res.blocks[0].path == "wiki/concepts/legit.md"
    assert any("../../etc/passwd" in w for w in res.warnings)
    assert any("unsafe path" in w for w in res.warnings)


def test_absolute_path_dropped():
    text = "---FILE: /etc/passwd---\nevil\n---END FILE---"
    res = parse_file_blocks(text)
    assert res.blocks == []
    assert any("unsafe path" in w for w in res.warnings)


def test_path_outside_wiki_dropped():
    text = "\n".join([
        "---FILE: src-tauri/src/main.rs---",
        'fn main() { panic!("injected"); }',
        "---END FILE---",
    ])
    res = parse_file_blocks(text)
    assert res.blocks == []
    assert any("unsafe path" in w for w in res.warnings)


def test_mixed_safe_and_unsafe_paths_keeps_only_safe():
    text = "\n".join([
        "---FILE: wiki/concepts/topic-a.md---",
        "topic A page",
        "---END FILE---",
        "---FILE: ../config.json---",
        '{"hijacked": true}',
        "---END FILE---",
        "---FILE: wiki/entities/topic-b.md---",
        "topic B page",
        "---END FILE---",
    ])
    res = parse_file_blocks(text)
    assert [b.path for b in res.blocks] == [
        "wiki/concepts/topic-a.md",
        "wiki/entities/topic-b.md",
    ]
    assert any("../config.json" in w for w in res.warnings)


# ── Streaming idempotency property ───────────────────────────────────

def test_parse_is_pure_on_identical_input():
    """Re-running the parser on the same input twice yields equal
    output. This is what powers the worker's "diff against last call"
    streaming partial-save loop — without idempotency, the worker
    would emit duplicates as the buffer grew."""
    text = "\n".join([
        "---FILE: wiki/concepts/a.md---",
        "A",
        "---END FILE---",
        "---FILE: wiki/concepts/b.md---",
        "B",
        "---END FILE---",
    ])
    a = parse_file_blocks(text)
    b = parse_file_blocks(text)
    assert a == b


def test_growing_prefix_yields_growing_block_count():
    """When the buffer grows by one closed block, exactly one new block
    appears — the worker uses this to emit incremental ``page`` updates."""
    block = "---FILE: wiki/concepts/{i}.md---\n{i}\n---END FILE---\n"
    grown = ""
    for i in range(3):
        grown += block.format(i=i)
        res = parse_file_blocks(grown)
        assert len(res.blocks) == i + 1
        assert res.blocks[-1].path == f"wiki/concepts/{i}.md"


def test_empty_input():
    res = parse_file_blocks("")
    assert res.blocks == []
    assert res.warnings == []
