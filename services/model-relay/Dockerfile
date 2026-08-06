ARG INFRA_REGISTRY=docker.io
# Multi-stage build: small distroless final image
FROM ${INFRA_REGISTRY}/library/golang:1.25-alpine AS builder
WORKDIR /workspace
ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=off
ENV GOFLAGS=-mod=mod

# Copy SDK first (for caching)
COPY packages/go-sdk/biu /workspace/packages/go-sdk/biu

# Then service
COPY services/model-relay /workspace/services/model-relay

WORKDIR /workspace/services/model-relay
RUN sed -i 's|dl-cdn.alpinelinux.org|mirrors.aliyun.com|g' /etc/apk/repositories && \
    apk add --no-cache git ca-certificates && \
    go mod download && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath \
        -ldflags="-s -w -X main.serviceVersion=$(git -C /workspace describe --always 2>/dev/null || echo dev)" \
        -o /out/model-relay ./cmd/model-relay

FROM ${INFRA_REGISTRY}/library/alpine:3.20
COPY --from=builder /out/model-relay /usr/local/bin/model-relay
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
# Migrations — when MODEL_RELAY_ADMIN_DATABASE_URL is set, the service
# runs goose against this dir at startup (see main.go startAdminStack).
# Path matches MODEL_RELAY_ADMIN_MIGRATIONS_DIR default in compose.
COPY services/model-relay/migrations /etc/biumind/migrations/model-relay
RUN sed -i 's|dl-cdn.alpinelinux.org|mirrors.aliyun.com|g' /etc/apk/repositories && \
    adduser -D -u 65532 nonroot && \
    apk add --no-cache ca-certificates wget
USER nonroot
EXPOSE 7001
ENTRYPOINT ["/usr/local/bin/model-relay"]
