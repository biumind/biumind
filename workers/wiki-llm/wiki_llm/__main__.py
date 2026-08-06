"""``python -m wiki_llm`` launcher.

Mirrors workers/ingest's __main__ — set up logging from BIUMIND_LOG_LEVEL
then hand off to asyncio. Config loads from env at boot; failures here
should crash the container, not start a silent worker.
"""

from __future__ import annotations

import asyncio
import logging
import os

from .config import Config
from .runner import run


def main() -> None:
    level = os.environ.get("BIUMIND_LOG_LEVEL", "INFO").upper()
    logging.basicConfig(
        level=level,
        format="%(asctime)s %(levelname)s %(name)s %(message)s",
    )
    cfg = Config.from_env()
    asyncio.run(run(cfg))


if __name__ == "__main__":
    main()
