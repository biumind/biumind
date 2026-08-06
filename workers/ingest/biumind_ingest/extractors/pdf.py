"""PDF text extractor backed by ``pypdf``.

Why pypdf and not pdfminer.six / pymupdf?
  - pypdf is pure Python (no system libs, no C extension build).
  - Quality is good enough for digital-native PDFs; scanned documents
    fall through to the OCR extractor anyway via metadata.kind="image".
"""

from __future__ import annotations

import io

from .base import ExtractorContext, ExtractorError, ExtractorUnavailable


def extract_pdf(data: bytes, _ctx: ExtractorContext) -> str:
    try:
        from pypdf import PdfReader  # type: ignore
    except ImportError as e:
        raise ExtractorUnavailable(
            "pdf extractor needs `pip install biumind-ingest-worker[pdf]`"
        ) from e

    try:
        reader = PdfReader(io.BytesIO(data))
    except Exception as e:  # noqa: BLE001 — pypdf raises a forest of types
        raise ExtractorError(f"failed to open PDF: {e}") from e

    pieces: list[str] = []
    for i, page in enumerate(reader.pages):
        try:
            text = page.extract_text() or ""
        except Exception as e:  # noqa: BLE001
            # One bad page shouldn't kill the whole document; record a
            # marker and continue. The downstream LLM-driven outline
            # still produces a coherent page from the rest.
            pieces.append(f"[page {i + 1}: extraction failed: {e}]")
            continue
        text = text.strip()
        if not text:
            continue
        # Page-break marker helps the chunker decide section boundaries
        # later; the Markdown renderer in Brain just treats it as text.
        pieces.append(f"--- page {i + 1} ---\n{text}")
    if not pieces:
        raise ExtractorError("PDF contained no extractable text "
                             "(scan? try OCR by re-submitting as kind=image)")
    return "\n\n".join(pieces)
