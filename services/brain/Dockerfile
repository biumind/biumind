ARG INFRA_REGISTRY=docker.io
FROM ${INFRA_REGISTRY}/library/golang:1.25-alpine AS builder
WORKDIR /workspace
ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=off
ENV GOFLAGS=-mod=mod
COPY packages/go-sdk/biu /workspace/packages/go-sdk/biu
COPY apps/cli/biu /workspace/apps/cli/biu
COPY services/brain /workspace/services/brain
WORKDIR /workspace/services/brain
RUN sed -i 's|dl-cdn.alpinelinux.org|mirrors.aliyun.com|g' /etc/apk/repositories && \
    apk add --no-cache git ca-certificates && \
    go mod download && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath \
        -ldflags="-s -w" -o /out/brain ./cmd/brain

FROM ${INFRA_REGISTRY}/library/alpine:3.20
COPY --from=builder /out/brain /usr/local/bin/brain
# migrations 路径跟 BIUMIND_MIGRATIONS_DIR 默认值匹配 (dbmigrate.Run 自动应用)
COPY --from=builder /workspace/services/brain/migrations /etc/biumind/migrations/brain
RUN sed -i 's|dl-cdn.alpinelinux.org|mirrors.aliyun.com|g' /etc/apk/repositories && \
    adduser -D -u 65532 nonroot && \
    apk add --no-cache ca-certificates wget
USER nonroot
EXPOSE 7003
ENTRYPOINT ["/usr/local/bin/brain"]
