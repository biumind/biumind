"""wiki-parse worker entrypoint."""

from __future__ import annotations

import asyncio
import logging
import os
import sys

from .config import Config
from .runner import run


def main() -> None:
    level = os.environ.get("BIUMIND_LOG_LEVEL", "INFO").upper()
    logging.basicConfig(
        level=getattr(logging, level, logging.INFO),
        format="%(asctime)s %(levelname)s %(name)s: %(message)s",
    )
    # httpx/httpcore 默认 DEBUG 噪音大（每请求 connect/headers/body 多行），
    # 降到 WARNING —— 业务日志（wiki_parse logger）仍按 BIUMIND_LOG_LEVEL。
    logging.getLogger("httpx").setLevel(logging.WARNING)
    logging.getLogger("httpcore").setLevel(logging.WARNING)
    cfg = Config.from_env()
    try:
        asyncio.run(run(cfg))
    except KeyboardInterrupt:
        sys.exit(0)


if __name__ == "__main__":
    main()
