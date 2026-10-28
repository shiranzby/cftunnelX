#!/usr/bin/env bash
# tunnel-health.sh —— 读取 cftunnelX 内置探测器得出的隧道健康态
#
# 输出单个 token（与既有外部监控的词表保持一致，便于平滑替换）：
#   OK            healthy   有活动连接
#   TUNNEL_DOWN   stalled   进程在，但没连上 Cloudflare（可自愈，重启有效）
#   NET_BLOCKED   blocked   连不到 Cloudflare（外部阻断，重启无效）
#   UNKNOWN       无法判定  cftunnelX 未运行 / 未配置 API 凭据
#
# 为什么改成读本地 API 而不是自己查 Cloudflare：
#   cftunnelX 现已内置每 30 秒一次的探测与分级自愈。外部脚本再查一次
#   CF API 会得到可能不一致的时点数据，也会额外消耗 API 配额。
#   这里直接复用内置结论，两者永远一致。
#
# 用法：
#   1) 单独运行：  ./tunnel-health.sh
#   2) 替换旧脚本：覆盖 /opt/cftunnelx/tunnel-health.sh 即可，
#                  原有 tunnel-watch.ps1 无需改动（词表相同）。
#   3) 让 ctl.sh status 也显示：
#      在 ctl.sh 输出块**末尾**追加一行（不要改动既有行格式）：
#        echo "  tunnel health   : $(/opt/cftunnelx/tunnel-health.sh)"
#
# 端口可用 CFTUNNEL_WEB_PORT 覆盖，默认 7860。

set -u

PORT="${CFTUNNEL_WEB_PORT:-7860}"
URL="http://127.0.0.1:${PORT}/api/status"

resp=$(curl -s --max-time 8 "$URL" 2>/dev/null)
if [ -z "$resp" ]; then
    echo "UNKNOWN"
    exit 0
fi

# 只取 tunnel_health 对象里的 state 字段，避免和其他含 "state" 的字段混淆
state=$(printf '%s' "$resp" \
    | grep -o '"tunnel_health":{[^}]*}' \
    | grep -o '"state":"[^"]*"' \
    | head -1 \
    | sed 's/.*:"//; s/"$//')

case "$state" in
    healthy)    echo "OK" ;;
    stalled)    echo "TUNNEL_DOWN" ;;
    blocked)    echo "NET_BLOCKED" ;;
    *)          echo "UNKNOWN" ;;
esac
