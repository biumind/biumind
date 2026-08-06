"""Anomaly detection — 24h rolling window, p99×3 threshold.

Pure algorithm module — no IO. The worker.py orchestrator pulls events
from billing.events and calls detect_anomalies(); test cases below
exercise the math directly without DB.
"""

from __future__ import annotations

import math
from dataclasses import dataclass


@dataclass(frozen=True)
class UserSpend:
    """Per-user 24h aggregate fed into detector."""

    user_id: str
    total_credits: int


@dataclass(frozen=True)
class Anomaly:
    """One offending user. Severity is multiple of p99 baseline."""

    user_id: str
    total_credits: int
    baseline: float
    multiple: float


# THRESHOLD_MULTIPLE — 用户 24h 消费 / p99 baseline 超过这个倍数视为异常.
# 3× 是 dev plan §5.2 W4-6 给定阈值. 调高减少误报, 调低更敏感.
THRESHOLD_MULTIPLE = 3.0

# MIN_SAMPLE_SIZE — 全网用户少于此数时拒绝评估 (p99 不可信).
# dev / 灰度环境数据稀疏时避免误报.
MIN_SAMPLE_SIZE = 10


def percentile(values: list[int], p: float) -> float:
    """Linear-interpolation p-percentile (matches numpy default).

    Empty input returns 0. p ∈ [0, 100].
    """
    if not values:
        return 0.0
    if len(values) == 1:
        return float(values[0])
    sorted_vals = sorted(values)
    rank = (p / 100.0) * (len(sorted_vals) - 1)
    low = math.floor(rank)
    high = math.ceil(rank)
    if low == high:
        return float(sorted_vals[int(rank)])
    weight = rank - low
    return sorted_vals[low] * (1 - weight) + sorted_vals[high] * weight


def detect_anomalies(
    spends: list[UserSpend],
    *,
    threshold_multiple: float = THRESHOLD_MULTIPLE,
    min_sample_size: int = MIN_SAMPLE_SIZE,
) -> list[Anomaly]:
    """Return users whose 24h spend exceeds p99 × threshold_multiple.

    Returns empty list if sample size < min_sample_size (avoids false
    positives in low-traffic windows).
    """
    if len(spends) < min_sample_size:
        return []
    totals = [s.total_credits for s in spends]
    baseline = percentile(totals, 99.0)
    if baseline <= 0:
        return []
    threshold = baseline * threshold_multiple
    out: list[Anomaly] = []
    for s in spends:
        if s.total_credits > threshold:
            out.append(
                Anomaly(
                    user_id=s.user_id,
                    total_credits=s.total_credits,
                    baseline=baseline,
                    multiple=s.total_credits / baseline,
                )
            )
    out.sort(key=lambda a: a.total_credits, reverse=True)
    return out
