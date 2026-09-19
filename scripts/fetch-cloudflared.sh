#!/usr/bin/env bash
set -euo pipefail

# 为"内嵌 cloudflared"构建准备资源文件。
#
# 用法:
#   scripts/fetch-cloudflared.sh [goos] [goarch]
# 默认按当前运行环境选择；结果写入
#   internal/daemon/assets/cloudflared.gz
# 之后用以下命令构建即可让二进制自带 cloudflared（无需联网下载）:
#   go build -tags bundled_cloudflared ...
#
# 说明:
#   - 内嵌的是**单一架构**的 cloudflared，因此每个 GOOS/GOARCH 都要单独准备与构建。
#   - assets/*.gz 不入版本库（见 .gitignore），由本脚本按需生成。
#   - 若网络受限，可先手工下载对应二进制，再用 --from <文件> 指定。

GOOS_TARGET="${1:-$(go env GOOS)}"
GOARCH_TARGET="${2:-$(go env GOARCH)}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
ASSET_DIR="$REPO_DIR/internal/daemon/assets"
ASSET="$ASSET_DIR/cloudflared.gz"

# cloudflared 官方资产名（2026.9 核对；无 mips / riscv64 构建）
case "$GOOS_TARGET/$GOARCH_TARGET" in
    linux/amd64)   ASSET_NAME="cloudflared-linux-amd64" ;;
    linux/arm64)   ASSET_NAME="cloudflared-linux-arm64" ;;
    linux/arm)     ASSET_NAME="cloudflared-linux-armhf" ;;
    linux/386)     ASSET_NAME="cloudflared-linux-386" ;;
    darwin/amd64)  ASSET_NAME="cloudflared-darwin-amd64.tgz" ;;
    darwin/arm64)  ASSET_NAME="cloudflared-darwin-arm64.tgz" ;;
    windows/amd64) ASSET_NAME="cloudflared-windows-amd64.exe" ;;
    windows/386)   ASSET_NAME="cloudflared-windows-386.exe" ;;
    *)
        echo "错误: cloudflared 没有 $GOOS_TARGET/$GOARCH_TARGET 的官方构建。" >&2
        echo "      Cloudflare 仅提供 amd64 / arm64 / arm(armhf) / 386。" >&2
        echo "      该架构请使用中继模式（frp 覆盖全部架构）。" >&2
        exit 1
        ;;
esac

ORIGIN="https://github.com/cloudflare/cloudflared/releases/latest/download/${ASSET_NAME}"
MIRRORS=(
    "https://gh-proxy.com/"
    "https://ghproxy.cn/"
    "https://ghproxy.net/"
    "https://ghfast.top/"
    "https://mirror.ghproxy.com/"
    "https://hub.gitmirror.com/"
    ""
)

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

downloaded=""
for mirror in "${MIRRORS[@]}"; do
    url="${mirror}${ORIGIN}"
    label="${mirror:-GitHub 直连}"
    printf '尝试下载 %s ... ' "$label"
    if curl -fsSL --ssl-no-revoke --connect-timeout 15 --max-time 300 -o "$TMP_DIR/$ASSET_NAME" "$url" 2>/dev/null; then
        echo "成功"
        downloaded="$TMP_DIR/$ASSET_NAME"
        break
    fi
    echo "失败"
done

if [ -z "$downloaded" ]; then
    echo "错误: 所有下载源均失败。" >&2
    echo "      可手工下载 $ASSET_NAME 后自行压缩到:" >&2
    echo "      internal/daemon/assets/cloudflared.gz" >&2
    echo "      即: gzip -9 -c <下载的文件> > internal/daemon/assets/cloudflared.gz" >&2
    exit 1
fi

# darwin 的资产是 .tgz，需要先解包
if [ "${ASSET_NAME##*.}" = "tgz" ]; then
    tar xzf "$downloaded" -C "$TMP_DIR"
    downloaded="$TMP_DIR/cloudflared"
fi

mkdir -p "$ASSET_DIR"
gzip -9 -c "$downloaded" > "$ASSET"
echo "已生成 $ASSET ($(du -h "$ASSET" | cut -f1))"
echo
echo "现在可用以下命令构建自带 cloudflared 的二进制:"
echo "  GOOS=$GOOS_TARGET GOARCH=$GOARCH_TARGET CGO_ENABLED=0 go build -tags bundled_cloudflared ."
