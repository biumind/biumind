ARG INFRA_REGISTRY=docker.io
# Multi-stage build keeping the runtime image free of build toolchain.
#
# Tesseract is the only system dep that's truly required even for the
# slim build — it's a few MB and the image extractor needs it. Whisper
# pulls down ffmpeg on first use; faster-whisper itself is wheel-installed.

FROM ${INFRA_REGISTRY}/library/python:3.12-slim AS builder
ENV PIP_INDEX_URL=https://mirrors.aliyun.com/pypi/simple/ \
    PIP_TRUSTED_HOST=mirrors.aliyun.com \
    PIP_DISABLE_PIP_VERSION_CHECK=1
WORKDIR /build
COPY pyproject.toml README.md ./
COPY biumind_ingest/ biumind_ingest/
RUN pip install --no-cache-dir --prefix=/install ".[all]"

FROM ${INFRA_REGISTRY}/library/python:3.12-slim
RUN sed -i 's|deb.debian.org|mirrors.aliyun.com|g; s|security.debian.org|mirrors.aliyun.com|g' \
        /etc/apt/sources.list.d/debian.sources && \
    apt-get update && apt-get install -y --no-install-recommends \
        tesseract-ocr tesseract-ocr-chi-sim ffmpeg \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /install /usr/local
WORKDIR /app
COPY biumind_ingest/ /app/biumind_ingest/

# Drop privileges. tmpfs mounts are caller's responsibility (k8s
# securityContext.readOnlyRootFilesystem + emptyDir on /tmp).
RUN useradd -r -u 10001 ingest
USER ingest

ENV PYTHONUNBUFFERED=1 \
    BIUMIND_LOG_LEVEL=INFO

ENTRYPOINT ["python", "-m", "biumind_ingest"]
