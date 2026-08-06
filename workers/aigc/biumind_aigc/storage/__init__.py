"""Storage subpackage — 把厂商返回的临时 URL 转存到 MinIO + 派生缩略图.

依赖 (走 [storage] / [all] extra 才装上):
  - boto3              S3/MinIO 客户端
  - Pillow (PIL)       图片元信息 + 缩略图 (P3-5 简化: 不做缩略图, 留 v2)
  - ffmpeg-python      视频探测 + cover 抽帧
  - blurhash-python    BlurHash 占位 (v2 启用)

P3-5 核心: 转存 + sha256 + ffprobe 视频元信息 + ffmpeg 抽 cover.
缩略图 / BlurHash 留 v2 (Phase 1+ Storage Design §5).
"""

from .persist import (
    PersistError,
    PersistedOutput,
    Persister,
    cas_key,
)

__all__ = ["PersistError", "PersistedOutput", "Persister", "cas_key"]
