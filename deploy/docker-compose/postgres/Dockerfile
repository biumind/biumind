ARG INFRA_REGISTRY=docker.io
# pgvector + zhparser（中文分词）+ pgcrypto + pg_trgm
# Base: pgvector/pgvector 已含 pgvector
FROM ${INFRA_REGISTRY}/pgvector/pgvector:pg16

USER root

# 安装 zhparser（中文分词）和编译依赖
RUN apt-get update && apt-get install -y --no-install-recommends \
        build-essential \
        postgresql-server-dev-16 \
        libscws1-dev \
        scws \
        git \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# 编译 zhparser
RUN git clone --depth=1 https://github.com/amutu/zhparser.git /tmp/zhparser \
    && cd /tmp/zhparser \
    && SCWS_HOME=/usr make \
    && make install \
    && rm -rf /tmp/zhparser

# 清理编译工具，缩小镜像
RUN apt-get purge -y build-essential postgresql-server-dev-16 git \
    && apt-get autoremove -y \
    && rm -rf /var/lib/apt/lists/*

USER postgres
