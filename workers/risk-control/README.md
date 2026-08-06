# biumind-risk-control-worker

W4-6 风控 worker — 24h 滚动窗口检测异常消费用户.

## 算法

每小时扫一次 `billing.events`:
1. 拉过去 24h 所有 `kind IN ('consume','settle')` 事件 (`amount` 单位 credits).
2. 按 `user_id` 聚合 `total_credits`.
3. 算所有用户的 p99 基线 (单用户 24h 消费的 99 分位).
4. 任意用户 `total > p99 × 3` 标记 anomaly, 写 `audit.events` + 通过 NATS 发告警.

详见 `biumind_risk/detector.py`.

## 运行

```bash
pip install -e .
DATABASE_URL=postgres://... python -m biumind_risk
```

测试:

```bash
pip install -e ".[dev]"
pytest
```
