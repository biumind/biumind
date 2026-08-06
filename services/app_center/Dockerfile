ARG INFRA_REGISTRY=docker.io
FROM ${INFRA_REGISTRY}/library/golang:1.25-alpine AS builder
WORKDIR /workspace
ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=off
ENV GOFLAGS=-mod=mod
COPY packages/go-sdk/biu /workspace/packages/go-sdk/biu
COPY services/app_center /workspace/services/app_center
WORKDIR /workspace/services/app_center
RUN sed -i 's|dl-cdn.alpinelinux.org|mirrors.aliyun.com|g' /etc/apk/repositories && \
    apk add --no-cache git ca-certificates && \
    go mod download && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath \
        -ldflags="-s -w" -o /out/app_center ./cmd/app_center

FROM ${INFRA_REGISTRY}/library/alpine:3.20
COPY --from=builder /out/app_center /usr/local/bin/app_center
# migrations 路径跟 BIUMIND_MIGRATIONS_DIR 默认值匹配 (dbmigrate.Run 自动应用)
COPY --from=builder /workspace/services/app_center/migrations /etc/biumind/migrations/app_center
RUN sed -i 's|dl-cdn.alpinelinux.org|mirrors.aliyun.com|g' /etc/apk/repositories && \
    adduser -D -u 65532 nonroot && \
    apk add --no-cache ca-certificates wget
USER nonroot
EXPOSE 7008
ENTRYPOINT ["/usr/local/bin/app_center"]
