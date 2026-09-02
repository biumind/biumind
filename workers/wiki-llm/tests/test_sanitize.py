"""sanitize_ingested_content —— 四种 LLM 畸形 frontmatter 形态的写时清洗。

契约来自 llm_wiki ingest-sanitize.ts 的真实语料审计（30/67 畸形率）；
每个形态一个正向用例 + 保守性反向用例（正文深处的合法结构不动）。
"""

from __future__ import annotations

from wiki_llm.domain.sanitize import sanitize_ingested_content


# ── shape 1: outer code fence ─────────────────────────────────────

def test_strips_yaml_fence_wrapping_whole_document():
    raw = "```yaml\n---\ntype: entity\n---\n# Body\n```\n"
    assert sanitize_ingested_content(raw) == "---\ntype: entity\n---\n# Body"


def test_strips_md_fence_variant():
    raw = "```markdown\n# Just a body\nmore\n```\n"
    assert sanitize_ingested_content(raw) == "# Just a body\nmore"


def test_strips_fence_that_only_wraps_frontmatter():
    raw = "```yaml\n---\ntype: entity\n---\n```\n# Body continues\n"
    assert sanitize_ingested_content(raw) == "---\ntype: entity\n---\n# Body continues\n"


def test_unclosed_leading_fence_is_left_alone():
    """流截断产生的未闭合围栏不在本模块修复范围（截断由 runner 判失败）。"""
    raw = "```yaml\n---\ntype: entity\n---\n# Body no close\n"
    assert sanitize_ingested_content(raw) == raw


def test_fence_deep_in_body_is_untouched():
    raw = "---\ntype: entity\n---\n# Body\n\n```yaml\nexample: 1\n```\n"
    assert sanitize_ingested_content(raw) == raw


# ── shape 2: stray frontmatter: key ───────────────────────────────

def test_strips_leading_frontmatter_key_before_block():
    raw = "frontmatter:\n---\ntype: entity\n---\n# Body\n"
    assert sanitize_ingested_content(raw) == "---\ntype: entity\n---\n# Body\n"


def test_prose_mention_of_frontmatter_is_untouched():
    raw = "# Notes\n\nfrontmatter: is a concept discussed here.\n"
    assert sanitize_ingested_content(raw) == raw


# ── shape 3: missing opening fence ────────────────────────────────

def test_adds_missing_opening_fence():
    raw = "type: entity\ntitle: Foo\n---\n# Body\n"
    assert sanitize_ingested_content(raw) == "---\ntype: entity\ntitle: Foo\n---\n# Body\n"


def test_heading_before_closer_breaks_repair():
    raw = "type: entity\n# A heading\n---\n"
    assert sanitize_ingested_content(raw) == raw


def test_non_frontmatter_opening_is_untouched():
    raw = "summary: not a frontmatter head key\n---\n# Body\n"
    assert sanitize_ingested_content(raw) == raw


# ── shape 4: wikilink lists inside frontmatter ────────────────────

def test_repairs_wikilink_list_inside_frontmatter():
    raw = "---\nrelated: [[a]], [[b]], [[c]]\n---\n# Body\n"
    assert sanitize_ingested_content(raw) == \
        '---\nrelated: ["[[a]]", "[[b]]", "[[c]]"]\n---\n# Body\n'


def test_body_wikilinks_are_untouched():
    raw = "---\ntype: entity\n---\nSee [[a]], [[b]] for details.\n"
    assert sanitize_ingested_content(raw) == raw


# ── 组合与恒等 ────────────────────────────────────────────────────

def test_combined_shapes_pipeline_in_order():
    """shape 2 + 4：去 stray key 后 frontmatter 块成形，wikilink 列表随之修复。"""
    raw = "frontmatter:\n---\ntype: entity\nrelated: [[a]], [[b]]\n---\n# Body\n"
    assert sanitize_ingested_content(raw) == \
        '---\ntype: entity\nrelated: ["[[a]]", "[[b]]"]\n---\n# Body\n'


def test_combined_fence_plus_missing_opener():
    """shape 1 + 3：剥外层围栏后暴露出缺开头 --- 的 frontmatter，补回。"""
    raw = "```yaml\ntype: entity\n---\n# Body\n```\n"
    assert sanitize_ingested_content(raw) == "---\ntype: entity\n---\n# Body"


def test_wellformed_page_is_identical():
    raw = "---\ntype: entity\ntitle: Foo\n---\n# Body\n\n```python\ncode()\n```\n"
    assert sanitize_ingested_content(raw) == raw
