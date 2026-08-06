"""Storage persist 测试 — mime sniff / cas_key 纯函数 + 主流程用 fake S3."""

from __future__ import annotations

import hashlib
import io
import json
import os
import tempfile
from unittest.mock import MagicMock

import httpx
import pytest
import respx

from biumind_aigc.storage.persist import (
    ALLOWED_MIMES,
    PersistError,
    PersistedOutput,
    _compute_blurhash,
    cas_key,
    sniff_mime,
)


# ─── 纯函数 ───────────────────────────────────────────


def test_cas_key_layout() -> None:
    sha = "abcdef0123456789" + "0" * 48
    assert cas_key("outputs", sha, "png") == f"outputs/ab/cd/{sha}.png"
    assert cas_key("derivatives", sha, "jpg") == f"derivatives/ab/cd/{sha}.jpg"


# ─── BlurHash (v2-2) ───────────────────────────────


def _write_solid_png(path: str, color: tuple[int, int, int] = (200, 100, 50),
                     size: tuple[int, int] = (64, 64)) -> None:
    from PIL import Image  # type: ignore
    Image.new("RGB", size, color).save(path, format="PNG")


def test_compute_blurhash_returns_valid_string() -> None:
    """blurhash 标准: 6+ 字符 base83 串. 4x3 components → 28 字符."""
    with tempfile.NamedTemporaryFile(suffix=".png", delete=False) as f:
        _write_solid_png(f.name)
        path = f.name
    try:
        h = _compute_blurhash(path)
        assert len(h) >= 6, f"blurhash too short: {h!r}"
        # 4x3 components 长度 = 4 + 2*1 + 2*4*3 + 2 = 28 (大致, base83)
        # 不强校验固定长度, 只确保非空字符串.
        assert isinstance(h, str)
    finally:
        os.unlink(path)


def test_compute_blurhash_returns_empty_on_missing_file() -> None:
    """文件不存在 / 损坏 → 返空串, 不抛."""
    h = _compute_blurhash("/tmp/does-not-exist-blurhash.png")
    assert h == ""


def test_compute_blurhash_diff_colors_diff_hashes() -> None:
    """不同图片应得不同 blurhash."""
    with tempfile.NamedTemporaryFile(suffix=".png", delete=False) as f1:
        _write_solid_png(f1.name, color=(255, 0, 0))
        red_path = f1.name
    with tempfile.NamedTemporaryFile(suffix=".png", delete=False) as f2:
        _write_solid_png(f2.name, color=(0, 0, 255))
        blue_path = f2.name
    try:
        h_red = _compute_blurhash(red_path)
        h_blue = _compute_blurhash(blue_path)
        assert h_red and h_blue
        assert h_red != h_blue, "纯红 / 纯蓝 blurhash 应不同"
    finally:
        os.unlink(red_path)
        os.unlink(blue_path)


def test_sniff_mime_png() -> None:
    assert sniff_mime(b"\x89PNG\r\n\x1a\nIHDR....") == "image/png"


def test_sniff_mime_jpeg() -> None:
    assert sniff_mime(b"\xff\xd8\xff\xe0\x00\x10JFIF") == "image/jpeg"


def test_sniff_mime_webp() -> None:
    assert sniff_mime(b"RIFF\x00\x00\x00\x00WEBPVP8 ") == "image/webp"


def test_sniff_mime_webp_riff_but_not_webp() -> None:
    """RIFF 头但不是 WEBP (如 wav) 应该返 None."""
    assert sniff_mime(b"RIFF\x00\x00\x00\x00WAVEfmt ") is None


def test_sniff_mime_mp4() -> None:
    assert sniff_mime(b"\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00") == "video/mp4"


def test_sniff_mime_avif() -> None:
    assert sniff_mime(b"\x00\x00\x00\x18ftypavif\x00\x00\x00\x00") == "image/avif"


def test_sniff_mime_webm() -> None:
    assert sniff_mime(b"\x1aE\xdf\xa3\x9fB\x86\x81\x01") == "video/webm"


def test_sniff_mime_unknown() -> None:
    assert sniff_mime(b"BOGUS_HEADER") is None
    assert sniff_mime(b"") is None


def test_allowed_mimes_table() -> None:
    """白名单表完整性 (避免漏写后被 sniff 出来却拒绝)."""
    for mime in ["image/jpeg", "image/png", "image/webp", "image/gif",
                 "video/mp4", "video/webm", "image/avif"]:
        assert mime in ALLOWED_MIMES


# ─── Persister 主流程 (mock boto3 + httpx) ────────────


class _FakeS3:
    """记录 upload_file 调用; 不真上传."""

    def __init__(self) -> None:
        self.uploads: list[dict] = []

    def upload_file(self, **kwargs) -> None:  # boto3 形式
        self.uploads.append(kwargs)


