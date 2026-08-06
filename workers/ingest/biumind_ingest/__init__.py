"""BiuMind binary ingestion worker.

Subscribes to NATS subject ``biumind.<env>.brain.ingest.binary`` and
turns binary inputs (PDF / image / audio) into plain text, then
re-publishes onto ``biumind.<env>.brain.ingest.requested`` (kind="plain")
so the existing Brain Go pipeline (two-step CoT → Wiki page) handles
the rest.

Why two stages?
  - Go owns the LLM prompting + Wiki write contract (one source of truth).
  - Python owns the binary-format parsers (Tesseract / Whisper / pypdf
    are mature in Python; the Go ecosystem is weak there).
  - The boundary is one NATS hop with the same Job schema, so adding a
    new format means writing one extractor, not modifying Go.
"""

from .config import Config
from .job import BinaryJob, TextJob

__all__ = ["Config", "BinaryJob", "TextJob"]
__version__ = "0.1.0"
