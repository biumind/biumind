"""Write-time sanitizer for LLM-generated wiki page bodies.

Ported from llm_wiki ``ingest-sanitize.ts`` (Apache-2.0 思路重写，不拷贝代码)。
其真实语料审计（67 页实体页）发现 30/67 的 frontmatter 无法严格解析，
模型反复产出四种畸形形态：

  1. 整页被 ```` ```yaml ````（或 ```` ```md ```` / ```` ```markdown ````）
     代码围栏包裹 —— 在生成上下文里看着没问题，但不是合法 .md 文件。
  2. 开头多一行 ``frontmatter:`` 键，把文档变成嵌套 YAML 畸形。
  3. 缺开头的 ``---`` 但有结尾 ``---``（模型从 YAML 块"中间"开始写）。
  4. frontmatter 内的 wikilink 列表没加外层括号：
     ``related: [[a]], [[b]]`` 不是合法 YAML 流语法。

本模块在写库前把这四种形态改写成标准 ``---\\n…\\n---\\n`` frontmatter。
刻意保守：每个模式都锚定在文档开头（或 frontmatter 顶层作用域），
正文深处的合法围栏 / 散文里出现的 "frontmatter:" 字样一概不动。

读取侧的 ``frontmatter.py`` 仍保留 fallback 解析以兼容历史脏数据；
写入侧 sanitize 让新产出的页永远不需要 fallback。
"""

from __future__ import annotations

import re


def sanitize_ingested_content(content: str) -> str:
    """Rewrite the four malformed LLM frontmatter shapes into standard form."""
    cleaned = _strip_outer_code_fence(content)
    cleaned = _strip_frontmatter_key_prefix(cleaned)
    cleaned = _add_missing_opening_frontmatter_fence(cleaned)
    cleaned = _repair_wikilink_lists_in_frontmatter(cleaned)
    return cleaned


# ── shape 1: outer code fence ─────────────────────────────────────

# 开头围栏：允许 BOM 与前导空行；info string 仅 yaml/md/markdown 或空。
_OUTER_FENCE_OPEN_RE = re.compile(
    r"^(?:\uFEFF)?(?:[ \t]*\r?\n)*[ \t]*```(?:yaml|md|markdown)?[ \t]*\r?\n",
    re.IGNORECASE,
)
# 结尾围栏：文档末尾独立一行的 ```（允许尾随空白）。
_OUTER_FENCE_CLOSE_RE = re.compile(r"\r?\n[ \t]*```[ \t]*\r?\n?\s*$")
# "只包了 frontmatter"的形态：围栏内恰好是一个完整 --- 块，之后正文不带围栏。
_FRONTMATTER_ONLY_RE = re.compile(
    r"^(---[ \t]*\r?\n[\s\S]*?^---[ \t]*\r?\n)[ \t]*```[ \t]*(?:\r?\n|$)",
    re.MULTILINE,
)


def _strip_outer_code_fence(content: str) -> str:
    """Remove a top-level fence wrapper (open + matching close lines)."""
    open_m = _OUTER_FENCE_OPEN_RE.match(content)
    if open_m is None:
        return content
    after_open = content[open_m.end():]

    close_m = _OUTER_FENCE_CLOSE_RE.search(after_open)
    if close_m is not None:
        return after_open[: close_m.start()]

    fm_only = _FRONTMATTER_ONLY_RE.match(after_open)
    if fm_only is None:
        return content
    return fm_only.group(1) + after_open[fm_only.end():]


# ── shape 2: stray `frontmatter:` key line ────────────────────────

_FRONTMATTER_KEY_PREFIX_RE = re.compile(
    r"^[ \t]*frontmatter\s*:\s*\r?\n(?=[ \t]*---\s*\r?\n)"
)


def _strip_frontmatter_key_prefix(content: str) -> str:
    """Strip a leading ``frontmatter:`` line only when the next line is
    the real ``---`` opener — prose mentioning the word is untouched."""
    m = _FRONTMATTER_KEY_PREFIX_RE.match(content)
    if m is None:
        return content
    return content[m.end():]


# ── shape 3: missing opening fence ────────────────────────────────

_ALREADY_OPEN_RE = re.compile(r"^[ \t]*---\s*(\r?\n|$)")
_FM_HEAD_KEY_RE = re.compile(
    r"^(type|title|created|updated|tags|related|sources)\s*:", re.IGNORECASE
)
_HEADING_RE = re.compile(r"^#{1,6}\s+")


def _add_missing_opening_frontmatter_fence(content: str) -> str:
    """Prepend ``---`` when the document clearly starts with frontmatter
    keys and a closing fence shows up within the next 30 lines."""
    if _ALREADY_OPEN_RE.match(content):
        return content
    lines = re.split(r"\r?\n", content)
    first_idx = next((i for i, ln in enumerate(lines) if ln.strip()), -1)
    if first_idx < 0:
        return content
    if _FM_HEAD_KEY_RE.match(lines[first_idx].strip()) is None:
        return content
    search_end = min(len(lines), first_idx + 30)
    for i in range(first_idx + 1, search_end):
        trimmed = lines[i].strip()
        if trimmed == "---":
            return "---\n" + "\n".join(lines[first_idx:])
        if _HEADING_RE.match(trimmed):
            break
    return content


# ── shape 4: unbracketed wikilink lists inside frontmatter ────────

_FM_BLOCK_RE = re.compile(r"^(---[ \t]*(\r?\n))([\s\S]*?)(\r?\n---[ \t]*(?:\r?\n|$))")
_WIKILINK_LIST_LINE_RE = re.compile(
    r"^(\s*[A-Za-z_][\w-]*\s*:\s*)"
    r"(\[\[[^\]]+\]\](?:\s*,\s*\[\[[^\]]+\]\])+)\s*$",
)


def _repair_wikilink_lists_in_frontmatter(content: str) -> str:
    """Wrap ``key: [[a]], [[b]]`` lines into quoted flow arrays, only
    inside the frontmatter block; body wikilinks are left alone."""
    m = _FM_BLOCK_RE.match(content)
    if m is None:
        return content
    newline = m.group(2)
    repaired_lines: list[str] = []
    for line in re.split(r"\r?\n", m.group(3)):
        lm = _WIKILINK_LIST_LINE_RE.match(line)
        if lm is None:
            repaired_lines.append(line)
            continue
        items = [s.strip() for s in lm.group(2).split(",") if s.strip()]
        quoted = ", ".join(f'"{s}"' for s in items)
        repaired_lines.append(f"{lm.group(1)}[{quoted}]")
    return m.group(1) + newline.join(repaired_lines) + m.group(4) + content[m.end():]
