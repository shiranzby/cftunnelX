//go:build !bundled_cloudflared

package daemon

// cloudflaredAsset 在未启用 bundled_cloudflared 构建标签时为空。
//
// 此时 EnsureCloudflared 走既有流程：
// 本地已有 → 随包附带（exe 同级）→ 系统 PATH → 网络下载（多镜像回退）。
// 这样默认构建产物不会因为内嵌 18MB 资源而变胖，同时保留"想要离线可用就带标签构建"的自由。
var cloudflaredAsset []byte
