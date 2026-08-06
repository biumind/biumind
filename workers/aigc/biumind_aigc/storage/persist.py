"""Storage persistence — 把厂商 URL 流式转存到 MinIO, 计算 sha256, 抽元信息.

设计来源: docs/BiuMind-AIGC-Storage-Design.md §7.2

流程:
  1. 流式下载厂商 URL → 临时文件 (chunk 64KiB; 不全量加载内存, 视频 ≥ 100MB)
  2. 同时计算 sha256 + 真实 mime (sniff 头部, 不信任 Content-Type)
  3. 拒绝非白名单 mime (image/jpeg|png|webp|avif, video/mp4|webm)
  4. 图片: 打开取 width/height
     视频: ffprobe 拿 width/height/duration_ms; ffmpeg 抽 cover (1 秒处单帧, 转存到 derivatives/)
  5. PUT 到 outputs/<sha[:2]>/<sha[2:4]>/<sha>.<ext> (CAS, 自动去重)
  6. 返回 PersistedOutput, 给 worker 发 task.update.

worker 在 task=completed 时调 Persister.persist_url() 替换原始 outcome URL.

与已有 outcome.output_meta 兼容: meta 里的 cover_url 字段会触发额外转存
(单独 persist 一次到 derivatives/<video_sha>/cover.<ext>).

依赖 [storage] extra (boto3 + Pillow + ffmpeg-python). 没装时 Persister 抛
ImportError 让 worker 启动早失败 (生产 image 必须装).
"""

from __future__ import annotations

import dataclasses
import hashlib
import logging
import os
import subprocess
import tempfile
from dataclasses import dataclass
from typing import Iterable, Optional

import httpx


logger = logging.getLogger("biumind.aigc.storage")


# ─── Errors ───────────────────────────────────────────


class PersistError(Exception):
    """转存过程中的可恢复错 (网络 / mime 不被允许 / ffprobe 失败 等)."""


# ─── 真实 mime sniff (头部魔数, 不依赖外部 lib) ───────


_MIME_BY_MAGIC: list[tuple[bytes, str, str]] = [
    # (magic prefix, mime, extension)
    (b"\xff\xd8\xff",                                "image/jpeg", "jpg"),
    (b"\x89PNG\r\n\x1a\n",                           "image/png",  "png"),
    (b"RIFF",                                         "image/webp", "webp"),  # 后续要 4 字节后 'WEBP' 二次校验
    (b"GIF87a",                                       "image/gif",  "gif"),
    (b"GIF89a",                                       "image/gif",  "gif"),
    (b"\x00\x00\x00",                                 "video/mp4",  "mp4"),  # ftyp box, 后续二次校验
    (b"\x1aE\xdf\xa3",                                "video/webm", "webm"),
]

# 允许的 mime → 扩展名 映射
ALLOWED_MIMES: dict[str, str] = {
    "image/jpeg": "jpg",
    "image/png":  "png",
    "image/webp": "webp",
    "image/gif":  "gif",
    "video/mp4":  "mp4",
    "video/webm": "webm",
    "image/avif": "avif",
}


def sniff_mime(head: bytes) -> Optional[str]:
    """从字节头识别 mime; 不在白名单返 None."""
    if not head:
        return None
    if head[:3] == b"\xff\xd8\xff":
        return "image/jpeg"
    if head[:8] == b"\x89PNG\r\n\x1a\n":
        return "image/png"
    if head[:4] == b"RIFF" and len(head) >= 12 and head[8:12] == b"WEBP":
        return "image/webp"
    if head[:6] in (b"GIF87a", b"GIF89a"):
        return "image/gif"
    # MP4 ftyp box: 第 4..8 字节是 "ftyp"
    if len(head) >= 12 and head[4:8] == b"ftyp":
        # 再看 brand: avif → image/avif, 其他视为 video/mp4
        brand = head[8:12]
        if brand in (b"avif", b"avis"):
            return "image/avif"
        return "video/mp4"
    if head[:4] == b"\x1aE\xdf\xa3":
        return "video/webm"
    return None


# ─── Helpers ──────────────────────────────────────────


def cas_key(prefix: str, sha: str, ext: str) -> str:
    """CAS 路径: <prefix>/<sha[:2]>/<sha[2:4]>/<sha>.<ext>."""
    return f"{prefix}/{sha[:2]}/{sha[2:4]}/{sha}.{ext}"


