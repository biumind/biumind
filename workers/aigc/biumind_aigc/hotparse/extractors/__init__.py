"""爆款解析平台提取器 (Phase2).

短视频分享链接 → yt-dlp 拿到媒体 → 本地文件,交回 HotparseProvider 抽音轨。
Phase2 支持: upload(直链, 走 download.py)、bilibili、douyin。
小红书(xiaohongshu)暂缓(yt-dlp 支持差 + 反爬最强 + 法务风险最高)。

yt-dlp 按 URL 自动选 extractor,故 bilibili/douyin 共用一条 ytdlp_download
路径;source enum 仅用于白名单校验 + 失败提示。为省带宽只下载音轨
(format=ba/b),STT 不需要画面。

失败抛 ExtractError → HotparseProvider 转成明确 failed(提示改用直链),不崩 worker。
"""

from __future__ import annotations

import asyncio
import logging
import os
import tempfile
from typing import Any

logger = logging.getLogger("biumind.aigc.hotparse")

# Phase2 已支持的 source。upload=公网直链;douyin/bilibili 走 yt-dlp。
PLATFORM_SOURCES = {"douyin", "bilibili"}
SUPPORTED_SOURCES = {"upload"} | PLATFORM_SOURCES

# 暂缓但需给出友好提示的 source。
DEFERRED_SOURCES = {"xiaohongshu", "xhs", "kuaishou", "wechat_channel"}


class ExtractError(Exception):
    """平台提取失败(不可用 / 反爬拦截 / 暂不支持)。"""


def detect_source(url: str) -> str:
    """从 URL host 推断 source(客户端未显式给 source 时兜底)。"""
    u = url.lower()
    if "bilibili.com" in u or "b23.tv" in u:
        return "bilibili"
    if "douyin.com" in u or "iesdouyin" in u:
        return "douyin"
    if "xiaohongshu.com" in u or "xhslink.com" in u:
        return "xiaohongshu"
    if "kuaishou.com" in u:
        return "kuaishou"
    return "upload"


async def ytdlp_download(url: str, *, max_bytes: int = 200 * 1024 * 1024) -> tuple[str, dict[str, Any]]:
    """用 yt-dlp 下载音轨到临时文件,返回 (path, meta{cover_url,duration_ms,title})。

    只下载 bestaudio(format=ba/b)以省带宽 —— STT 不需要画面。调用方负责清理
    返回的文件(及其所在临时目录)。
    """
    return await asyncio.to_thread(_ytdlp_download_blocking, url, max_bytes)


def _ytdlp_download_blocking(url: str, max_bytes: int) -> tuple[str, dict[str, Any]]:
    try:
        import yt_dlp  # 延迟 import: 仅 hotparse extra 装了才需要
    except ImportError as e:  # pragma: no cover
        raise ExtractError("yt-dlp 未安装 (worker 需装 [hotparse]/[all] extra)") from e

    tmpdir = tempfile.mkdtemp(prefix="hotparse-ytdlp-")
    outtmpl = os.path.join(tmpdir, "%(id)s.%(ext)s")
    opts = {
        "outtmpl": outtmpl,
        "format": "ba/b",          # bestaudio, 退回 best
        "noplaylist": True,
        "quiet": True,
        "no_warnings": True,
        "max_filesize": max_bytes,
        "retries": 2,
        "socket_timeout": 30,
    }
    cookiefile = os.environ.get("AIGC_YTDLP_COOKIES")
    if cookiefile:
        opts["cookiefile"] = cookiefile

    try:
        with yt_dlp.YoutubeDL(opts) as ydl:
            info = ydl.extract_info(url, download=True)
            filename = ydl.prepare_filename(info)
    except Exception as e:  # yt_dlp.utils.DownloadError 等
        _rmtree(tmpdir)
        raise ExtractError(f"yt-dlp 提取失败: {str(e)[:300]}") from e

    path = _resolve_downloaded(filename)
    if not path:
        _rmtree(tmpdir)
        raise ExtractError("yt-dlp 未产出可用文件 (可能被限制大小或反爬拦截)")

    meta = {
        "cover_url": info.get("thumbnail") or "",
        "duration_ms": int((info.get("duration") or 0) * 1000),
        "title": info.get("title") or "",
    }
    return path, meta


def _resolve_downloaded(prepared: str) -> str | None:
    """prepare_filename 给的扩展名可能与实际下载/合并后的不一致,容错查找。"""
    if os.path.isfile(prepared):
        return prepared
    base = os.path.splitext(prepared)[0]
    for ext in (".m4a", ".mp3", ".webm", ".opus", ".mp4", ".mkv", ".aac"):
        cand = base + ext
        if os.path.isfile(cand):
            return cand
    return None


def cleanup(path: str) -> None:
    """清理 ytdlp 下载文件及其临时目录。"""
    if not path:
        return
    d = os.path.dirname(path)
    if os.path.basename(d).startswith("hotparse-ytdlp-"):
        _rmtree(d)
    else:
        try:
            os.unlink(path)
        except OSError:
            pass


def _rmtree(d: str) -> None:
    import shutil
    try:
        shutil.rmtree(d, ignore_errors=True)
    except OSError:
        pass
