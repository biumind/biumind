"""BiuMind wiki LLM worker.

Subscribes to NATS subject ``biumind.<env>.brain.wiki.ingest.requested``
and turns one source (raw text or referenced source row) into multiple
wiki pages via two-stage CoT prompting:

  Stage 1 (analyze):  classify content, propose page splits, plan titles
  Stage 2 (generate): emit the actual ``---FILE: path---\\n…\\n---END FILE---``
                      blocks streamed back so each finished block lands
                      as a wiki page IMMEDIATELY (streaming partial-save).

Why a separate worker instead of wiring the CoT loop into brain (Go):

  * Multi-page output ≠ single-page direct parse contract
    (workers/wiki-parse owns source file → extracted text)
  * CoT prompts iterate fast in Python with hot-reload; Go service deploy
    is slower
  * Streaming partial-save needs cooperative cancel + chunk-boundary
    state, easier to model in async Python than in a Go service loop
  * License hygiene: knowcode's reference algorithms are GPL; we
    re-implement from llm_wiki TS source, which is easier to audit when
    each domain module is its own file.

Wire payload (from services/brain/internal/wiki/ingest):

    {
        "task_id":    "<uuid>",       # brain.ingest_tasks.id
        "project_id": "<uuid>",
        "owner_id":   "<uuid>",
        "title":      "optional",
        "raw_text":   "...",          # one of raw_text / source_id required
        "source_id":  "<uuid>",       # optional
    }
"""

from .config import Config
from .job import IngestRequest

__all__ = ["Config", "IngestRequest"]
__version__ = "0.1.0"
