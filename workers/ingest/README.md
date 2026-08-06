# biumind-ingest-worker

Python worker that turns binary uploads (PDF / image / audio) into plain
text and hands them off to Brain's existing Go ingestion pipeline via
NATS.

## Why Python?

Go's ecosystem for PDF parsing, OCR, and speech-to-text is thin; Python
has mature libraries (`pypdf`, `pytesseract`, `faster-whisper`) for all
three. We keep the LLM prompting + Wiki write logic in Go (one source
of truth) and use Python *only* as a binary→text adaptor.

## Wire protocol

**Subscribes to**: `biumind.<env>.brain.ingest.binary`

```json
{
  "source_id":  "<uuid>",
  "project_id": "<uuid>",
  "user_id":    "<uuid>",
  "kind":       "pdf" | "image" | "audio",
  "title":      "optional",
  "url":        "optional",
  "data_b64":   "<base64 raw bytes>",
  "metadata":   { "language": "zh" }
}
```

**Re-publishes to**: `biumind.<env>.brain.ingest.requested`

Same shape as `services/brain/internal/ingestbus.Job`. The Go consumer
runs the existing two-step CoT and writes a Wiki page.

## Install

```bash
# pick the extractors you need
pip install ./workers/ingest[pdf,ocr,whisper]

# or just one
pip install ./workers/ingest[pdf]
```

System dependencies:
- **OCR**: `tesseract` binary on `PATH` (`brew install tesseract` /
  `apt install tesseract-ocr tesseract-ocr-chi-sim`).
- **Whisper**: ffmpeg on `PATH` for audio decoding.

## Run

```bash
BIUMIND_NATS_URL=nats://localhost:4222 \
BIUMIND_ENV=dev \
python -m biumind_ingest
```

## Env vars

| name | default | meaning |
|------|---------|---------|
| `BIUMIND_NATS_URL`         | `nats://localhost:4222` | NATS server |
| `BIUMIND_ENV`              | `dev`                   | subject prefix |
| `BIUMIND_INGEST_QUEUE`     | `brain-ingest-py`       | queue group |
| `BIUMIND_INGEST_TIMEOUT_S` | `180`                   | per-job timeout |
| `BIUMIND_WHISPER_MODEL`    | `base`                  | faster-whisper model id |
| `BIUMIND_WHISPER_DEVICE`   | `cpu`                   | `cpu` or `cuda` |
| `BIUMIND_TESSERACT_LANG`   | `eng+chi_sim`           | OCR lang packs |

## Test

```bash
cd workers/ingest && pytest
```

Tests use a stub extractor + in-memory publisher; no NATS / tesseract /
whisper needed.
