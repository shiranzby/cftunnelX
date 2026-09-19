package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/shiranzby/cftunnelX/internal/config"
)

// globTunnelPIDs 返回当前所有 per-tunnel PID 文件。
func globTunnelPIDs() []string {
	matches, _ := filepath.Glob(filepath.Join(config.RunDir(), "cloudflared-*.pid"))
	return matches
}

// TestAnyRunningDetectsPerTunnelPIDFile 是本项缺陷的核心回归测试。
//
// 缺陷：WebUI 启动隧道写的是 cloudflared-<隧道ID>.pid，而诊断走 daemon.Running()
// 只读 cloudflared.pid。结果是 /api/status 显示"运行中"、diagnose 显示"未运行"，
// 两个接口对同一事实给出相反结论。
//
// 测试手法：把自己（测试进程）的 PID 写进 per-tunnel PID 文件——
// 该进程必然存活，因此只要实现正确，AnyRunning 就必须报告运行中。
func TestAnyRunningDetectsPerTunnelPIDFile(t *testing.T) {
	if err := config.Ensure(); err != nil {
		t.Fatalf("准备目录失败: %v", err)
	}
	self := os.Getpid()

	// 确保旧版全局 PID 文件不存在，以证明"只凭 per-tunnel 文件"也能被识别
	_ = os.Remove(pidFilePath())

	tunnelID := "regression-test"
	pidFile := tunnelPIDPath(tunnelID)
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(self)), 0o600); err != nil {
		t.Fatalf("写入 PID 文件失败: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(pidFile) })

	inst := AnyRunning()
	if !inst.Running {
		t.Fatal("AnyRunning() 未识别 per-tunnel PID 文件——这正是 diagnose 误报未运行的原因")
	}
	if inst.PID != self {
		t.Errorf("PID = %d, 期望 %d", inst.PID, self)
	}
	if inst.Scope != "tunnel:"+tunnelID {
		t.Errorf("Scope = %q, 期望 %q", inst.Scope, "tunnel:"+tunnelID)
	}
	if inst.PidFile != pidFile {
		t.Errorf("PidFile = %q, 期望 %q", inst.PidFile, pidFile)
	}
	if inst.Hint() == "" {
		t.Error("运行中时 Hint() 不应为空")
	}

	// 与 RunningTunnels() 保持一致（诊断与 /api/status 必须同源）
	if _, ok := RunningTunnels()[tunnelID]; !ok {
		t.Error("RunningTunnels() 未包含该隧道，两个接口口径不一致")
	}
}

// TestAnyRunningDetectsLegacyPIDFile 旧版全局 PID 文件仍须被识别。
func TestAnyRunningDetectsLegacyPIDFile(t *testing.T) {
	if err := config.Ensure(); err != nil {
		t.Fatalf("准备目录失败: %v", err)
	}
	self := os.Getpid()

	// 清掉可能影响判定的 per-tunnel 文件
	for _, m := range globTunnelPIDs() {
		_ = os.Remove(m)
	}

	if err := os.WriteFile(pidFilePath(), []byte(strconv.Itoa(self)), 0o600); err != nil {
		t.Fatalf("写入 PID 文件失败: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(pidFilePath()) })

	inst := AnyRunning()
	if !inst.Running {
		t.Fatal("AnyRunning() 未识别旧版 cloudflared.pid")
	}
	if inst.Scope != "legacy" {
		t.Errorf("Scope = %q, 期望 legacy", inst.Scope)
	}
}

// TestAnyRunningNothingRunning 无 PID 文件时不应误报运行中。
func TestAnyRunningNothingRunning(t *testing.T) {
	_ = os.Remove(pidFilePath())
	for _, m := range globTunnelPIDs() {
		_ = os.Remove(m)
	}
	if inst := AnyRunning(); inst.Running {
		t.Errorf("无 PID 文件时不应报告运行中: %+v", inst)
	}
}

// TestReadPIDFileInvalid 无效内容应返回 0 而不是 panic。
func TestReadPIDFileInvalid(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"empty.pid":  "",
		"text.pid":   "not-a-number",
		"spaces.pid": "  ",
		"valid.pid":  "1234",
	} {
		p := dir + string(os.PathSeparator) + name
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		got := readPIDFile(p)
		if name == "valid.pid" && got != 1234 {
			t.Errorf("valid.pid 解析为 %d, 期望 1234", got)
		}
		if name != "valid.pid" && got != 0 {
			t.Errorf("%s 解析为 %d, 期望 0", name, got)
		}
	}
	if got := readPIDFile(dir + string(os.PathSeparator) + "missing.pid"); got != 0 {
		t.Errorf("文件不存在时应返回 0, 实际 %d", got)
	}
}
