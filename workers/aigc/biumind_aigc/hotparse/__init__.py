"""爆款解析 (hotparse) 子模块.

短视频链接 → 下载 (download.py) → ffmpeg 抽音轨 → model-relay STT 转写 →
model-relay LLM 拆解 (prompt.py) → 结构化结果 {文案/钩子/分镜/标签}.

执行器在 providers/hotparse_provider.py。
"""
