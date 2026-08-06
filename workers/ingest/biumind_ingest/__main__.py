"""``python -m biumind_ingest`` launcher.

Configures logging then hands off to the asyncio event loop. Operators
override log level via ``BIUMIND_LOG_LEVEL`` env (default INFO).
"""

from __future__ import annotations

import asyncio
import logging
import os

from .config import Config
from .worker import run


def main() -> None:
    # Python logging 对 level 大小写严格 ('debug' 会 ValueError); .upper() 容忍
    # 两种写法 — Go 服务的 zerolog 不挑,但 Python 必须大写或整数.
    level = os.environ.get("BIUMIND_LOG_LEVEL", "INFO").upper()
    logging.basicConfig(
        level=level,
        format="%(asctime)s %(levelname)s %(name)s %(message)s",
    )
    cfg = Config.from_env()
    asyncio.run(run(cfg))


if __name__ == "__main__":
    main()
