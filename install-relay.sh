#!/usr/bin/env bash
set -euo pipefail

# cftunnelX 中继服务端一键安装脚本
# 用法: curl -fsSL https://raw.githubusercontent.com/shiranzby/cftunnelX/main/install-relay.sh | bash
#
# 同时兼容普通 Linux 发行版（systemd）与 OpenWrt（procd）：
#   - 下载工具自动选择 curl / wget / uclient-fetch（OpenWrt 默认不带 curl）
#   - 服务注册自动选择 systemd 或 procd
#   - 不依赖 xxd / install 等 BusyBox 可能缺失的命令

FRP_VERSION="0.66.0"
BIND_PORT="${RELAY_PORT:-7000}"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/frps"
SERVICE_NAME="frps"

# 颜色（BusyBox 的 echo 不一定支持 -e，用 printf 保证可移植）
info()  { printf '\033[0;32m[INFO]\033[0m %s\n' "$*"; }
warn()  { printf '\033[1;33m[WARN]\033[0m %s\n' "$*"; }
error() { printf '\033[0;31m[ERROR]\033[0m %s\n' "$*"; exit 1; }

# 检查 root
if [ "$(id -u)" -ne 0 ]; then
    error "请使用 root 用户运行，或 sudo bash"
fi

# 检查系统
if [ "$(uname -s)" != "Linux" ]; then
    error "此脚本仅支持 Linux"
fi

# 是否使用 procd（OpenWrt）
USE_PROCD=0
if [ ! -d /run/systemd/system ] && [ -f /etc/rc.common ]; then
    USE_PROCD=1
    INSTALL_DIR="/usr/bin"
    info "检测到 OpenWrt（procd），安装目录调整为 $INSTALL_DIR"
fi

# 下载工具探测：OpenWrt 默认不带 curl
DOWNLOADER=""
for cand in curl wget uclient-fetch; do
    if command -v "$cand" >/dev/null 2>&1; then
        DOWNLOADER="$cand"
        break
    fi
done
[ -n "$DOWNLOADER" ] || error "缺少下载工具，请先安装 curl、wget 或 uclient-fetch"
info "下载工具: $DOWNLOADER"

download() {
    url="$1"; dest="$2"
    case "$DOWNLOADER" in
        curl)          curl -fsSL --connect-timeout 10 -o "$dest" "$url" ;;
        wget)          wget -q -T 10 -O "$dest" "$url" ;;
        uclient-fetch) uclient-fetch -q -T 10 -O "$dest" "$url" ;;
    esac
}

# 检测架构（frp 覆盖 OpenWrt 常见架构，含 mips / mipsle / riscv64）
ARCH=$(uname -m)
case "$ARCH" in
    x86_64|amd64)        FRP_ARCH="amd64" ;;
    aarch64|arm64)       FRP_ARCH="arm64" ;;
    armv7l|armv7)        FRP_ARCH="arm_hf" ;;
    armv6l|armv6|arm)    FRP_ARCH="arm" ;;
    mips64el)            FRP_ARCH="mips64le" ;;
    mips64)              FRP_ARCH="mips64" ;;
    mipsel)              FRP_ARCH="mipsle" ;;
    mips)                FRP_ARCH="mips" ;;
    riscv64)             FRP_ARCH="riscv64" ;;
    loongarch64|loong64) FRP_ARCH="loong64" ;;
    *)                   error "不支持的架构: $ARCH" ;;
esac
info "架构: $ARCH -> frp_${FRP_ARCH}"

FILENAME="frp_${FRP_VERSION}_linux_${FRP_ARCH}.tar.gz"
DOWNLOAD_URL="https://github.com/fatedier/frp/releases/download/v${FRP_VERSION}/${FILENAME}"

MIRRORS=(
    "https://ghfast.top/"
    "https://gh-proxy.com/"
    "https://ghproxy.cn/"
    ""
)

TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

