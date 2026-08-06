"""Frontmatter parser contract.

Mirrors the behaviour pinned by llm_wiki's frontmatter.ts:
strict block first, fallback for LLM-corrupted prefixes, wikilink-list
repair, and a flat normalised result shape.
"""

from __future__ import annotations

from wiki_llm.domain.frontmatter import parse_frontmatter


def test_no_frontmatter_returns_original_body():
    body = "no frontmatter here\nsecond line"
    res = parse_frontmatter(body)
    assert res.frontmatter is None
    assert res.body == body
    assert res.raw_block == ""


def test_strict_block_at_top_of_file():
    src = "---\ntitle: RoPE\nrelated:\n  - rotary\n  - transformers\n---\n\n# Body\ntext"
    res = parse_frontmatter(src)
    assert res.frontmatter == {
        "title": "RoPE",
        "related": ["rotary", "transformers"],
    }
    assert res.body.startswith("\n# Body")
    assert res.raw_block.startswith("---\n")
    assert res.raw_block.endswith("---\n")


def test_roundtrip_via_raw_block():
    """The raw_block + body must reconstruct the original input exactly,
    so callers that edit only the body can preserve user-managed YAML."""
    src = "---\ntitle: Test\n---\n\nhello"
    res = parse_frontmatter(src)
    assert res.raw_block + res.body == src


def test_no_body_after_frontmatter():
    src = "---\ntitle: Empty\n---\n"
    res = parse_frontmatter(src)
    assert res.frontmatter == {"title": "Empty"}
    assert res.body == ""


def test_unparseable_yaml_falls_back_to_none():
    # Invalid YAML (mismatched colons inside a flow value).
    src = "---\nkey: : :\n---\nbody"
    res = parse_frontmatter(src)
    # The block is recognised structurally so raw_block + body are set;
    # only `frontmatter` is None because the YAML couldn't be parsed.
    assert res.frontmatter is None
    assert res.body == "body"
    assert res.raw_block.startswith("---\n")


def test_wikilink_list_repair_recovers_unbracketed_lists():
    src = "---\nrelated: [[a]], [[b]], [[c]]\ntitle: T\n---\nbody"
    res = parse_frontmatter(src)
    assert res.frontmatter == {
        "related": ["[[a]]", "[[b]]", "[[c]]"],
        "title": "T",
    }


def test_fallback_recovers_block_after_yaml_fence_prefix():
    """LLM wrapped the file in ```yaml ... ``` — recover the block AND
    strip the orphan closing fence so the body renders cleanly."""
    src = "```yaml\n---\ntitle: Wrapped\n---\n```\n# Body\n"
    res = parse_frontmatter(src)
    assert res.frontmatter == {"title": "Wrapped"}
    # Closing fence stripped from body head.
    assert res.body.lstrip().startswith("# Body")


def test_fallback_rejects_horizontal_rule_far_from_top():
    """A `---` divider deep in the body must not be mistaken for a
    frontmatter opener — otherwise editing any page with section
    dividers becomes a parser hazard."""
    body_lines = ["paragraph"] * 10 + ["---", "irrelevant", "---", "more body"]
    src = "\n".join(body_lines)
    res = parse_frontmatter(src)
    assert res.frontmatter is None
    assert res.raw_block == ""


def test_normalize_coerces_non_string_scalars():
    src = "---\nyear: 2026\npublished: true\n---\nbody"
    res = parse_frontmatter(src)
    assert res.frontmatter == {"year": "2026", "published": "true"}


def test_normalize_dates_to_iso_date_only():
    src = "---\ncreated: 2026-05-28\n---\nbody"
    res = parse_frontmatter(src)
    assert res.frontmatter == {"created": "2026-05-28"}


def test_normalize_nested_object_becomes_json():
    src = "---\nmeta:\n  author: nash\n  count: 3\n---\nbody"
    res = parse_frontmatter(src)
    fm = res.frontmatter
    assert fm is not None
    # Nested object stringified as JSON; key order isn't guaranteed but
    # both fields must be present.
    raw = fm["meta"]
    assert isinstance(raw, str)
    assert "nash" in raw
    assert "3" in raw


def test_top_level_non_dict_yields_none_frontmatter():
    # A YAML payload that's just a list (legal YAML, not legal frontmatter
    # for our flat-dict surface) returns None.
    src = "---\n- a\n- b\n---\nbody"
    res = parse_frontmatter(src)
    assert res.frontmatter is None
    assert res.body == "body"


def test_crlf_line_endings_are_handled():
    src = "---\r\ntitle: CRLF\r\n---\r\nbody"
    res = parse_frontmatter(src)
    assert res.frontmatter == {"title": "CRLF"}
