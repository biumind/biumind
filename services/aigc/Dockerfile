ARG INFRA_REGISTRY=docker.io
# Multi-stage build: small final image
FROM ${INFRA_REGISTRY}/library/golang:1.25-alpine AS builder
WORKDIR /workspace
ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=off
ENV GOFLAGS=-mod=mod

# Copy SDK first (for caching)
COPY packages/go-sdk/biu /workspace/packages/go-sdk/biu

# Then service
COPY services/aigc /workspace/services/aigc

WORKDIR /workspace/services/aigc
RUN sed -i 's|dl-cdn.alpinelinux.org|mirrors.aliyun.com|g' /etc/apk/repositories && \
    apk add --no-cache git ca-certificates && \
    go mod download && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath \
        -ldflags="-s -w -X main.serviceVersion=$(git -C /workspace describe --always 2>/dev/null || echo dev)" \
        -o /out/aigc ./cmd/aigc

FROM ${INFRA_REGISTRY}/library/alpine:3.20
COPY --from=builder /out/aigc /usr/local/bin/aigc
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
# Migrations — when AIGC_DATABASE_URL is set, the service runs goose against
# this dir at startup. Path matches AIGC_MIGRATIONS_DIR default.
COPY services/aigc/migrations /etc/biumind/migrations/aigc
RUN sed -i 's|dl-cdn.alpinelinux.org|mirrors.aliyun.com|g' /etc/apk/repositories && \
    adduser -D -u 65532 nonroot && \
    apk add --no-cache ca-certificates wget
USER nonroot
EXPOSE 7011
ENTRYPOINT ["/usr/local/bin/aigc"]
