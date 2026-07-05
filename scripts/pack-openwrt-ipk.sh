#!/usr/bin/env bash
set -euo pipefail

version="${1:?version required}"
arch="${2:?openwrt arch required}"
binary="${3:?binary path required}"
out_dir="${4:-dist}"

pkg_version="${version#v}"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/control" "$work/data/usr/bin" "$work/data/etc/init.d" "$out_dir"
cp "$binary" "$work/data/usr/bin/cftunnelX"
chmod 0755 "$work/data/usr/bin/cftunnelX"

cat > "$work/control/control" <<EOF
Package: cftunnelx
Version: ${pkg_version}
Architecture: ${arch}
Maintainer: shiranzby
Section: net
Priority: optional
Description: cftunnelX Cloudflare Tunnel and relay management tool.
EOF

cat > "$work/data/etc/init.d/cftunnelx" <<'EOF'
#!/bin/sh /etc/rc.common
START=90
STOP=10
USE_PROCD=1

start_service() {
  mkdir -p /usr/bin/config /usr/bin/log
  procd_open_instance
  procd_set_param command /usr/bin/cftunnelX web --open=false --port 7860
  procd_set_param respawn 3600 5 5
  procd_close_instance
}
EOF
chmod 0755 "$work/data/etc/init.d/cftunnelx"

echo "2.0" > "$work/debian-binary"
(cd "$work/control" && tar --owner=0 --group=0 -czf "$work/control.tar.gz" .)
(cd "$work/data" && tar --owner=0 --group=0 -czf "$work/data.tar.gz" .)
(cd "$work" && ar r "${OLDPWD}/${out_dir}/cftunnelX-${version}-openwrt-${arch}.ipk" debian-binary control.tar.gz data.tar.gz)