@dataclass
class PersistedOutput:
    """persist_url 返回值, worker 用它构造 OutputEntry."""

    sha256: str
    storage_url: str       # "cas:<sha>"
    storage_key: str       # MinIO 桶内 key (admin 视野)
    mime_type: str
    file_size: int
    width: int = 0
    height: int = 0
    duration_ms: int = 0
    cover_sha: str = ""    # 视频专用; 已转存到 derivatives bucket
    kind: str = ""         # image | video
    blurhash: str = ""     # 仅 image 填. 客户端首帧加载时用作占位.


# ─── Persister ────────────────────────────────────────


class Persister:
    """单例 (worker 进程级), 持有 boto3 client + httpx client.

    构造时注入 cfg.bucket_outputs / bucket_derivatives 等. boto3 / Pillow / ffmpeg-python
    懒 import 让没装 [storage] extra 时单元测试 (sniff_mime / cas_key 等纯函数) 仍可跑.
    """

    def __init__(
        self,
        *,
        s3_endpoint: str,
        s3_access_key: str,
        s3_secret_key: str,
        s3_region: str,
        bucket_outputs: str,
        bucket_derivatives: str,
        http_timeout_s: float = 60.0,
    ) -> None:
        try:
            import boto3  # type: ignore
        except ImportError as e:
            raise ImportError(
                "boto3 not installed; install workers/aigc with `[storage]` extra"
            ) from e

        self._s3 = boto3.client(
            "s3",
            endpoint_url=s3_endpoint,
            aws_access_key_id=s3_access_key,
            aws_secret_access_key=s3_secret_key,
            region_name=s3_region,
        )
        self._bucket_outputs = bucket_outputs
        self._bucket_derivatives = bucket_derivatives
        self._http = httpx.AsyncClient(
            timeout=http_timeout_s,
            follow_redirects=True,
            limits=httpx.Limits(max_connections=8),
        )

    async def aclose(self) -> None:
        await self._http.aclose()

    # ─── 主入口 ───────────────────────────────────────

    async def persist_url(self, url: str, *, kind: str) -> PersistedOutput:
        """流式下载 url, 转存 MinIO outputs bucket; 抽元信息. 返回 PersistedOutput."""
        if kind not in ("image", "video", "audio", "cover"):
            raise PersistError(f"unsupported kind {kind!r}")

        with tempfile.NamedTemporaryFile(delete=False, suffix=".bin") as tmp:
            tmp_path = tmp.name
        try:
            sha, size, head = await self._download_to(tmp_path, url)
            mime = sniff_mime(head)
            if mime is None or mime not in ALLOWED_MIMES:
                raise PersistError(f"unsupported mime: head={head[:16]!r}")
            ext = ALLOWED_MIMES[mime]

            # 元信息 + 派生
            width = height = duration_ms = 0
            cover_sha = ""
            blurhash = ""
            if mime.startswith("image/"):
                width, height = _probe_image(tmp_path)
                blurhash = _compute_blurhash(tmp_path)
            elif mime.startswith("video/"):
                width, height, duration_ms = _ffprobe_video(tmp_path)
                cover_sha = await self._extract_and_persist_cover(tmp_path, sha)

            # 上传 outputs
            key = cas_key("outputs", sha, ext)
            self._s3.upload_file(
                Filename=tmp_path,
                Bucket=self._bucket_outputs,
                Key=key,
                ExtraArgs={"ContentType": mime},
            )
            return PersistedOutput(
                sha256=sha,
                storage_url=f"cas:{sha}",
                storage_key=key,
                mime_type=mime,
                file_size=size,
                width=width, height=height,
                duration_ms=duration_ms,
                cover_sha=cover_sha,
                blurhash=blurhash,
                kind=kind,
            )
        finally:
            try:
                os.unlink(tmp_path)
            except FileNotFoundError:
                pass

    # ─── 内部 ─────────────────────────────────────────

    async def _download_to(self, path: str, url: str) -> tuple[str, int, bytes]:
        """流式写到 path, 返回 (sha256_hex, size, first_chunk_head_16B)."""
        h = hashlib.sha256()
        size = 0
        head = b""
        try:
            async with self._http.stream("GET", url) as resp:
                if resp.status_code != 200:
                    raise PersistError(
                        f"download {url[:80]} status={resp.status_code}"
                    )
                with open(path, "wb") as f:
                    async for chunk in resp.aiter_bytes(chunk_size=64 * 1024):
                        if not head:
                            head = chunk[:16]
                        h.update(chunk)
                        size += len(chunk)
                        f.write(chunk)
        except httpx.HTTPError as e:
            raise PersistError(f"download http: {e}") from e
        if size == 0:
            raise PersistError("download empty body")
        return h.hexdigest(), size, head

    async def _extract_and_persist_cover(self, video_path: str, video_sha: str) -> str:
        """ffmpeg -ss 1 -frames:v 1 抽帧 → 转存到 derivatives bucket. 返回 cover sha."""
        cover_tmp = video_path + ".cover.jpg"
        try:
            try:
                subprocess.run(
                    [
                        "ffmpeg", "-y", "-loglevel", "error",
                        "-ss", "1",
                        "-i", video_path,
                        "-frames:v", "1",
                        "-q:v", "3",
                        cover_tmp,
                    ],
                    check=True,
                    timeout=30,
                )
            except (subprocess.CalledProcessError, subprocess.TimeoutExpired,
                    FileNotFoundError) as e:
                logger.warning("ffmpeg cover extract failed: %s", e)
                return ""
            if not os.path.exists(cover_tmp) or os.path.getsize(cover_tmp) == 0:
                return ""

            # 算 cover sha
            cover_sha = hashlib.sha256()
            with open(cover_tmp, "rb") as f:
                for chunk in iter(lambda: f.read(64 * 1024), b""):
                    cover_sha.update(chunk)
            sha = cover_sha.hexdigest()
            key = cas_key("derivatives", sha, "jpg")
            self._s3.upload_file(
                Filename=cover_tmp,
                Bucket=self._bucket_derivatives,
                Key=key,
                ExtraArgs={"ContentType": "image/jpeg"},
            )
            return sha
        finally:
            try:
                os.unlink(cover_tmp)
            except FileNotFoundError:
                pass


