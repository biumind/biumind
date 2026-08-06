"""W4-6 detector tests — 3 cases per dev plan §5.2."""

from __future__ import annotations

from biumind_risk.detector import (
    THRESHOLD_MULTIPLE,
    Anomaly,
    UserSpend,
    detect_anomalies,
    percentile,
)


def test_percentile_basic() -> None:
    # numpy reference: np.percentile([1..100], 99) == 99.01
    assert abs(percentile(list(range(1, 101)), 99.0) - 99.01) < 0.5
    assert percentile([], 99.0) == 0.0
    assert percentile([42], 99.0) == 42.0


# ────────────────────────────────────────────────────────
# Case 1: Normal traffic, no anomalies — all users similar.
# ────────────────────────────────────────────────────────
def test_normal_no_anomalies() -> None:
    spends = [UserSpend(f"u{i}", 100 + i) for i in range(50)]  # 100..149
    anomalies = detect_anomalies(spends)
    assert anomalies == [], f"unexpected anomalies in normal traffic: {anomalies}"


# ────────────────────────────────────────────────────────
# Case 2: One outlier 100x normal — flagged.
# ────────────────────────────────────────────────────────
def test_outlier_flagged() -> None:
    # 99 normal users + 1 abusive user — the abuser is high enough up the
    # tail that even after contaminating p99 the threshold is still cleared.
    spends = [UserSpend(f"u{i}", 100 + i) for i in range(99)]
    spends.append(UserSpend("abuser", 100_000))

    anomalies = detect_anomalies(spends)
    assert len(anomalies) == 1
    a = anomalies[0]
    assert a.user_id == "abuser"
    assert a.total_credits == 100_000
    assert a.multiple > THRESHOLD_MULTIPLE


# ────────────────────────────────────────────────────────
# Case 3: Boundary — user at exactly p99×3, NOT flagged (>strict).
# ────────────────────────────────────────────────────────
def test_boundary_strict_gt() -> None:
    spends = [UserSpend(f"u{i}", 100) for i in range(99)]
    # Add a user whose total = p99 × 3 exactly = 300.
    spends.append(UserSpend("borderline", 300))

    anomalies = detect_anomalies(spends)
    # 100 should equal p99 (all values are 100), 300 = exactly 3×; not flagged
    # because the rule is strict-greater-than.
    assert len(anomalies) == 0


# ────────────────────────────────────────────────────────
# Case 4: Sample too small — refuse to evaluate.
# ────────────────────────────────────────────────────────
def test_small_sample_skipped() -> None:
    spends = [UserSpend(f"u{i}", 100_000) for i in range(5)]  # below MIN_SAMPLE_SIZE
    assert detect_anomalies(spends) == []


# ────────────────────────────────────────────────────────
# Case 5: Multiple anomalies — sorted descending by total.
# ────────────────────────────────────────────────────────
def test_multiple_anomalies_sorted() -> None:
    # 998 normals + 2 abusers — abusers don't pollute p99 enough to mask each other.
    spends = [UserSpend(f"u{i}", 100) for i in range(998)]
    spends.append(UserSpend("medium_abuser", 10_000))
    spends.append(UserSpend("severe_abuser", 50_000))

    anomalies = detect_anomalies(spends)
    assert len(anomalies) == 2, f"got {anomalies}"
    assert anomalies[0].user_id == "severe_abuser"
    assert anomalies[1].user_id == "medium_abuser"


def test_anomaly_dataclass_immutable() -> None:
    a = Anomaly(user_id="x", total_credits=1, baseline=1.0, multiple=1.0)
    try:
        a.total_credits = 999  # type: ignore[misc]
    except AttributeError:
        return
    raise AssertionError("Anomaly should be frozen")
