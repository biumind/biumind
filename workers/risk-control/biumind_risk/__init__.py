"""W4-6 BiuMind risk-control worker.

Detects anomalous credit-spending users via 24h rolling p99 baselines.
"""

from .detector import Anomaly, detect_anomalies

__all__ = ["Anomaly", "detect_anomalies"]
