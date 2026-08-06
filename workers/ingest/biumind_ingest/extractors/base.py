"""Shared extractor types."""

from __future__ import annotations

from dataclasses import dataclass


class ExtractorError(Exception):
    """Generic extraction failure (corrupt file, decode error, etc.).

    The worker NACKs the job with this message so the publisher can
    decide whether to retry or surface to the user.
    """


class ExtractorUnavailable(ExtractorError):
    """The optional dependency for this kind is not installed.

    Distinct from a generic error so operator dashboards can flag
    "wrong image, no parser" separately from "extractor crashed on a
    valid image".
    """


@dataclass(frozen=True)
class ExtractorContext:
    """Per-call extractor knobs sourced from Config / Job metadata.

    Centralising these here keeps signatures stable as we add formats.
    """
    whisper_model: str
    whisper_device: str
    tesseract_lang: str
