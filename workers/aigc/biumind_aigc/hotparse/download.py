"""通用直链下载 + ffmpeg 抽音轨.

Phase1 只支持公网直链 (mp4 / m3u8 等 ffmpeg 原生可读的源)。抖音/小红书/B站
等平台提取器 (yt-dlp) 留 Phase2 (新依赖,需单独拍板)。

ffmpeg 已是 storage/persist.py 的现有依赖 (cover 抽帧/ffprobe),此处复用同一
二进制抽音轨,Phase1 不引入新依赖。
"""

from __future__ import annotations

import asyncio
import logging
import os
import subprocess
import tempfile

import httpx


logger = logging.getLogger("biumind.aigc.hotparse")

# 直链下载体积上限 (防滥用; 短视频通常 <100MB)。
_MAX_VIDEO_BYTES = 200 * 1024 * 1024


class DownloadError(Exception):
    """下载或抽音轨失败。"""


async def download_to_tmp(url: str, *, client: httpx.AsyncClient) -> str:
    """流式下载直链到临时文件,返回本地路径。调用方负责清理。

    Phase1: 仅接受 http(s) 直链。m3u8 不在此下载 (交给 ffmpeg 直接读 URL,
    见 extract_audio 的 m3u8 分支)。
    """
    if not (url.startswith("http://") or url.startswith("https://")):
        raise DownloadError(f"unsupported source url scheme: {url[:40]}")

    # m3u8 播放列表本身很小,但媒体分片要 ffmpeg 拉 —— 这里只处理普通文件直链。
    # m3u8 走 extract_audio 里 ffmpeg -i <url> 直读。
    if _looks_like_m3u8(url):
        return url  # 透传 URL,extract_audio 让 ffmpeg 直接拉流

    fd, path = tempfile.mkstemp(suffix=".mp4")
    os.close(fd)
    total = 0
    try:
        async with client.stream("GET", url, follow_redirects=True) as resp:
            if resp.status_code >= 400:
                raise DownloadError(f"download http {resp.status_code}")
            with open(path, "wb") as f:
                async for chunk in resp.aiter_bytes(64 * 1024):
                    total += len(chunk)
                    if total > _MAX_VIDEO_BYTES:
                        raise DownloadError("video exceeds 200MB limit")
                    f.write(chunk)
    except httpx.HTTPError as e:
        _safe_unlink(path)
        raise DownloadError(f"download failed: {e}") from e
    except Exception:
        _safe_unlink(path)
        raise
    if total == 0:
        _safe_unlink(path)
        raise DownloadError("downloaded 0 bytes")
    logger.info("hotparse: downloaded %d bytes → %s", total, path)
    return path


async def extract_audio(video_path_or_url: str) -> str:
    """ffmpeg 从视频抽音轨 → m4a 临时文件,返回路径。调用方负责清理。

    video_path_or_url 可以是本地路径或 m3u8 URL(ffmpeg 直读)。
    """
    fd, out_path = tempfile.mkstemp(suffix=".m4a")
    os.close(fd)
    cmd = [
        "ffmpeg", "-y", "-loglevel", "error",
        "-i", video_path_or_url,
        "-vn",               # 去视频流
        "-acodec", "aac",
        "-b:a", "128k",
        "-ac", "1",          # 单声道,转写够用且更小
        out_path,
    ]
    try:
        # ffmpeg 是阻塞 subprocess,丢到线程池避免卡 event loop。
        await asyncio.to_thread(
            subprocess.run, cmd, check=True, capture_output=True, timeout=300,
        )
    except subprocess.CalledProcessError as e:
        _safe_unlink(out_path)
        stderr = (e.stderr or b"").decode("utf-8", "replace")[:300]
        raise DownloadError(f"ffmpeg extract audio failed: {stderr}") from e
    except subprocess.TimeoutExpired as e:
        _safe_unlink(out_path)
        raise DownloadError("ffmpeg extract audio timeout") from e
    if os.path.getsize(out_path) == 0:
        _safe_unlink(out_path)
        raise DownloadError("extracted audio is empty (无音轨?)")
    return out_path


def _looks_like_m3u8(url: str) -> bool:
    base = url.split("?", 1)[0].lower()
    return base.endswith(".m3u8")


def _safe_unlink(path: str) -> None:
    try:
        os.unlink(path)
    except OSError:
        pass
