"""File → extracted text. MVP parsers: PDF (pypdf) + MD/TXT (utf-8) +
HTML (stdlib tag-strip).

分派按 mime + filename 扩展名 + 内容探测。失败抛 ``ParseError``，runner
捕获后回写 ``parse_status='error'``。

MVP 边界（llm_wiki 有而我们暂未做的）：
- HTML 只 stdlib strip tag，无 readability/trafilatura 正文抽取
- PDF 只 pypdf 文本层提取，扫描版/复杂表格效果差（pdfplumber/MinerU 排期）
- 无 DOCX/XLSX/PPTX/EPUB/OCR（均排期）

content_hash = sha256(extracted_text)（不是文件字节），所以同内容不同格式
会被判重 —— 这是产品决策（用户已确认）。
"""

from __future__ import annotations

import io
from html.parser import HTMLParser
from typing import List, Optional


class ParseError(RuntimeError):
    """文件解析失败。message 落 wiki_sources.parse_error。"""


# 不提取正文的 HTML 标签（script/style/title/meta 等）。
_SKIP_TAGS = {"script", "style", "title", "noscript", "head", "meta", "link"}


class _TagStripper(HTMLParser):
    """stdlib HTMLParser 提纯文本：忽略 script/style 等，取其余 data，压空白。

    MVP 级别 —— 真 boilerplate 抽取（readability-lxml/trafilatura）排期。
    对 webclip 已抓取的相对干净 HTML 够用；对杂乱页面质量一般。
    """

    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self._pieces: List[str] = []
        self._skip_depth = 0

    def handle_starttag(self, tag: str, attrs) -> None:  # noqa: ANN001
        if tag in _SKIP_TAGS:
            self._skip_depth += 1

    def handle_endtag(self, tag: str) -> None:
        if tag in _SKIP_TAGS and self._skip_depth > 0:
            self._skip_depth -= 1

    def handle_data(self, data: str) -> None:
        if self._skip_depth == 0:
            self._pieces.append(data)

    def text(self) -> str:
        raw = " ".join(self._pieces)
        return " ".join(raw.split())  # 压连续空白


def _looks_pdf(mime: str, filename: str) -> bool:
    return "pdf" in mime or filename.lower().endswith(".pdf")


def _looks_html(data: bytes, mime: str, filename: str) -> bool:
    if "html" in mime:
        return True
    name = filename.lower()
    if name.endswith((".html", ".htm", ".xhtml")):
        return True
    head = data[:512].lstrip().lower()
    return head.startswith(b"<!doctype html") or head.startswith(b"<html")


def count_pages(data: bytes, *, mime: str = "", filename: str = "") -> Optional[int]:
    """页数（云端解析按页计费用，client-docproc W4）。

    仅 PDF 有页概念（pypdf len(pages)）；其他格式返回 None（调用方
    自行兜底，如按 1 页或按字符）。解析失败同样 None —— 页数是计费
    元数据，绝不让它阻断主解析流程。
    """
    if not _looks_pdf(mime, filename):
        return None
    try:
        from pypdf import PdfReader
        return len(PdfReader(io.BytesIO(data)).pages)
    except Exception:  # noqa: BLE001 — 页数拿不到就当未知
        return None


def extract(data: bytes, *, mime: str = "", filename: str = "") -> str:
    """从文件字节提取纯文本。

    分派：PDF（mime/扩展名）→ HTML（探测）→ 其余按 utf-8 decode（MD/TXT/code/JSON…）。
    空文件或无文本抛 ParseError。
    """
    if not data:
        raise ParseError("empty file")

    if _looks_pdf(mime, filename):
        return _extract_pdf(data)

    if _looks_html(data, mime, filename):
        return _extract_html(data)

    # MD/TXT/code/JSON/CSV/等 —— 直接 utf-8 decode（errors=replace 容错）
    try:
        text = data.decode("utf-8", errors="replace")
    except Exception as e:  # noqa: BLE001 — decode 几乎不抛（replace），兜底
        raise ParseError(f"utf-8 decode failed: {e}") from e
    if not text.strip():
        raise ParseError("decoded text empty")
    return text


def _extract_pdf(data: bytes) -> str:
    try:
        from pypdf import PdfReader
    except ImportError as e:
        raise ParseError("pypdf not installed — add pypdf to worker deps") from e
    try:
        reader = PdfReader(io.BytesIO(data))
    except Exception as e:
        raise ParseError(f"open PDF failed: {e}") from e

    pieces: List[str] = []
    for i, page in enumerate(reader.pages):
        try:
            text = (page.extract_text() or "").strip()
        except Exception as e:  # noqa: BLE001 — 单页失败不致命，占位继续
            pieces.append(f"[page {i + 1}: extraction failed: {e}]")
            continue
        if text:
            pieces.append(f"--- page {i + 1} ---\n{text}")

    if not pieces:
        raise ParseError(
            "PDF contained no extractable text (scanned image? OCR 排期)"
        )
    return "\n\n".join(pieces)


def _extract_html(data: bytes) -> str:
    try:
        html = data.decode("utf-8", errors="replace")
    except Exception as e:  # noqa: BLE001
        raise ParseError(f"html decode failed: {e}") from e
    stripper = _TagStripper()
    try:
        stripper.feed(html)
    except Exception as e:  # noqa: BLE001 — HTMLParser 对畸形 HTML 宽容
        raise ParseError(f"html parse failed: {e}") from e
    text = stripper.text().strip()
    if not text:
        raise ParseError("HTML stripped to empty (boilerplate-only or no body)")
    return text
