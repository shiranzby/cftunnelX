#!/usr/bin/env bash
set -euo pipefail

# 打包 OpenWrt IPK。
#
# 用法: scripts/pack-openwrt-ipk.sh <version> <openwrt-arch> <binary> [out-dir]
#
# 设计要点（对照此前无法使用的问题）：
#   - control 增加 Depends: ca-bundle / ca-certificates。
#     缺少根证书时访问 api.cloudflare.com 的 TLS 握手会失败，
#     表现为"面板能开但任何操作都报错"。
#   - procd 必须显式打开 stdout/stderr，否则服务日志全部丢失。
#   - 数据目录使用 FHS 位置（/etc/cftunnelx、/var/log/cftunnelx、
#     /var/lib/cftunnelx），不再往 /usr/bin 下写 config/ 与 log/。
#   - Web 面板的 init 脚本命名为 cftunnelx-webui，避免与
#     `cftunnelX install` 注册的隧道服务 cftunnelx-tunnel 冲突。
#   - 输出目录在脚本开头解析为绝对路径，不再依赖调用方的 $OLDPWD。

version="${1:?version required}"
arch="${2:?openwrt arch required}"
binary="${3:?binary path required}"
out_dir="${4:-dist}"

pkg_version="${version#v}"

# 脚本所在目录，用于定位同目录的 ar_create.py
script_dir="$(cd "$(dirname "$0")" && pwd)"

# 解析为绝对路径：后续要 cd 到临时目录构建，相对路径会失效
out_dir="$(mkdir -p "$out_dir" && cd "$out_dir" && pwd)"
binary="$(cd "$(dirname "$binary")" && pwd)/$(basename "$binary")"

work="$(mktemp -d)"
# CFTUNNEL_IPK_KEEP_WORK=1 时保留临时目录，便于核对 control/init.d 内容
if [ "${CFTUNNEL_IPK_KEEP_WORK:-0}" = "1" ]; then
    echo "保留临时目录: $work"
else
    trap 'rm -rf "$work"' EXIT
fi

mkdir -p "$work/control" \
         "$work/data/usr/bin" \
         "$work/data/etc/init.d"

cp "$binary" "$work/data/usr/bin/cftunnelX"
chmod 0755 "$work/data/usr/bin/cftunnelX"

# ---------------- control 元数据 ----------------
cat > "$work/control/control" <<EOF
Package: cftunnelx
Version: ${pkg_version}
Architecture: ${arch}
Maintainer: shiranzby
Section: net
Priority: optional
Depends: libc, ca-bundle, ca-certificates
License: MIT
Installed-Size: $(( $(wc -c < "$work/data/usr/bin/cftunnelX") / 1024 ))
Description: cftunnelX - Cloudflare Tunnel and frp relay management.
 Web 管理面板 + 命令行工具，用于管理 Cloudflare Tunnel 与 frp 中继穿透。
EOF

# ---------------- Web 面板 init 脚本 ----------------
cat > "$work/data/etc/init.d/cftunnelx-webui" <<'EOF'
#!/bin/sh /etc/rc.common
# cftunnelX Web 管理面板
#
# 说明: 本脚本由 IPK 提供，仅负责常驻 Web 面板。
# 隧道自身的开机自启由 `cftunnelX install` 注册为 cftunnelx-tunnel，
# 中继由 `cftunnelX relay install` 注册为 cftunnelx-relay。

START=95
STOP=10
USE_PROCD=1

PROG=/usr/bin/cftunnelX
CONF_DIR=/etc/cftunnelx
LOG_DIR=/var/log/cftunnelx
BIN_DIR=/var/lib/cftunnelx/bin
RUN_DIR=/run/cftunnelx

start_service() {
	mkdir -p "$CONF_DIR" "$LOG_DIR" "$BIN_DIR" "$RUN_DIR"
	procd_open_instance
	procd_set_param command "$PROG" web --open=false --port 7860
	procd_set_param respawn 3600 5 5
	# 不配置 stdout/stderr 时 procd 会丢弃全部输出，排障时看不到任何日志
	procd_set_param stdout 1
	procd_set_param stderr 1
	procd_close_instance
}

reload_service() {
	stop
	start
}

service_triggers() {
	procd_add_reload_trigger "cftunnelx"
}
EOF
chmod 0755 "$work/data/etc/init.d/cftunnelx-webui"

# ---------------- 安装/卸载钩子 ----------------
cat > "$work/control/postinst" <<'EOF'
#!/bin/sh
[ -n "${IPKG_INSTROOT:-}" ] && exit 0
mkdir -p /etc/cftunnelx /var/log/cftunnelx /var/lib/cftunnelx/bin
/etc/init.d/cftunnelx-webui enable
/etc/init.d/cftunnelx-webui start
exit 0
EOF
chmod 0755 "$work/control/postinst"

cat > "$work/control/prerm" <<'EOF'
#!/bin/sh
[ -n "${IPKG_INSTROOT:-}" ] && exit 0
/etc/init.d/cftunnelx-webui stop 2>/dev/null
/etc/init.d/cftunnelx-webui disable 2>/dev/null
exit 0
EOF
chmod 0755 "$work/control/prerm"

# ---------------- 组装 IPK ----------------
echo "2.0" > "$work/debian-binary"
(cd "$work/control" && tar --owner=0 --group=0 -czf "$work/control.tar.gz" .)
(cd "$work/data" && tar --owner=0 --group=0 -czf "$work/data.tar.gz" .)

ipk="${out_dir}/cftunnelX-${version}-openwrt-${arch}.ipk"
rm -f "$ipk"

# 优先用系统的 ar；缺失时回退到纯 Python 实现（binutils 并非到处都有）
if command -v ar >/dev/null 2>&1; then
    (cd "$work" && ar r "$ipk" debian-binary control.tar.gz data.tar.gz)
elif command -v python3 >/dev/null 2>&1; then
    python3 "$script_dir/ar_create.py" "$ipk" \
        "$work/debian-binary" "$work/control.tar.gz" "$work/data.tar.gz"
elif command -v python >/dev/null 2>&1; then
    python "$script_dir/ar_create.py" "$ipk" \
        "$work/debian-binary" "$work/control.tar.gz" "$work/data.tar.gz"
else
    echo "错误: 生成 IPK 需要 ar 或 python(3)，两者都不可用" >&2
    exit 1
fi
echo "已生成: $ipk"
