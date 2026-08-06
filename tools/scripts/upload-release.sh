#!/usr/bin/env bash
# upload-release.sh — 上传客户端发版产物到 S3 兼容对象存储 (阿里云 OSS / MinIO)。
#
# 用法:
#   upload-release.sh <endpoint> <access-key> <secret-key> <bucket> <version> <artifacts-dir>
#
#   endpoint      S3 兼容 endpoint。
#                 阿里云 OSS: https://oss-cn-beijing.aliyuncs.com (region endpoint, 不含 bucket)
#                 本地 MinIO: http://minio:9000
#   access-key    access key (阿里云 RAM AK / MinIO root user)
#   secret-key    secret key
#   bucket        bucket 名 (生产 biumind-releases; 本地 dev releases)
#   version       版本号 (如 0.1.0,无 v 前缀)
#   artifacts-dir 产物目录 (如 apps/client),含 biumind-<version>* / releases.json / asset-*.json
#
# 上传布局 (产物 url 在 releases.json 里是 /downloads/<filename> 根路径稳定 url):
#   <bucket>/<filename>               产物放根 (稳定 url, 每次新版覆盖)
#   <bucket>/<version>/<filename>     版本子目录历史归档
#   <bucket>/releases.json            清单放根 (官网 /downloads/releases.json 直取)
#
# mc 用 podman/docker 容器跑 (CI agent 不一定装 mc)。podman 优先 (本机用)。
# 阿里云 OSS bucket 在控制台预建 + 设公共读 (mc anonymous set 对 OSS 权限模型不适用)。
#
# 本地联调 (dev MinIO, bucket=releases):
#   MC_NETWORK=biumind_biu-net tools/scripts/upload-release.sh \
#     http://minio:9000 biumind biumind_minio_dev releases 0.1.0 apps/client
#
# 生产 (阿里云 OSS, bucket=biumind-releases, Jenkins 调用):
#   tools/scripts/upload-release.sh \
#     https://oss-cn-beijing.aliyuncs.com <AK> <SK> biumind-releases 0.1.0 apps/client
#
set -euo pipefail

if [ "$#" -ne 6 ]; then
  echo "用法: $0 <endpoint> <access-key> <secret-key> <bucket> <version> <artifacts-dir>" >&2
  exit 1
fi

EP="$1"; AK="$2"; SK="$3"; BUCKET="$4"; VER="$5"; DIR="$6"

if [ ! -d "$DIR" ]; then
  echo "错误: artifacts-dir 不存在: $DIR" >&2
  exit 1
fi

# podman 优先 (本机/CI 可能用 podman), 否则 docker
CONTAINER="$(command -v podman || command -v docker || true)"
if [ -z "$CONTAINER" ]; then
  echo "错误: 需要 podman 或 docker (跑 mc 镜像)" >&2
  exit 1
fi

# 挂载 artifacts-dir 进 mc 容器。用绝对路径 (podman/docker 要求)。
DIR_ABS="$(cd "$DIR" && pwd)"

# 可选: 容器网络。本地 dev 测试时 MC 容器要加入 biumind_biu-net 才能解析
# minio hostname (设 MC_NETWORK=biumind_biu-net)。生产访问公网 endpoint 不需。
NET_OPT=""
if [ -n "${MC_NETWORK:-}" ]; then
  NET_OPT="--network ${MC_NETWORK}"
fi

# 内层 -c 命令: $EP/$AK/$SK/$BUCKET/$VER 由外层 bash 展开后传入 (容器内是字面值);
# 容器内 shell 变量 ($f) 用 \$ 转义保留给容器内展开。
"$CONTAINER" run --rm \
  $NET_OPT \
  -v "$DIR_ABS":/work:ro \
  --entrypoint /bin/sh \
  "${MC_IMAGE:-docker.io/minio/mc:latest}" \
  -c "
    set -eu
    mc alias set prod ${EP} ${AK} ${SK}
    # MinIO: 自动建 bucket + 公共读。阿里云 OSS: bucket 控制台预建,
    # mc mb --ignore-existing 无害; anonymous set 对 OSS 权限模型不适用 (|| true)。
    mc mb --ignore-existing prod/${BUCKET} || true
    mc anonymous set download prod/${BUCKET} || true
    cd /work
    # 上传所有产物 + asset 元信息。
    # 产物 url 在 releases.json 里是根路径 <origin>/<filename> (稳定 url),
    # 所以产物必须放 bucket 根 (每次新版覆盖, url 永远指向最新)。
    # 版本子目录 <version>/ 额外归档历史版本 (旧版不丢)。
    for f in biumind-${VER}* asset-*.json; do
      [ -e \"\$f\" ] || continue
      mc cp \"\$f\" \"prod/${BUCKET}/\"                # 根: 稳定 url 对应
      mc cp \"\$f\" \"prod/${BUCKET}/${VER}/\"          # 版本目录: 历史归档
    done
    # releases.json 额外放 bucket 根 (官网 /downloads/releases.json 经 nginx 反代 OSS 直取)
    if [ -f releases.json ]; then
      mc cp releases.json prod/${BUCKET}/releases.json
    fi
    echo '✓ uploaded to prod/${BUCKET}/ root (+ ${VER}/ archive + releases.json)'
  "
