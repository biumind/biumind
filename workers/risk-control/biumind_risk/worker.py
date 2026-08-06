"""Main worker loop — pulls last 24h of billing.events, runs detector,
writes anomalies to audit.events / risk_alerts.

Minimal orchestrator; algorithm in detector.py is the testable part.
"""

from __future__ import annotations

import logging
import os
import time
from datetime import datetime, timedelta, timezone

import psycopg

from .detector import UserSpend, detect_anomalies

logger = logging.getLogger("biumind.risk")


def fetch_24h_spends(conn: psycopg.Connection) -> list[UserSpend]:
    """Aggregate billing.events for last 24h by user_id."""
    cutoff = datetime.now(timezone.utc) - timedelta(hours=24)
    with conn.cursor() as cur:
        cur.execute(
            """
            SELECT user_id::text, COALESCE(sum(amount), 0)::bigint AS total
            FROM billing.events
            WHERE kind IN ('consume','settle')
              AND occurred_at >= %s
            GROUP BY user_id
            """,
            (cutoff,),
        )
        return [UserSpend(user_id=row[0], total_credits=int(row[1])) for row in cur.fetchall()]


def write_alert(conn: psycopg.Connection, anomaly_user_id: str, total: int, baseline: float, multiple: float) -> None:
    """Append a row to identity.risk_alerts (创建 if missing).

    Idempotent on (user_id, day) — same user only flagged once per day.
    """
    with conn.cursor() as cur:
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS identity.risk_alerts (
                id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
                user_id         uuid NOT NULL,
                day             date NOT NULL DEFAULT CURRENT_DATE,
                total_credits   bigint NOT NULL,
                p99_baseline    numeric(20, 4) NOT NULL,
                multiple        numeric(10, 4) NOT NULL,
                resolved        boolean NOT NULL DEFAULT false,
                created_at      timestamptz NOT NULL DEFAULT now(),
                UNIQUE (user_id, day)
            )
            """
        )
        cur.execute(
            """
            INSERT INTO identity.risk_alerts (user_id, total_credits, p99_baseline, multiple)
            VALUES (%s, %s, %s, %s)
            ON CONFLICT (user_id, day) DO UPDATE SET
                total_credits = EXCLUDED.total_credits,
                p99_baseline  = EXCLUDED.p99_baseline,
                multiple      = EXCLUDED.multiple
            """,
            (anomaly_user_id, total, baseline, multiple),
        )
    conn.commit()


def run_once(conn: psycopg.Connection) -> int:
    """One detection cycle. Returns number of anomalies written.

    Runs in a single transaction; safe to invoke from cron / subprocess.
    """
    spends = fetch_24h_spends(conn)
    logger.debug("risk: fetched 24h spends users=%d", len(spends))
    anomalies = detect_anomalies(spends)
    logger.debug("risk: detector ran users=%d anomalies=%d",
                 len(spends), len(anomalies))
    for a in anomalies:
        logger.warning(
            "risk_alert user=%s total=%d baseline=%.1f multiple=%.2fx",
            a.user_id, a.total_credits, a.baseline, a.multiple,
        )
        write_alert(conn, a.user_id, a.total_credits, a.baseline, a.multiple)
    return len(anomalies)


def main() -> None:
    # 跟其他三个 worker (aigc / ingest / wiki-llm) 的入口对齐 — BIUMIND_LOG_LEVEL
    # 控制全部 Python worker 的级别;之前硬编码 INFO 是 risk-control 单点偏差。
    level = os.environ.get("BIUMIND_LOG_LEVEL", "INFO").upper()
    logging.basicConfig(level=level, format="%(asctime)s %(levelname)s %(name)s %(message)s")
    db_url = os.environ.get("DATABASE_URL")
    if not db_url:
        raise SystemExit("DATABASE_URL required")
    interval = int(os.environ.get("RISK_INTERVAL_SECONDS", "3600"))
    logger.info("risk-control starting interval=%ds", interval)
    while True:
        try:
            with psycopg.connect(db_url) as conn:
                n = run_once(conn)
                logger.info("cycle done anomalies=%d", n)
        except Exception:  # broad — keep worker alive
            logger.exception("cycle failed")
        time.sleep(interval)
