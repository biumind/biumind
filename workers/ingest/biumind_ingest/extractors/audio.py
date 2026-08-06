"""Audio transcription via ``faster-whisper``.

faster-whisper is the CTranslate2 port of OpenAI Whisper — significantly
faster on CPU than openai-whisper, and the API is stable across model
sizes (``base`` for dev, ``medium``/``large-v3`` for prod).
"""

from __future__ import annotations

import os
import tempfile

from .base import ExtractorContext, ExtractorError, ExtractorUnavailable


# Cache of (model_id, device) -> WhisperModel. Loading a model is
# expensive (300MB-1.5GB download + warmup) so we keep them resident
# for the worker's lifetime. Only one device per model is supported;
# changing devices requires a worker restart.
_MODEL_CACHE: dict[tuple[str, str], object] = {}


def extract_audio(data: bytes, ctx: ExtractorContext) -> str:
    try:
        from faster_whisper import WhisperModel  # type: ignore
    except ImportError as e:
        raise ExtractorUnavailable(
            "audio extractor needs `pip install biumind-ingest-worker[whisper]`"
        ) from e

    key = (ctx.whisper_model, ctx.whisper_device)
    model = _MODEL_CACHE.get(key)
    if model is None:
        try:
            model = WhisperModel(ctx.whisper_model,
                                 device=ctx.whisper_device,
                                 compute_type="auto")
        except Exception as e:  # noqa: BLE001 — Whisper crashes are varied
            raise ExtractorError(
                f"failed to load Whisper model {ctx.whisper_model!r}: {e}"
            ) from e
        _MODEL_CACHE[key] = model

    # faster-whisper takes a path or a file-like with seek; safer to
    # write a temp file and let the underlying ffmpeg detect the format.
    suffix = ".audio"
    with tempfile.NamedTemporaryFile(suffix=suffix, delete=False) as fh:
        fh.write(data)
        path = fh.name
    try:
        segments, _info = model.transcribe(path, vad_filter=True)  # type: ignore[attr-defined]
        pieces = [seg.text.strip() for seg in segments if seg.text.strip()]
    except Exception as e:  # noqa: BLE001
        raise ExtractorError(f"whisper transcription failed: {e}") from e
    finally:
        try:
            os.unlink(path)
        except OSError:
            pass

    if not pieces:
        raise ExtractorError(
            "whisper produced no segments (silent audio? unsupported codec?)"
        )
    return "\n".join(pieces)
