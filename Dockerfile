# ============ 构建阶段 ============
# 1. 前端构建（Vue3 + Vite）
FROM --platform=$BUILDPLATFORM node:20-alpine AS web-builder
WORKDIR /build
COPY web/package.json web/package-lock.json* ./
RUN npm config set registry https://registry.npmmirror.com && \
    (npm ci --no-audit --no-fund 2>/dev/null || npm install --no-audit --no-fund)
COPY web/ ./
# 显式指定输出目录，不依赖 vite.config 的 outDir（其默认写到 workdir 之外的 ../internal/api/dist）
RUN npm run build -- --outDir /build/dist --emptyOutDir

# 2. 后端构建（纯静态，CGO_ENABLED=0 支持多架构交叉编译）
FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS go-builder
ARG TARGETOS=linux
ARG TARGETARCH=amd64
WORKDIR /build
ENV GOPROXY=https://goproxy.cn,direct
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# 先注入前端产物
COPY --from=web-builder /build/dist/ internal/api/dist/
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w" -o /out/go-emby .

# ============ 运行阶段 ============
FROM alpine:3.20
RUN apk add --no-cache ffmpeg tzdata ca-certificates && \
    addgroup -S app && adduser -S app -G app -u 1000 || true
COPY --from=go-builder /out/go-emby /usr/local/bin/go-emby
# 数据目录与媒体目录
RUN mkdir -p /config /media
VOLUME ["/config", "/media"]
EXPOSE 8097
ENV GEMBY_DATA=/config \
    MEDIA_ROOTS=/media \
    GEMBY_ADDR=:8097
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s \
    CMD wget -qO- http://127.0.0.1:8097/health || exit 1
ENTRYPOINT ["/usr/local/bin/go-emby"]
