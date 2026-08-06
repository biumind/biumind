#!/bin/sh
# 从容器自身 /etc/resolv.conf 提取首个 nameserver, 生成 nginx resolver 配置。
#
# 为什么: nginx.conf 用 `set $upstream` 变量 + proxy_pass 变量, 需要 `resolver`
# 指令做运行时 DNS 解析。而 resolver IP 因运行时而异:
#   - Docker: embedded DNS 127.0.0.11
#   - podman (aardvark-dns): 网络网关 IP (如 10.89.0.1)
# 硬编码 127.0.0.11 在 podman 下 DNS 超时, 所有 /v1/* 反代挂。这里读容器实际
# resolv.conf, 两种运行时都自适应。
#
# 由 nginx:alpine 官方 entrypoint (/docker-entrypoint.sh) 在启动 nginx 前自动
# 执行 /docker-entrypoint.d/*.sh。写到 /tmp (容器 read_only 时 tmpfs 可写)。
set -eu

NS="$(awk '/^nameserver/ { print $2; exit }' /etc/resolv.conf 2>/dev/null || true)"
: "${NS:=127.0.0.11}"   # 兜底 Docker 默认

echo "resolver ${NS} valid=30s ipv6=off;" > /tmp/biumind-resolver.conf
echo "05-resolver.sh: nginx resolver -> ${NS}"
