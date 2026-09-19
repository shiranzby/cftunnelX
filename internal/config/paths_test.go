package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// linuxSystemDirs 用于测试的"系统级安装目录"清单（固定为 Linux 语义，
// 使测试在任何开发机上都能验证 Linux/OpenWrt 的路径决策）。
var linuxSystemDirs = []string{"/bin", "/sbin", "/usr/bin", "/usr/sbin", "/usr/local/bin", "/usr/local/sbin"}

func alwaysUsable(string) bool { return true }
func neverUsable(string) bool  { return false }

// TestResolvePathsOpenWrt 模拟 OpenWrt：二进制在 /usr/bin，
// 数据必须落到系统级目录，而不是 /usr/bin/config。
func TestResolvePathsOpenWrt(t *testing.T) {
	cfgDir, logDir, binDir, runDir, mode, degraded := resolvePaths(
		"/usr/bin", "", linuxSystemDirs, alwaysUsable)

	if mode != ModeSystem {
		t.Fatalf("mode = %q, 期望 %q", mode, ModeSystem)
	}
	if filepath.Dir(cfgDir) == "/usr/bin" || cfgDir == "/usr/bin/config" {
		t.Fatalf("配置目录不能位于二进制目录下: %s", cfgDir)
	}
	if degraded != "" {
		t.Errorf("可直接写入时不应发生降级, degraded=%q", degraded)
	}
	for name, dir := range map[string]string{"配置": cfgDir, "日志": logDir, "依赖": binDir, "运行": runDir} {
		if dir == "" {
			t.Errorf("%s目录为空", name)
		}
		if runtime.GOOS != "windows" && dir == "/usr/bin/config" {
			t.Errorf("%s目录仍在 /usr/bin 下: %s", name, dir)
		}
	}
}

// TestResolvePathsSystemNotWritable 系统目录不可写时必须降级到用户目录，
// 并记录 degraded 来源，供 CLI 打印警告。
func TestResolvePathsSystemNotWritable(t *testing.T) {
	// 用 systemPaths() 的实际返回值构造"不可写"判定。
	// 不要写死 /etc 与 /var：macOS 上系统路径是 /usr/local/etc 等，
	// 写死会让本测试在 macOS CI 上把系统分支误判为可写（曾经如此）。
	sysCfg, sysLog, sysBin, sysRun := systemPaths()
	sysDirs := map[string]bool{sysCfg: true, sysLog: true, sysBin: true, sysRun: true}
	sysUnwritable := func(p string) bool { return !sysDirs[p] }

	cfgDir, _, _, _, mode, degraded := resolvePaths(
		"/usr/local/bin", "", linuxSystemDirs, sysUnwritable)

	if mode != ModeUser {
		t.Fatalf("mode = %q, 期望 %q", mode, ModeUser)
	}
	if degraded == "" {
		t.Error("降级时应记录原系统目录")
	}
	if cfgDir == "" {
		t.Error("降级后配置目录不应为空")
	}
	if sysDirs[cfgDir] {
		t.Errorf("降级后配置目录不应仍是系统目录, 实际 %q", cfgDir)
	}
}

// TestResolvePathsAllUnwritable 系统与用户目录都不可写时保留系统路径，
// 以便后续报错能指出确切路径。
func TestResolvePathsAllUnwritable(t *testing.T) {
	cfgDir, _, _, _, mode, degraded := resolvePaths(
		"/usr/local/bin", "", linuxSystemDirs, neverUsable)

	if mode != ModeSystem {
		t.Fatalf("mode = %q, 期望 %q", mode, ModeSystem)
	}
	if degraded != "" {
		t.Errorf("全都不可写时不应标记为降级, 实际 %q", degraded)
	}
	if cfgDir == "" {
		t.Error("配置目录不应为空")
	}
}

// TestResolvePathsPortableMarker 同级存在 config/ 时保持便携模式。
func TestResolvePathsPortableMarker(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfgDir, logDir, binDir, _, mode, _ := resolvePaths(dir, "", linuxSystemDirs, alwaysUsable)

	if mode != ModePortable {
		t.Fatalf("mode = %q, 期望 %q", mode, ModePortable)
	}
	if want := filepath.Join(dir, "config"); cfgDir != want {
		t.Errorf("配置目录 = %q, 期望 %q", cfgDir, want)
	}
	if want := filepath.Join(dir, "log"); logDir != want {
		t.Errorf("日志目录 = %q, 期望 %q", logDir, want)
	}
	// 便携模式下依赖仍放在 config/bin，保持对既有便携包的兼容
	if want := filepath.Join(dir, "config", "bin"); binDir != want {
		t.Errorf("依赖目录 = %q, 期望 %q", binDir, want)
	}
}

// TestResolvePathsPortableFileMarker 存在 portable 标记文件也算便携模式。
func TestResolvePathsPortableFileMarker(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "portable"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, mode, _ := resolvePaths(dir, "", linuxSystemDirs, alwaysUsable)
	if mode != ModePortable {
		t.Fatalf("mode = %q, 期望 %q", mode, ModePortable)
	}
}

// TestResolvePathsHomeOverride CFTUNNEL_HOME 优先级最高。
func TestResolvePathsHomeOverride(t *testing.T) {
	home := t.TempDir()
	cfgDir, logDir, binDir, runDir, mode, _ := resolvePaths("/usr/bin", home, linuxSystemDirs, alwaysUsable)

	if mode != ModeCustom {
		t.Fatalf("mode = %q, 期望 %q", mode, ModeCustom)
	}
	if want := filepath.Join(home, "config"); cfgDir != want {
		t.Errorf("配置目录 = %q, 期望 %q", cfgDir, want)
	}
	if want := filepath.Join(home, "log"); logDir != want {
		t.Errorf("日志目录 = %q, 期望 %q", logDir, want)
	}
	if want := filepath.Join(home, "bin"); binDir != want {
		t.Errorf("依赖目录 = %q, 期望 %q", binDir, want)
	}
	if want := filepath.Join(home, "run"); runDir != want {
		t.Errorf("运行目录 = %q, 期望 %q", runDir, want)
	}
}

// TestResolvePathsDevBuild 非系统目录（开发构建）保持便携模式。
func TestResolvePathsDevBuild(t *testing.T) {
	dir := t.TempDir()
	cfgDir, _, _, _, mode, _ := resolvePaths(dir, "", linuxSystemDirs, alwaysUsable)
	if mode != ModePortable {
		t.Fatalf("mode = %q, 期望 %q", mode, ModePortable)
	}
	if want := filepath.Join(dir, "config"); cfgDir != want {
		t.Errorf("配置目录 = %q, 期望 %q", cfgDir, want)
	}
}

// TestEnsureCreatesDirs 验证 Ensure 会创建全部目录。
func TestEnsureCreatesDirs(t *testing.T) {
	if err := Ensure(); err != nil {
		t.Fatalf("Ensure 失败: %v", err)
	}
	for _, d := range []string{Dir(), LogDir(), BinDir(), RunDir()} {
		if info, err := os.Stat(d); err != nil || !info.IsDir() {
			t.Errorf("目录未创建: %s (%v)", d, err)
		}
	}
}
