package relay

import "testing"

// TestFrpAssetsOpenWrt 锁定 OpenWrt 各架构的 frp 资产映射。
// frp 覆盖了 cloudflared 不支持的 mips / mipsle / riscv64，
// 这是 OpenWrt 路由器仍能使用本工具（中继模式）的前提。
func TestFrpAssetsOpenWrt(t *testing.T) {
	cases := []struct {
		goos    string
		goarch  string
		want    []string
		unsupor bool
	}{
		// 典型的 OpenWrt 目标架构
		{goos: "linux", goarch: "amd64", want: []string{"frp_0.66.0_linux_amd64.tar.gz"}},
		{goos: "linux", goarch: "arm64", want: []string{"frp_0.66.0_linux_arm64.tar.gz"}},
		{goos: "linux", goarch: "arm", want: []string{"frp_0.66.0_linux_arm_hf.tar.gz", "frp_0.66.0_linux_arm.tar.gz"}},
		{goos: "linux", goarch: "mipsle", want: []string{"frp_0.66.0_linux_mipsle.tar.gz"}},
		{goos: "linux", goarch: "mips", want: []string{"frp_0.66.0_linux_mips.tar.gz"}},
		{goos: "linux", goarch: "riscv64", want: []string{"frp_0.66.0_linux_riscv64.tar.gz"}},
		{goos: "linux", goarch: "loong64", want: []string{"frp_0.66.0_linux_loong64.tar.gz"}},
		{goos: "windows", goarch: "amd64", want: []string{"frp_0.66.0_windows_amd64.zip"}},
		// frp 没有 386 构建
		{goos: "linux", goarch: "386", unsupor: true},
	}

	for _, tc := range cases {
		t.Run(tc.goos+"/"+tc.goarch, func(t *testing.T) {
			got := frpAssetsFor(tc.goos, tc.goarch)
			if tc.unsupor {
				if len(got) != 0 {
					t.Fatalf("应返回空, 实际 %v", got)
				}
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("资产数 = %d (%v), 期望 %d (%v)", len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("资产[%d] = %q, 期望 %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}
