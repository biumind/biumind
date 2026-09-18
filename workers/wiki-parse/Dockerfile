ARG INFRA_REGISTRY=docker.io
# Lean image — wiki-parse worker。解析库全走 wheel（pypdf/mammoth/openpyxl
# 纯 Python；python-pptx 依赖的 lxml/Pillow 有预编译 wheel），slim base 够用。
# 依赖声明在 pyproject.toml，`pip install .` 自动带上。
# 多阶段对齐 wiki-llm 形态，build cache 不泄进 runtime。

FROM ${INFRA_REGISTRY}/library/python:3.12-slim AS builder
ENV PIP_INDEX_URL=https://mirrors.aliyun.com/pypi/simple/ \
    PIP_TRUSTED_HOST=mirrors.aliyun.com \
    PIP_DISABLE_PIP_VERSION_CHECK=1
WORKDIR /build
COPY pyproject.toml README.md ./
COPY wiki_parse/ wiki_parse/
RUN pip install --no-cache-dir --prefix=/install .

FROM ${INFRA_REGISTRY}/library/python:3.12-slim
# python:3.12-slim 自带 perl 系包带 CVE（内嵌 Archive::Tar 模块 CVE-2026-42496 /
# perl CVE-2026-8376），CI Trivy 门禁会拦——升级到修复版（同 site 镜像
# openssl CVE-2026-31789 的处理先例）。
RUN sed -i 's|deb.debian.org|mirrors.aliyun.com|g; s|security.debian.org|mirrors.aliyun.com|g' \
        /etc/apt/sources.list.d/debian.sources && \
    apt-get update && apt-get upgrade -y --no-install-recommends \
        perl-base \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /install /usr/local
WORKDIR /app
COPY wiki_parse/ /app/wiki_parse/

RUN useradd -r -u 10003 wikiparse
USER wikiparse

ENV PYTHONUNBUFFERED=1 \
    BIUMIND_LOG_LEVEL=INFO

ENTRYPOINT ["python", "-m", "wiki_parse"]
