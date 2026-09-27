# syntax=docker/dockerfile:1

# =============================================================================
# 构建阶段
#
# 用 --platform=$BUILDPLATFORM 让构建阶段跑在**原生架构**上（CI runner 是 amd64），
# 再通过 GOOS/GOARCH 交叉编译出目标架构的产物。多架构构建因此不必全程走 QEMU 模拟，
# 速度显著更快，也避开了在模拟环境里跑 go build 的各类坑。
#
# 注意：不要把 GOARCH 写死。曾经这里硬编码 GOARCH=amd64，而 CI 声明了
# platforms: linux/amd64,linux/arm64 —— 结果 arm64 镜像里装的是 amd64 二进制，
# 在 arm64 设备上启动即 exec format error。
# =============================================================================
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

# TARGETARCH / TARGETOS 由 buildx 依据 --platform 自动注入（amd64、arm64、arm、386…）
ARG TARGETARCH
ARG TARGETOS

# 固定 cloudflared 版本，保证构建可复现。
# 如需尝鲜可传 --build-arg CLOUDFLARED_VERSION=latest
ARG CLOUDFLARED_VERSION=2026.9.3

WORKDIR /src
RUN apk add --no-cache git ca-certificates curl

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# 按目标架构下载 cloudflared 并放入产物目录。
#
# 为什么放 /usr/local/bin 而不是内嵌进二进制：
#   程序查找 cloudflared 的顺序是「已有本地文件 → 同目录附带 → 系统 PATH →
#   内嵌资源 → 联网下载」。PATH 在"内嵌资源"之前，因此放进 PATH 既不需要
#   bundled_cloudflared 构建标签，也避免了容器首次运行时把 38MB 解压写入数据卷。
#
# cloudflared 是静态链接的 Go 二进制（实测无 PT_INTERP、无 libc.so 依赖），
# 可直接运行在 musl 的 Alpine 上。
#
# 官方只提供 amd64 / arm64 / arm(armhf) / 386 四种 Linux 构建。其它架构
# （mips、riscv64 等）没有官方产物，此处跳过并明确提示改用中继模式。
#
# 国内网络不通 GitHub 时按顺序回退到镜像前缀重试。
RUN set -eu; \
    mkdir -p /out/bin; \
    case "$TARGETARCH" in \
      amd64) cf_asset="cloudflared-linux-amd64" ;; \
      arm64) cf_asset="cloudflared-linux-arm64" ;; \
      arm)   cf_asset="cloudflared-linux-armhf" ;; \
      386)   cf_asset="cloudflared-linux-386" ;; \
      *)     cf_asset="" ;; \
    esac; \
    if [ -z "$cf_asset" ]; then \
      echo "警告: cloudflared 没有 $TARGETARCH 的官方构建，本镜像将不含 cloudflared。"; \
      echo "      该架构请使用中继(relay)模式。"; \
    else \
      if [ "$CLOUDFLARED_VERSION" = "latest" ]; then \
        cf_path="releases/latest/download/$cf_asset"; \
      else \
        cf_path="releases/download/$CLOUDFLARED_VERSION/$cf_asset"; \
      fi; \
      ok=0; \
      for base in \
        "https://github.com/cloudflare/cloudflared" \
        "https://gh-proxy.com/https://github.com/cloudflare/cloudflared" \
        "https://ghproxy.net/https://github.com/cloudflare/cloudflared"; do \
        echo "下载 cloudflared: $base/$cf_path"; \
        if curl -fsSL --retry 2 --connect-timeout 20 -o /out/bin/cloudflared "$base/$cf_path"; then \
          ok=1; break; \
        fi; \
        echo "  该源失败，尝试下一个"; \
      done; \
      if [ "$ok" != "1" ]; then \
        echo "错误: cloudflared 下载失败（GitHub 与镜像源均不可达）。" >&2; \
        echo "      可在有网络的环境构建，或传入可用的 CLOUDFLARED_VERSION/镜像源。" >&2; \
        exit 1; \
      fi; \
      chmod +x /out/bin/cloudflared; \
      /out/bin/cloudflared --version; \
    fi

RUN CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    go build -buildvcs=false -ldflags="-s -w" -o /out/cftunnelX .

# =============================================================================
# 运行阶段
# =============================================================================
FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY --from=builder /out/cftunnelX /app/cftunnelX
COPY --from=builder /out/bin/cloudflared /usr/local/bin/cloudflared

RUN adduser -D -H -u 10001 cftunnelx \
 && mkdir -p /app/config /app/log \
 && chown -R cftunnelx:cftunnelx /app

USER cftunnelx
EXPOSE 7860
VOLUME ["/app/config", "/app/log"]

# 健康检查端口。仅用于本 HEALTHCHECK，不是应用自身的配置项；
# 若用 `--port` 或界面改了监听端口，请同步覆盖此变量（docker run -e / compose environment）。
ENV CFTUNNEL_HEALTHCHECK_PORT=7860

# /api/version 属免认证路径，容器内用 busybox wget 探测即可
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD wget -q -O /dev/null "http://127.0.0.1:${CFTUNNEL_HEALTHCHECK_PORT}/api/version" || exit 1

ENTRYPOINT ["/app/cftunnelX"]
CMD ["web", "--open=false"]
