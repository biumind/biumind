ARG INFRA_REGISTRY=docker.io
# go.mod 要求 go 1.26 — builder 用 golang:1.26-alpine（其余服务多为 1.25）。
FROM ${INFRA_REGISTRY}/library/golang:1.26-alpine AS builder
WORKDIR /workspace
ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=off
ENV GOFLAGS=-mod=mod
COPY packages/go-sdk/biu /workspace/packages/go-sdk/biu
COPY services/sandbox /workspace/services/sandbox
WORKDIR /workspace/services/sandbox
RUN sed -i 's|dl-cdn.alpinelinux.org|mirrors.aliyun.com|g' /etc/apk/repositories && \
    apk add --no-cache git ca-certificates && \
    go mod download && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath \
        -ldflags="-s -w" -o /out/sandbox ./cmd/sandbox

FROM ${INFRA_REGISTRY}/library/alpine:3.20
COPY --from=builder /out/sandbox /usr/local/bin/sandbox
RUN sed -i 's|dl-cdn.alpinelinux.org|mirrors.aliyun.com|g' /etc/apk/repositories && \
    adduser -D -u 65532 nonroot && \
    apk add --no-cache ca-certificates wget
USER nonroot
EXPOSE 7005
ENTRYPOINT ["/usr/local/bin/sandbox"]
