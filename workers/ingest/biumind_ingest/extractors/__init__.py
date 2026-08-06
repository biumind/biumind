"""Extractor registry.

Each extractor declares ``KIND`` and exposes ``extract(data, ctx) -> str``.
The worker picks one by job kind. Missing optional deps surface as
:class:`ExtractorUnavailable` so the Job can be NACK'd with a clear
error rather than crashing the worker.
"""

from __future__ import annotations

from typing import Callable, Dict

from .base import ExtractorContext, ExtractorError, ExtractorUnavailable
from .pdf import extract_pdf
from .image import extract_image
from .audio import extract_audio


# kind → callable. Each callable accepts (bytes, ExtractorContext) and
# returns plain text (UTF-8). Raises ExtractorError / ExtractorUnavailable.
REGISTRY: Dict[str, Callable[[bytes, ExtractorContext], str]] = {
    "pdf": extract_pdf,
    "image": extract_image,
    "audio": extract_audio,
}


def get(kind: str) -> Callable[[bytes, ExtractorContext], str]:
    if kind not in REGISTRY:
        raise ExtractorError(f"unknown kind {kind!r}")
    return REGISTRY[kind]


__all__ = [
    "REGISTRY",
    "get",
    "ExtractorContext",
    "ExtractorError",
    "ExtractorUnavailable",
]
