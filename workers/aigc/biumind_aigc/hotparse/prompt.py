"""爆款拆解的 LLM prompt 工程 + 结构化输出解析.

worker 把 STT 转写文本喂给 model-relay /v1/internal/chat(Anthropic 兼容),
要求 LLM 严格输出 JSON。本模块负责构造 messages + 容错解析(剥 markdown
code fence、校验字段、补默认值)。

输出 schema:
    {
      "copywriting": "完整文案改写 (口播稿)",
      "hooks": ["前3秒钩子1", "钩子2"],
      "scenes": [
        {"index": 1, "description": "画面描述",
         "prompt": "可直接喂文生图/视频的成品提示词", "duration_hint_s": 3}
      ],
      "tags": ["标签1", "标签2"]
    }
"""

from __future__ import annotations

import json
from typing import Any


SYSTEM_PROMPT = (
    "你是爆款短视频拆解专家。根据给定的视频转写文本,拆解出可复用的创作要素。"
    "严格只输出一个 JSON 对象,不要 markdown 代码块、不要任何解释文字。"
    "JSON schema:\n"
    '{"copywriting": "完整文案改写(口播稿)",'
    ' "hooks": ["前3秒钩子,2-4 条"],'
    ' "scenes": [{"index": 1, "description": "画面描述",'
    ' "prompt": "可直接用于 AI 文生图/视频的成品提示词,需具体含主体/画面/风格",'
    ' "duration_hint_s": 3}],'
    ' "tags": ["话题标签,3-8 个"]}\n'
    "scenes 按视频叙事顺序给 3-8 个。prompt 字段是关键,必须是成品级提示词。"
)


def build_messages(transcript: str) -> list[dict[str, Any]]:
    """构造 Anthropic messages(system 走单独字段,这里只给 user turn)。"""
    text = (transcript or "").strip()
    if not text:
        text = "(转写为空,请基于一个通用爆款短视频结构给出可复用模板)"
    user = "以下是某条爆款短视频的转写文本,请拆解:\n\n" + text
    return [{"role": "user", "content": user}]


def _strip_fence(s: str) -> str:
    """剥掉 ```json ... ``` 或 ``` ... ``` 代码块围栏。"""
    t = s.strip()
    if t.startswith("```"):
        # 去掉首行 ``` 或 ```json
        nl = t.find("\n")
        if nl != -1:
            t = t[nl + 1 :]
        if t.rstrip().endswith("```"):
            t = t.rstrip()[:-3]
    return t.strip()


def _extract_json_object(s: str) -> str:
    """从可能含杂质的文本里截取第一个 {...} JSON 对象(平衡花括号)。"""
    start = s.find("{")
    if start == -1:
        return s
    depth = 0
    for i in range(start, len(s)):
        c = s[i]
        if c == "{":
            depth += 1
        elif c == "}":
            depth -= 1
            if depth == 0:
                return s[start : i + 1]
    return s[start:]


def parse_result(text: str) -> dict[str, Any]:
    """把 LLM 返回文本解析成结构化拆解结果。容错 + 字段归一。

    解析失败抛 ValueError(由 HotparseProvider 转成 failed Outcome)。
    """
    raw = _extract_json_object(_strip_fence(text or ""))
    try:
        data = json.loads(raw)
    except (ValueError, TypeError) as e:
        raise ValueError(f"LLM 拆解输出非合法 JSON: {e}") from e
    if not isinstance(data, dict):
        raise ValueError("LLM 拆解输出顶层不是 JSON 对象")

    copywriting = str(data.get("copywriting") or "").strip()
    hooks = [str(h).strip() for h in (data.get("hooks") or []) if str(h).strip()]
    tags = [str(t).strip() for t in (data.get("tags") or []) if str(t).strip()]

    scenes_out: list[dict[str, Any]] = []
    for i, sc in enumerate(data.get("scenes") or []):
        if not isinstance(sc, dict):
            continue
        prompt = str(sc.get("prompt") or "").strip()
        if not prompt:
            continue  # 没有可用 prompt 的分镜对"一键同款"无意义,丢弃
        scenes_out.append(
            {
                "index": int(sc.get("index") or (i + 1)),
                "description": str(sc.get("description") or "").strip(),
                "prompt": prompt,
                "duration_hint_s": _num(sc.get("duration_hint_s")),
            }
        )

    if not copywriting and not scenes_out:
        raise ValueError("LLM 拆解结果为空(无文案且无可用分镜)")

    return {
        "copywriting": copywriting,
        "hooks": hooks,
        "scenes": scenes_out,
        "tags": tags,
    }


def _num(v: Any) -> float | int:
    try:
        f = float(v)
        return int(f) if f.is_integer() else f
    except (TypeError, ValueError):
        return 0
