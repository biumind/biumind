"""Provider 抽象 + 注册中心.

每个 provider 实现 base.Executor 接口. 注册到 REGISTRY 后, worker 按
SubmitTask.provider_code 路由.

段 3.6 后:dashscope / volcengine 的生成统一经 model-relay
(/v1/internal/generations, RelayProvider) —— worker 不再直连上游, 原
dashscope_*.py / volcengine_*.py 直连 provider 已删除。get() 在
AIGC_GENERATE_VIA_RELAY=true(默认)时把这些 provider_code 路由到
RelayProvider;REGISTRY 只保留 stub(测试用)。
"""

from __future__ import annotations

from typing import Callable

from .base import Executor, Outcome, ProviderError, ProviderUnavailable, StubProvider
from ..config import Config


# ProviderFactory: (Config) → Executor. 注册函数而非实例,
# 让昂贵的 SDK 客户端按需懒构造.
ProviderFactory = Callable[[Config], Executor]


REGISTRY: dict[str, ProviderFactory] = {
    # 测试 stub — 不调上游, 立即返回 fake output.
    "stub": lambda cfg: StubProvider(),
}


def register(code: str, factory: ProviderFactory) -> None:
    REGISTRY[code] = factory


def get(code: str, cfg: Config) -> Executor:
    # 爆款解析: provider_code='hotparse' → HotparseProvider(经 model-relay
    # 内部 STT/chat 端点执行,非生成上游,故先于 relay 名单判断)。
    if code == "hotparse":
        from .hotparse_provider import HotparseProvider
        return HotparseProvider(cfg)

    # 段3.6: flag 开(默认)+ provider 在 relay 名单内 → 走 model-relay 单一
    # egress(生成真正过 relay)。dashscope / volcengine 的直连 provider 已删,
    # 仅此一条路径。
    if getattr(cfg, "generate_via_relay", True):
        from .relay_provider import RELAY_PROVIDERS, RelayProvider
        if code in RELAY_PROVIDERS:
            return RelayProvider(cfg)
    factory = REGISTRY.get(code)
    if factory is None:
        raise ProviderUnavailable(
            f"provider {code!r} not registered "
            f"(直连 provider 已于段3.6 删除; 确认 AIGC_GENERATE_VIA_RELAY=true)"
        )
    return factory(cfg)


__all__ = [
    "Executor",
    "Outcome",
    "ProviderError",
    "ProviderUnavailable",
    "StubProvider",
    "REGISTRY",
    "register",
    "get",
]
