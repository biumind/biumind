ARG INFRA_REGISTRY=docker.io
# Multi-stage build for biumind-aigc-worker.
#
# 基础镜像不带 ffmpeg / Pillow 这些重依赖 — 只装 storage extra 时才需要 (P3-5+).
# DashScope / VolcEngine 都走 HTTP REST, 不需要厂商 SDK.

FROM ${INFRA_REGISTRY}/library/python:3.12-slim AS builder
ENV PIP_INDEX_URL=https://mirrors.aliyun.com/pypi/simple/ \
    PIP_TRUSTED_HOST=mirrors.aliyun.com \
    PIP_DISABLE_PIP_VERSION_CHECK=1
WORKDIR /build
COPY pyproject.toml README.md ./
COPY biumind_aigc/ biumind_aigc/
# 默认装 [all] (含 storage 依赖). 不需要时可改成 base "."
RUN pip install --no-cache-dir --prefix=/install ".[all]"

FROM ${INFRA_REGISTRY}/library/python:3.12-slim
RUN sed -i 's|deb.debian.org|mirrors.aliyun.com|g; s|security.debian.org|mirrors.aliyun.com|g' \
        /etc/apt/sources.list.d/debian.sources && \
    apt-get update && apt-get install -y --no-install-recommends \
        ffmpeg \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /install /usr/local
WORKDIR /app
COPY biumind_aigc/ /app/biumind_aigc/

RUN useradd -r -u 10002 aigc
USER aigc

ENTRYPOINT ["python", "-m", "biumind_aigc"]
