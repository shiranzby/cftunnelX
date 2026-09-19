#!/bin/bash
set -e

REPO="shiranzby/cftunnelX"
INSTALL_DIR="/usr/local/bin"

OS=$(uname -s | tr A-Z a-z)
ARCH=$(uname -m)
case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "不支持的架构: $ARCH"; exit 1 ;;
esac

FILENAME="cftunnelX_${OS}_${ARCH}.tar.gz"
DOWNLOAD_URL="https://github.com/$REPO/releases/latest/download/$FILENAME"
MIRRORS=("https://ghfast.top/" "https://gh-proxy.com/" "https://ghproxy.cn/" "")

echo "正在下载 cftunnelX ($OS/$ARCH)..."
TMP=$(mktemp -d)
download_ok=false
for mirror in "${MIRRORS[@]}"; do
  url="${mirror}${DOWNLOAD_URL}"
  src="${mirror:-GitHub}"
  echo "  尝试: ${src} ..."
  if curl -fsSL --connect-timeout 10 -o "$TMP/$FILENAME" "$url"; then
    download_ok=true; echo "  下载成功"; break
  fi
  echo "  失败，尝试下一个源..."
done
if [ "$download_ok" = false ]; then
  echo "所有下载源均失败，请检查网络后重试"; rm -rf "$TMP"; exit 1
fi
tar xzf "$TMP/$FILENAME" -C "$TMP"
sudo install -m 755 "$TMP/cftunnelX" "$INSTALL_DIR/cftunnelX"
rm -rf "$TMP"

# 预创建系统级数据目录。
# 安装在 /usr/local/bin 时程序会使用 FHS 路径，不再往二进制目录写 config/ 与 log/。
if [ "$(id -u)" -eq 0 ]; then
  install -d -m 0700 /etc/cftunnelx
  install -d -m 0755 /var/log/cftunnelx /var/lib/cftunnelx/bin /run/cftunnelx
elif command -v sudo >/dev/null 2>&1; then
  sudo install -d -m 0700 /etc/cftunnelx
  sudo install -d -m 0755 /var/log/cftunnelx /var/lib/cftunnelx/bin /run/cftunnelx
fi

if [ "$("$INSTALL_DIR/cftunnelX" version 2>/dev/null | head -n 1)" ] ; then
  echo "cftunnelX 已安装到 $INSTALL_DIR/cftunnelX"
else
  echo "警告: cftunnelX 已复制到 $INSTALL_DIR/cftunnelX，但自检未通过"
  echo "      请手动执行 $INSTALL_DIR/cftunnelX version 确认"
fi
echo "运行 cftunnelX init 开始配置"
echo "启动管理面板: cftunnelX web   （无图形界面环境默认监听 0.0.0.0:7860，可从局域网访问）"
