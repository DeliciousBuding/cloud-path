# syntax=docker/dockerfile:1.7
# 多阶段构建：pnpm build -> go build -tags embed_ui -> 非 root Alpine 运行镜像。
# 构建出来的镜像同时包含 server 与 edge；默认入口是 server。

FROM node:22-alpine AS webui
ARG PNPM_VERSION=9.15.4
WORKDIR /src/webui
RUN corepack enable && corepack prepare "pnpm@${PNPM_VERSION}" --activate
COPY webui/package.json webui/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY webui/ ./
RUN pnpm build

FROM golang:1.26-alpine AS builder
ARG VERSION=dev
WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# 前端产物来自上一个 stage；本行覆盖 context 中被忽略的 webui/dist。
COPY --from=webui /src/webui/dist ./webui/dist
RUN CGO_ENABLED=0 go build -tags embed_ui -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/cloudpath-server ./cmd/cloudpath-server && \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/cloudpath-edge ./cmd/cloudpath-edge

FROM alpine:3.21
RUN apk add --no-cache ca-certificates && \
    addgroup -S cloudpath && adduser -S -G cloudpath -u 65532 cloudpath && \
    mkdir -p /app /data && chown -R cloudpath:cloudpath /app /data
COPY --from=builder --chown=cloudpath:cloudpath /out/cloudpath-server /app/cloudpath-server
COPY --from=builder --chown=cloudpath:cloudpath /out/cloudpath-edge /app/cloudpath-edge
ENV CLOUDPATH_ADDR=0.0.0.0:8080 \
    CLOUDPATH_DB=/data/cloudpath.db
USER cloudpath
WORKDIR /app
VOLUME ["/data"]
EXPOSE 8080
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/app/cloudpath-server"]