# ─── Probes (delay-imported; 单测可 monkeypatch) ──────


def _probe_image(path: str) -> tuple[int, int]:
    try:
        from PIL import Image  # type: ignore
    except ImportError:
        return 0, 0
    try:
        with Image.open(path) as im:
            return im.size  # (width, height)
    except Exception as e:
        logger.warning("PIL probe failed: %s", e)
        return 0, 0


def _compute_blurhash(path: str, components_x: int = 4, components_y: int = 3) -> str:
    """计算图片 BlurHash 字符串. 失败时返空串 (不阻塞主流程).

    BlurHash 标准 4x3 components 在 1KB 文件大小左右编码; 客户端解码后
    在 cas:sha 真图加载前作占位 (类似 LQIP). 客户端 creation_card.dart
    已支持显示.

    缩放到 32x32 后再算: blurhash 算法是 O(w*h*x*y), 直接喂 1024x1024 太慢.
    Lanczos 缩放保留低频信息, 不影响 BlurHash 视觉质量.
    """
    try:
        from PIL import Image  # type: ignore
        import blurhash  # type: ignore
    except ImportError:
        return ""
    try:
        with Image.open(path) as im:
            im = im.convert("RGB")
            im.thumbnail((32, 32), Image.Resampling.LANCZOS)
            return blurhash.encode(im, x_components=components_x, y_components=components_y)
    except Exception as e:
        logger.warning("blurhash compute failed: %s", e)
        return ""


def _ffprobe_video(path: str) -> tuple[int, int, int]:
    """返回 (width, height, duration_ms). 失败时全 0."""
    try:
        out = subprocess.run(
            [
                "ffprobe", "-v", "error",
                "-select_streams", "v:0",
                "-show_entries", "stream=width,height:format=duration",
                "-of", "csv=p=0:s=,",
                path,
            ],
            check=True, capture_output=True, text=True, timeout=15,
        )
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired,
            FileNotFoundError) as e:
        logger.warning("ffprobe failed: %s", e)
        return 0, 0, 0
    parts = [p for p in out.stdout.strip().splitlines() if p]
    if not parts:
        return 0, 0, 0
    # 形如: "1920,1080" "30.500000" (两行 stream + format)
    try:
        wh = parts[0].split(",")
        w = int(wh[0]) if len(wh) >= 1 and wh[0] else 0
        h = int(wh[1]) if len(wh) >= 2 and wh[1] else 0
        dur_s = float(parts[1]) if len(parts) >= 2 else 0
        return w, h, int(dur_s * 1000)
    except (ValueError, IndexError) as e:
        logger.warning("ffprobe parse failed: %s out=%r", e, out.stdout)
        return 0, 0, 0
