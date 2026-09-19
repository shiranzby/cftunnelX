//go:build bundled_cloudflared

package daemon

import _ "embed"

// cloudflaredAsset 是构建时内嵌的 cloudflared（gzip 压缩）。
//
// 启用方式：构建前用 scripts/fetch-cloudflared.sh 把目标架构的 cloudflared
// 压缩到 internal/daemon/assets/cloudflared.gz，然后加 `-tags bundled_cloudflared` 构建。
//
// 注意：内嵌的是**单一架构**的二进制，因此每个 GOOS/GOARCH 必须单独构建。
// assets/*.gz 不入版本库（见 .gitignore），由构建流程按需准备。
//
//go:embed assets/cloudflared.gz
var cloudflaredAsset []byte
