"""Image OCR via Tesseract (pytesseract + Pillow)."""

from __future__ import annotations

import io

from .base import ExtractorContext, ExtractorError, ExtractorUnavailable


def extract_image(data: bytes, ctx: ExtractorContext) -> str:
    try:
        from PIL import Image  # type: ignore
        import pytesseract  # type: ignore
    except ImportError as e:
        raise ExtractorUnavailable(
            "image extractor needs `pip install biumind-ingest-worker[ocr]` "
            "and a system `tesseract` binary on PATH"
        ) from e

    try:
        img = Image.open(io.BytesIO(data))
    except Exception as e:  # noqa: BLE001
        raise ExtractorError(f"failed to decode image: {e}") from e

    try:
        text = pytesseract.image_to_string(img, lang=ctx.tesseract_lang)
    except pytesseract.TesseractNotFoundError as e:  # type: ignore[attr-defined]
        raise ExtractorUnavailable(
            "system `tesseract` binary not found on PATH"
        ) from e
    except Exception as e:  # noqa: BLE001 — Tesseract raises subprocess errors
        raise ExtractorError(f"OCR failed: {e}") from e

    text = (text or "").strip()
    if not text:
        raise ExtractorError("OCR produced no text "
                             "(image too noisy / language mismatch?)")
    return text
