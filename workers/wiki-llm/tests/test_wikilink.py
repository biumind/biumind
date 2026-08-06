"""Wikilink transformation contract.

Mirrors the behaviour pinned by llm_wiki's hand-coded TS reference
(wikilink-transform.ts): rewrite outside
fenced code AND outside inline code, encode the target, escape brackets
in the label, alias takes precedence over target as the visible text.
"""

from __future__ import annotations

from wiki_llm.domain.wikilink import transform_wikilinks


def test_no_wikilinks_passthrough():
    body = "plain markdown\nno links here"
    assert transform_wikilinks(body) == body


def test_basic_target_only():
    assert transform_wikilinks("see [[rope]] for details") == \
        "see [rope](#rope) for details"


def test_target_with_alias():
    assert transform_wikilinks("see [[rope|RoPE embedding]] for details") == \
        "see [RoPE embedding](#rope) for details"


def test_multiple_links_in_one_line():
    src = "[[a]] and [[b|B]] and [[c]]"
    assert transform_wikilinks(src) == "[a](#a) and [B](#b) and [c](#c)"


def test_target_url_encoded_for_special_chars():
    out = transform_wikilinks("[[hello world]]")
    # Space → %20 in the fragment.
    assert out == "[hello world](#hello%20world)"


def test_inline_code_span_left_alone():
    src = "use `[[name]]` syntax"
    assert transform_wikilinks(src) == src


def test_fenced_code_block_left_alone():
    src = "intro\n\n```\n[[example]]\n```\n\nuse [[real]] outside"
    out = transform_wikilinks(src)
    assert "```\n[[example]]\n```" in out
    assert "[real](#real)" in out


def test_alias_containing_close_bracket_does_not_match():
    """The wikilink regex doesn't allow ``]`` inside target or alias —
    a stray bracket aborts the match entirely so we don't run off the
    end of the document looking for the closer. The output is the input
    unchanged."""
    src = "[[target|some [bracket] thing]]"
    assert transform_wikilinks(src) == src


def test_open_bracket_in_target_escapes_in_label():
    """Target permits ``[`` (only ``]`` and ``|`` are forbidden), and
    when target becomes the label the open bracket must be escaped so
    it doesn't open a new markdown link inside the rewritten one."""
    out = transform_wikilinks("[[a [b]]")
    # target captured is ``a [b``; label = target; ``[`` escaped.
    assert out == "[a \\[b](#a%20%5Bb)"


def test_target_with_chinese_chars():
    out = transform_wikilinks("[[中文条目]]")
    # quote() %-encodes non-ASCII targets; the visible label keeps
    # the original chars.
    assert out.startswith("[中文条目](#")
    assert out.endswith(")")
    assert "%" in out  # encoded fragment


def test_empty_double_bracket_left_unchanged():
    # `[[]]` doesn't match the regex (target is +, not *) so it stays
    # as garbage rather than producing a broken link.
    assert transform_wikilinks("foo [[]] bar") == "foo [[]] bar"


def test_does_not_span_paragraph_boundaries():
    # `[[` on one line and `]]` on the next must NOT match — wikilinks
    # are single-line by convention, and matching across lines would
    # eat user-intended bracket content.
    src = "see [[\nthis is on the next line]]"
    assert transform_wikilinks(src) == src


def test_link_inside_inline_code_inside_fence_still_left():
    # Defense in depth: even nested code constructs preserve content.
    src = "```\nuse `[[x]]` like so\n```"
    assert transform_wikilinks(src) == src


def test_alias_strip_whitespace():
    # Trailing/leading whitespace inside the alias is trimmed, but the
    # link still works.
    assert transform_wikilinks("[[t|  spaced  ]]") == "[spaced](#t)"
