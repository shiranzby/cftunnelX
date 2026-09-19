//go:build windows

package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

// TestPE_Subsystem 验证能正确识别自身（控制台子系统）与 GUI 子系统。
func TestPE_Subsystem(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Skipf("无法定位测试二进制: %v", err)
	}
	gui, ok := peSubsystemIsGUI(self)
	if !ok {
		t.Fatal("无法读取测试二进制自身的 PE 子系统信息")
	}
	// go test 生成的是控制台子系统程序
	if gui {
		t.Error("测试二进制应被识别为控制台子系统，而不是 GUI 子系统")
	}
}

func TestPE_SubsystemNonPE(t *testing.T) {
	f := filepath.Join(t.TempDir(), "not-a-pe.txt")
	if err := os.WriteFile(f, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok := peSubsystemIsGUI(f); ok {
		t.Error("非 PE 文件应返回 ok=false")
	}
}

func TestQuoteWindowsArg(t *testing.T) {
	got := quoteWindowsArg(`C:\Program Files\cftunnelX\cftunnelX.exe`)
	want := `"C:\Program Files\cftunnelX\cftunnelX.exe"`
	if got != want {
		t.Errorf("quoteWindowsArg() = %q, 期望 %q", got, want)
	}
}

// TestUTF16LEWithBOM 隐藏启动器需以 UTF-16LE+BOM 保存，才能支持含中文的路径。
func TestUTF16LEWithBOM(t *testing.T) {
	out := utf16LEWithBOM("a中")
	if len(out) < 2 || out[0] != 0xFF || out[1] != 0xFE {
		t.Fatalf("缺少 UTF-16LE BOM: % x", out[:2])
	}
	// 'a' -> 61 00, '中'(U+4E2D) -> 2D 4E
	want := []byte{0xFF, 0xFE, 0x61, 0x00, 0x2D, 0x4E}
	if string(out) != string(want) {
		t.Errorf("编码结果 = % x, 期望 % x", out, want)
	}
}

// TestWriteHiddenLauncher 校验生成的 VBS 含隐藏窗口样式且路径被正确引用。
func TestWriteHiddenLauncher(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "含 空格", "cftunnelX-cli.exe")
	shim := filepath.Join(dir, "autostart.vbs")

	if err := writeHiddenLauncher(shim, exe); err != nil {
		t.Fatalf("writeHiddenLauncher 失败: %v", err)
	}
	raw, err := os.ReadFile(shim)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 2 || raw[0] != 0xFF || raw[1] != 0xFE {
		t.Fatal("隐藏启动器缺少 UTF-16LE BOM")
	}
	// 按 UTF-16LE 正确解码，验证含中文的路径没有丢失
	units := make([]uint16, 0, (len(raw)-2)/2)
	for i := 2; i+1 < len(raw); i += 2 {
		units = append(units, uint16(raw[i])|uint16(raw[i+1])<<8)
	}
	body := string(utf16.Decode(units))

	if !strings.Contains(body, "WScript.Shell") {
		t.Error("VBS 缺少 WScript.Shell")
	}
	want := `sh.Run """` + exe + `"" web --open=false", 0, False`
	if !strings.Contains(body, want) {
		t.Errorf("VBS Run 行未按预期转义与隐藏窗口样式 0\n实际: %s\n期望含: %s", body, want)
	}
	// 中文目录名必须完整保留
	if !strings.Contains(body, "含 空格") {
		t.Errorf("含中文的路径未正确写入 VBS: %s", body)
	}
}
