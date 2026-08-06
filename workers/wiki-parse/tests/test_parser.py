"""wiki-parse parser 单测。覆盖 MVP 分派：MD/TXT/HTML + PDF 缺失分支。

真 PDF 文本提取需 fixture（reportlab 构造或 sample.pdf），MVP 跳，排期补。
"""

from __future__ import annotations

import sys

import pytest

from wiki_parse.parser import ParseError, extract


def test_extract_markdown_passthrough():
    data = b"# Title\n\nbody text\n- item"
    assert extract(data, filename="note.md") == "# Title\n\nbody text\n- item"


def test_extract_txt_utf8_replace_on_invalid_bytes():
    # 非法 utf-8 字节 → errors=replace 不抛，保留可解码部分
    data = b"hello\xff\xffworld"
    out = extract(data, filename="log.txt")
    assert "hello" in out and "world" in out


def test_extract_html_strips_script_style_title():
    html = (
        b"<html><head><title>IGNORE</title>"
        b"<style>body{}</style></head>"
        b"<body><p>Hello <b>world</b></p>"
        b"<script>alert(1)</script></body></html>"
    )
    out = extract(html, filename="page.html")
    assert "Hello" in out and "world" in out
    assert "alert" not in out     # script stripped
    assert "IGNORE" not in out    # title stripped
    assert "body{}" not in out    # style stripped


def test_extract_html_by_content_sniff_without_extension():
    # 无 .html 扩展名、mime 空 —— 靠 doctype 探测
    data = b"<!doctype html><html><body><p>sniffed</p></body></html>"
    out = extract(data, filename="unknown")
    assert "sniffed" in out


def test_extract_html_boilerplate_only_raises():
    data = b"<html><head><title>x</title></head><body></body></html>"
    with pytest.raises(ParseError):
        extract(data, filename="empty.html")


def test_extract_empty_file_raises():
    with pytest.raises(ParseError, match="empty"):
        extract(b"", filename="x.txt")


def test_extract_pdf_missing_pypdf_raises(monkeypatch):
    # 模拟 pypdf 未安装：sys.modules["pypdf"] = None 让 import 抛 ModuleNotFoundError
    monkeypatch.setitem(sys.modules, "pypdf", None)
    with pytest.raises(ParseError, match="pypdf"):
        extract(b"%PDF-1.4 fake", mime="application/pdf", filename="x.pdf")


def test_extract_decoded_empty_raises():
    with pytest.raises(ParseError, match="decoded text empty"):
        extract(b"   \n\t  ", filename="blank.txt")