@pytest.fixture
def persister(monkeypatch):
    """构造 Persister, 把 boto3 client / ffprobe / PIL 全 mock 掉."""
    from biumind_aigc.storage import persist as p

    fake_s3 = _FakeS3()

    # patch boto3.client → fake
    fake_boto3 = MagicMock()
    fake_boto3.client = MagicMock(return_value=fake_s3)
    monkeypatch.setitem(__import__("sys").modules, "boto3", fake_boto3)

    inst = p.Persister(
        s3_endpoint="http://minio:9000",
        s3_access_key="test", s3_secret_key="test",
        s3_region="us-east-1",
        bucket_outputs="biumind-aigc-outputs",
        bucket_derivatives="biumind-aigc-derivatives",
    )
    inst._s3_fake = fake_s3  # 测试访问

    # 不依赖真 PIL / ffmpeg
    monkeypatch.setattr(p, "_probe_image", lambda path: (1024, 1024))
    monkeypatch.setattr(p, "_ffprobe_video", lambda path: (1280, 720, 5000))

    return inst


@respx.mock
async def test_persist_url_image_happy(persister) -> None:
    # 一张最小 PNG (8 字节 header 足够 sniff)
    body = b"\x89PNG\r\n\x1a\n" + b"\x00" * 256
    respx.get("https://upstream.local/img.png").mock(
        return_value=httpx.Response(200, content=body),
    )

    po = await persister.persist_url("https://upstream.local/img.png", kind="image")
    assert isinstance(po, PersistedOutput)
    assert po.mime_type == "image/png"
    assert po.file_size == len(body)
    assert po.storage_url.startswith("cas:")
    assert po.storage_key.startswith("outputs/")
    assert po.storage_key.endswith(".png")
    assert po.width == 1024 and po.height == 1024
    assert po.duration_ms == 0
    # sha 一致性
    expected = hashlib.sha256(body).hexdigest()
    assert po.sha256 == expected
    assert po.storage_url == f"cas:{expected}"

    # 上传被调一次
    assert len(persister._s3_fake.uploads) == 1
    up = persister._s3_fake.uploads[0]
    assert up["Bucket"] == "biumind-aigc-outputs"
    assert up["ExtraArgs"]["ContentType"] == "image/png"
    await persister.aclose()


@respx.mock
async def test_persist_url_unsupported_mime(persister) -> None:
    respx.get("https://upstream.local/x.bin").mock(
        return_value=httpx.Response(200, content=b"BOGUS_HEADER" + b"\x00" * 32),
    )
    with pytest.raises(PersistError):
        await persister.persist_url("https://upstream.local/x.bin", kind="image")
    await persister.aclose()


@respx.mock
async def test_persist_url_404(persister) -> None:
    respx.get("https://upstream.local/missing").mock(
        return_value=httpx.Response(404, text="not found"),
    )
    with pytest.raises(PersistError):
        await persister.persist_url("https://upstream.local/missing", kind="image")
    await persister.aclose()


@respx.mock
async def test_persist_url_empty_body(persister) -> None:
    respx.get("https://upstream.local/empty").mock(
        return_value=httpx.Response(200, content=b""),
    )
    with pytest.raises(PersistError):
        await persister.persist_url("https://upstream.local/empty", kind="image")
    await persister.aclose()


@respx.mock
async def test_persist_url_video_with_cover(persister, monkeypatch) -> None:
    """视频转存时尝试抽 cover (mock subprocess.run 让它不真跑 ffmpeg)."""
    body = b"\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00" + b"\xab" * 1024
    respx.get("https://upstream.local/v.mp4").mock(
        return_value=httpx.Response(200, content=body),
    )

    # subprocess.run mock — 不真跑 ffmpeg, 但要在 cover_tmp 写一些字节让 hash 算得出
    from biumind_aigc.storage import persist as p
    real_subprocess_run = p.subprocess.run

    def fake_run(cmd, **kw):
        # 取 cmd 最后一项当 output path
        cover_path = cmd[-1]
        with open(cover_path, "wb") as f:
            f.write(b"FAKE_COVER_JPEG_BYTES")
        return MagicMock(returncode=0)

    monkeypatch.setattr(p.subprocess, "run", fake_run)

    po = await persister.persist_url("https://upstream.local/v.mp4", kind="video")
    assert po.mime_type == "video/mp4"
    assert po.duration_ms == 5000
    # cover 应该被算 + 上传到 derivatives
    assert po.cover_sha != ""
    expected_cover = hashlib.sha256(b"FAKE_COVER_JPEG_BYTES").hexdigest()
    assert po.cover_sha == expected_cover

    # 应有两次 upload: outputs + derivatives
    buckets = [u["Bucket"] for u in persister._s3_fake.uploads]
    assert "biumind-aigc-outputs" in buckets
    assert "biumind-aigc-derivatives" in buckets
    await persister.aclose()
