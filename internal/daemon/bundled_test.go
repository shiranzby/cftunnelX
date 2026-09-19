package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEmbeddedCloudflaredAsset 验证内嵌的 cloudflared 能被正确解压为可执行文件。
//
// 未启用 bundled_cloudflared 构建标签时内嵌资源为空，本测试直接跳过——
// 这样默认构建与内嵌构建都能通过同一套测试。
//
// 运行内嵌版本的测试:
//
//	go test -tags bundled_cloudflared ./internal/daemon/ -run TestEmbeddedCloudflaredAsset -v
func TestEmbeddedCloudflaredAsset(t *testing.T) {
	size := EmbeddedCloudflaredSize()
	if size == 0 {
		t.Log("本构建未内嵌 cloudflared（未启用 bundled_cloudflared 标签），跳过")
		return
	}
	if size < 5<<20 {
		t.Errorf("内嵌资源仅 %d 字节，明显小于 cloudflared 的实际体积（约 18MB 压缩后）", size)
	}

	// 刻意不用 t.TempDir()：解压出的是 ~38MB 的可执行文件，紧接着
	// t.TempDir 的 RemoveAll 在 Windows 上会因句柄尚未完全释放
	// （或杀软/索引器短暂扫描）而报 "The directory is not empty"，
	// 让测试变成随机失败。这里改用自建目录 + 尽力而为的清理。
	dir, err := os.MkdirTemp("", "cftunnelx-embed-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	dest := filepath.Join(dir, "cloudflared")
	if err := extractEmbeddedCloudflared(dest); err != nil {
		t.Fatalf("解压内嵌 cloudflared 失败: %v", err)
	}

	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("解压后文件不存在: %v", err)
	}
	// 解压后应显著大于压缩包（压缩率约 50%）
	if info.Size() <= int64(size) {
		t.Errorf("解压后大小 %d 不大于压缩大小 %d，疑似解压不完整", info.Size(), size)
	}

	// 校验可执行文件魔数：ELF / PE
	// 显式 Close 而不是 defer——不给 Windows 留下"清理时句柄仍在"的机会
	f, err := os.Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 4)
	_, readErr := f.Read(head)
	closeErr := f.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}

	switch {
	case string(head) == "\x7fELF":
	case len(head) >= 2 && head[0] == 'M' && head[1] == 'Z':
	default:
		t.Errorf("解压结果不是可执行文件，前 4 字节: % x", head)
	}
}

// TestExtractEmbeddedWithoutAsset 未内嵌时应返回明确错误而不是写出空文件。
func TestExtractEmbeddedWithoutAsset(t *testing.T) {
	if EmbeddedCloudflaredSize() != 0 {
		t.Skip("本构建内嵌了 cloudflared，跳过")
	}
	dest := filepath.Join(t.TempDir(), "cloudflared")
	if err := extractEmbeddedCloudflared(dest); err == nil {
		t.Error("未内嵌时应返回错误")
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("未内嵌时不应产生输出文件")
	}
}
