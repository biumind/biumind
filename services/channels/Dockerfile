ARG INFRA_REGISTRY=docker.io
FROM ${INFRA_REGISTRY}/library/golang:1.25-alpine AS builder
WORKDIR /workspace
ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=off
ENV GOFLAGS=-mod=mod
COPY packages/go-sdk/biu /workspace/packages/go-sdk/biu
COPY services/channels /workspace/services/channels
WORKDIR /workspace/services/channels
RUN sed -i 's|dl-cdn.alpinelinux.org|mirrors.aliyun.com|g' /etc/apk/repositories && \
    apk add --no-cache git ca-certificates && \
    go mod download && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath \
        -ldflags="-s -w" -o /out/channels ./cmd/channels

FROM ${INFRA_REGISTRY}/library/alpine:3.20
COPY --from=builder /out/channels /usr/local/bin/channels
RUN sed -i 's|dl-cdn.alpinelinux.org|mirrors.aliyun.com|g' /etc/apk/repositories && \
    adduser -D -u 65532 nonroot && \
    apk add --no-cache ca-certificates wget
USER nonroot
EXPOSE 7007
ENTRYPOINT ["/usr/local/bin/channels"]
