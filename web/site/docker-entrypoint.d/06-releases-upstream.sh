#!/bin/sh
# 生成 nginx location 片段, 反代 releases.json 到对象存储 (dev MinIO / prod 阿里云 OSS)。
#
# 为什么: releases.json 经 site nginx 提供 (客户端/官网单 origin 拉 <origin>/downloads/
# releases.json, 不碰 OSS 域名)。但来源因环境而异:
#   - dev:  本地 MinIO http://minio:9000/releases (bucket=releases)
#   - prod: CNAME 自定义域名 https://releases.your-biumind.example.com (DNS CNAME 到
#     biumind-releases.oss-cn-beijing.aliyuncs.com, 公共读)。必须用 CNAME: 阿里云对
#     APK 经 OSS 默认 endpoint 公网下载返 ApkDownloadForbidden, CNAME 绕过。
# 同一份 nginx.conf 无法写死, 由 env RELEASES_UPSTREAM 注入, 这里展开成 location 片段。
#
# 产物大文件 (dmg/apk/...) 不经 nginx, releases.json 里 url 已是 OSS 直链。
# 这里只反代 releases.json 这一个几 KB 的小文件。
#
# RELEASES_UPSTREAM 值:
#   - 不含 bucket path (dev MinIO) → http://minio:9000/releases (bucket=releases 在 path)
#   - CNAME 自定义域名 → https://releases.your-biumind.example.com (prod, https 分支)
# 两种 + /releases.json 都拼出正确的 object url。
#
# 由 nginx:alpine 官方 entrypoint 在启动 nginx 前自动执行。
set -eu

# dev 默认: 本地 MinIO (compose 注入 RELEASES_UPSTREAM=http://minio:9000/releases)
RU="${RELEASES_UPSTREAM:-http://minio:9000/releases}"

# 判断是否 HTTPS (OSS) — 决定要不要 SNI + 公网 resolver
case "$RU" in
  https://*)
    # OSS: 公网域名, 需要 proxy_ssl_server_name (SNI) + resolver 解析公网。
    # resolver 复用 05-resolver.sh 提取的 (容器 DNS 能解析公网)。
    cat > /tmp/biumind-releases.conf <<EOF
# releases.json ← 阿里云 OSS (prod, 由 RELEASES_UPSTREAM 注入)。只反代此小文件;
# 产物大文件走 releases.json 里的 OSS 直链 url, 不经 nginx。
location = /downloads/releases.json {
    include /tmp/biumind-resolver.conf;
    proxy_ssl_server_name on;
    proxy_ssl_protocols TLSv1.2 TLSv1.3;
    proxy_pass ${RU}/releases.json;
    proxy_set_header Host \$proxy_host;
    proxy_set_header X-Real-IP \$remote_addr;
    proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto \$scheme;
    proxy_buffering off;
    proxy_read_timeout 30s;
}
EOF
    ;;
  *)
    # dev MinIO: 内网 HTTP, Host 用 $host (path-style 访问)
    cat > /tmp/biumind-releases.conf <<EOF
# releases.json ← 本地 MinIO (dev, RELEASES_UPSTREAM=${RU})。只反代此小文件。
location = /downloads/releases.json {
    set \$upstream "${RU}";
    proxy_pass \$upstream/releases.json;
    proxy_set_header Host \$host;
    proxy_set_header X-Real-IP \$remote_addr;
    proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto \$scheme;
    proxy_buffering off;
    proxy_read_timeout 30s;
}
EOF
    ;;
esac

echo "06-releases-upstream.sh: releases.json source -> ${RU}"
