package daemon

import (
	"os"
	"strings"
	"testing"
)

// TestCloudflaredAssets 固定各平台的 cloudflared 资产映射。
// 这些名字来自 cloudflare/cloudflared 实际发布的资产列表，改动即代表行为变更，
// 因此用测试锁住，避免再出现"下载 404"。
func TestCloudflaredAssets(t *testing.T) {
	cases := []struct {
		goos    string
		goarch  string
		want    []string
		unsupor bool
	}{
		{goos: "linux", goarch: "amd64", want: []string{"cloudflared-linux-amd64"}},
		{goos: "linux", goarch: "arm64", want: []string{"cloudflared-linux-arm64"}},
		// OpenWrt arm_cortex-a7 需要 armhf 版本
		{goos: "linux", goarch: "arm", want: []string{"cloudflared-linux-armhf", "cloudflared-linux-arm"}},
		{goos: "linux", goarch: "386", want: []string{"cloudflared-linux-386"}},
		{goos: "darwin", goarch: "arm64", want: []string{"cloudflared-darwin-arm64.tgz"}},
		{goos: "darwin", goarch: "amd64", want: []string{"cloudflared-darwin-amd64.tgz"}},
		{goos: "windows", goarch: "amd64", want: []string{"cloudflared-windows-amd64.exe"}},
		// 无官方构建的 OpenWrt 架构必须返回空，由调用方给出替代方案
		{goos: "linux", goarch: "mips", unsupor: true},
		{goos: "linux", goarch: "mipsle", unsupor: true},
		{goos: "linux", goarch: "riscv64", unsupor: true},
		{goos: "linux", goarch: "loong64", unsupor: true},
	}

	for _, tc := range cases {
		t.Run(tc.goos+"/"+tc.goarch, func(t *testing.T) {
			got := cloudflaredAssetsFor(tc.goos, tc.goarch)
			if tc.unsupor {
				if len(got) != 0 {
					t.Fatalf("无官方构建的架构应返回空, 实际 %v", got)
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

// TestUnsupportedPlatformErrorActionable 无官方构建时的报错必须给出可操作方案，
// 而不是一句"不支持的平台"（OpenWrt MIPS 路由器的实际场景）。
func TestUnsupportedPlatformErrorActionable(t *testing.T) {
	err := unsupportedPlatformError()
	if err == nil {
		t.Fatal("期望返回错误")
	}
	msg := err.Error()
	for _, want := range []string{"relay", "CFTUNNEL_CLOUDFLARED_URL", "mips"} {
		if !strings.Contains(msg, want) {
			t.Errorf("报错信息缺少关键指引 %q:\n%s", want, msg)
		}
	}
}

// TestFileUsable 0 字节残留不能被当成"已安装"。
func TestFileUsable(t *testing.T) {
	dir := t.TempDir()

	empty := dir + "/empty"
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if fileUsable(empty) {
		t.Error("0 字节文件不应被视为可用")
	}

	ok := dir + "/ok"
	if err := os.WriteFile(ok, []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !fileUsable(ok) {
		t.Error("非空文件应被视为可用")
	}

	if fileUsable(dir + "/missing") {
		t.Error("不存在的文件不应被视为可用")
	}
	if fileUsable(dir) {
		t.Error("目录不应被视为可用")
	}
}
