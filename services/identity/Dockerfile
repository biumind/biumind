ARG INFRA_REGISTRY=docker.io
FROM ${INFRA_REGISTRY}/library/golang:1.25-alpine AS builder
WORKDIR /workspace
ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=off
ENV GOFLAGS=-mod=mod
COPY packages/go-sdk/biu /workspace/packages/go-sdk/biu
COPY services/identity /workspace/services/identity
WORKDIR /workspace/services/identity
RUN sed -i 's|dl-cdn.alpinelinux.org|mirrors.aliyun.com|g' /etc/apk/repositories && \
    apk add --no-cache git ca-certificates && \
    go mod download && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath \
        -ldflags="-s -w" -o /out/identity ./cmd/identity

FROM ${INFRA_REGISTRY}/library/alpine:3.20
COPY --from=builder /out/identity /usr/local/bin/identity
COPY --from=builder /workspace/services/identity/migrations /etc/biumind/migrations/identity
RUN sed -i 's|dl-cdn.alpinelinux.org|mirrors.aliyun.com|g' /etc/apk/repositories && \
    adduser -D -u 65532 nonroot && \
    apk add --no-cache ca-certificates wget
# JWT 签名密钥目录预建并 chown 给 nonroot — named volume 首建继承镜像内属主（DESIGN.md §9）
RUN mkdir -p /var/lib/biumind/identity && \
    chown -R 65532:65532 /var/lib/biumind/identity && \
    chmod 0700 /var/lib/biumind/identity
USER nonroot
EXPOSE 7004
ENTRYPOINT ["/usr/local/bin/identity"]
