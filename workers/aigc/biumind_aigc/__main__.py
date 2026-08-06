"""``python -m biumind_aigc`` launcher."""

from __future__ import annotations

import asyncio
import logging
import os

from .config import Config
from .worker import run


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