download_ok=false
for mirror in "${MIRRORS[@]}"; do
    url="${mirror}${DOWNLOAD_URL}"
    src="${mirror:-GitHub}"
    info "尝试下载: ${src} ..."
    if download "$url" "$TMP_DIR/$FILENAME"; then
        download_ok=true
        info "下载成功"
        break
    fi
    warn "下载失败，尝试下一个源..."
done
[ "$download_ok" = true ] || error "所有下载源均失败"

info "正在解压..."
tar -xzf "$TMP_DIR/$FILENAME" -C "$TMP_DIR"
EXTRACT_DIR="$TMP_DIR/frp_${FRP_VERSION}_linux_${FRP_ARCH}"
if [ ! -d "$EXTRACT_DIR" ]; then
    EXTRACT_DIR="$(find "$TMP_DIR" -maxdepth 1 -type d -name 'frp_*' | head -n 1)"
fi
[ -n "$EXTRACT_DIR" ] && [ -d "$EXTRACT_DIR" ] || error "解压目录未找到"

mkdir -p "$INSTALL_DIR"
cp "$EXTRACT_DIR/frps" "$INSTALL_DIR/frps"
chmod 0755 "$INSTALL_DIR/frps"
info "frps 已安装到 $INSTALL_DIR/frps"

# 生成随机 token（不依赖 xxd，BusyBox 的 od 一定存在）
TOKEN=$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n')

mkdir -p "$CONFIG_DIR"
cat > "$CONFIG_DIR/frps.toml" <<EOF
bindPort = ${BIND_PORT}
auth.token = "${TOKEN}"
EOF
chmod 600 "$CONFIG_DIR/frps.toml"
info "配置文件: $CONFIG_DIR/frps.toml"

if [ "$USE_PROCD" -eq 1 ]; then
    # ---------- OpenWrt: procd ----------
    cat > /etc/init.d/frps <<EOF
#!/bin/sh /etc/rc.common
# frps relay server (cftunnelX)

START=95
STOP=10
USE_PROCD=1

start_service() {
	procd_open_instance
	procd_set_param command ${INSTALL_DIR}/frps -c ${CONFIG_DIR}/frps.toml
	procd_set_param respawn 3600 5 5
	procd_set_param stdout 1
	procd_set_param stderr 1
	procd_close_instance
}
EOF
    chmod 0755 /etc/init.d/frps
    /etc/init.d/frps enable
    /etc/init.d/frps restart
    info "procd 服务已启动 (frps)"
else
    # ---------- 常规发行版: systemd ----------
    cat > /etc/systemd/system/${SERVICE_NAME}.service <<EOF
[Unit]
Description=frps relay server (cftunnelX)
After=network-online.target
Wants=network-online.target

[Service]
ExecStart="${INSTALL_DIR}/frps" -c "${CONFIG_DIR}/frps.toml"
Restart=always
RestartSec=5
LimitNOFILE=65535
SyslogIdentifier=frps

[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload
    systemctl enable --now "$SERVICE_NAME"
    info "systemd 服务已启动 (frps)"
fi

# 获取服务器 IP
SERVER_IP=""
if command -v curl >/dev/null 2>&1; then
    SERVER_IP=$(curl -s --connect-timeout 5 https://api.ipify.org 2>/dev/null || true)
fi
if [ -z "$SERVER_IP" ]; then
    SERVER_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
fi
[ -n "$SERVER_IP" ] || SERVER_IP="<服务器IP>"

echo ""
echo "=============================================="
echo "      frps 中继服务端安装完成"
echo "=============================================="
echo ""
echo "  二进制: $INSTALL_DIR/frps"
echo "  配置:   $CONFIG_DIR/frps.toml"
echo "  端口:   $BIND_PORT"
echo ""
echo "在客户端执行以下命令连接:"
echo ""
echo "  cftunnelX relay init --server ${SERVER_IP}:${BIND_PORT} --token ${TOKEN}"
echo ""
echo "也可以使用 Docker 部署: https://github.com/shiranzby/cftunnelX#relay-server"
echo ""
