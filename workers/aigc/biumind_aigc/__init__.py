"""biumind_aigc — AIGC 生成 worker.

订阅 NATS aigc.task.submit, 调上游 provider (DashScope / VolcEngine), 完成后
转存到 MinIO 并 publish aigc.task.update 让 services/aigc 写库 + 通过
services/realtime 推 SSE 给客户端.

参考: docs/BiuMind-AIGC-Migration-Plan.md §2.2
      docs/BiuMind-AIGC-Storage-Design.md §7
"""

__version__ = "0.1.0"
