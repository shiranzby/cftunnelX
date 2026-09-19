package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// 路径模式。
//
// 背景（Linux / OpenWrt 适配）：
// 早期实现把所有数据都写在可执行文件同级目录。安装到 /usr/local/bin 或
// OpenWrt 的 /usr/bin 后，配置、日志与 cloudflared 二进制会落到
// /usr/local/bin/config、/usr/bin/log 这类位置：
//   - 普通用户无权写入，工具直接不可用；
//   - 违反 FHS，且在只读挂载或启用 SELinux 的系统上会被直接拒绝。
//
// 现在按"安装位置"决定模式，而不是按当前权限临时决定，避免同一台机器上
// 因 sudo/非 sudo 而读写两份不同配置。
const (
	// ModePortable 可执行文件同级目录：Windows 便携包、开发构建、解压即用。
	ModePortable = "portable"
	// ModeSystem 系统级目录：/etc、/var/log、/var/lib（OpenWrt、服务器安装）。
	ModeSystem = "system"
	// ModeUser 用户级目录：~/.config、~/.local（系统级不可写时的降级）。
	ModeUser = "user"
	// ModeCustom 由 CFTUNNEL_HOME 显式指定。
	ModeCustom = "custom"
)

var (
	pathOnce     sync.Once
	configDir    string
	logDir       string
	binDir       string
	runDir       string
	mode         string
	degradedTo   string // 发生降级时，记录原本不可写的系统目录
	migratedFrom string // 发生过配置迁移时，记录旧配置路径
)

// systemBinDirs 返回"系统级安装"的判定目录。
// 可执行文件位于这些目录时，数据改写到系统级路径。
func systemBinDirs() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"/usr/bin", "/usr/sbin", "/usr/local/bin", "/usr/local/sbin", "/opt/homebrew/bin"}
	case "windows":
		return nil // Windows 一律便携模式，保持便携包行为不变
	default:
		return []string{"/bin", "/sbin", "/usr/bin", "/usr/sbin", "/usr/local/bin", "/usr/local/sbin"}
	}
}

func systemPaths() (cfg, log, bin, run string) {
	if runtime.GOOS == "darwin" {
		return "/usr/local/etc/cftunnelx",
			"/usr/local/var/log/cftunnelx",
			"/usr/local/var/lib/cftunnelx/bin",
			"/usr/local/var/run/cftunnelx"
	}
	return "/etc/cftunnelx",
		"/var/log/cftunnelx",
		"/var/lib/cftunnelx/bin",
		"/run/cftunnelx"
}

func userPaths() (cfg, log, bin, run string) {
	home, _ := os.UserHomeDir()
	cfgBase := os.Getenv("XDG_CONFIG_HOME")
	if cfgBase == "" {
		cfgBase = filepath.Join(home, ".config")
	}
	stateBase := os.Getenv("XDG_STATE_HOME")
	if stateBase == "" {
		stateBase = filepath.Join(home, ".local", "state")
	}
	dataBase := os.Getenv("XDG_DATA_HOME")
	if dataBase == "" {
		dataBase = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(cfgBase, "cftunnelX"),
		filepath.Join(stateBase, "cftunnelX", "log"),
		filepath.Join(dataBase, "cftunnelX", "bin"),
		filepath.Join(stateBase, "cftunnelX", "run")
}

// executableDir 返回可执行文件所在目录（已解析符号链接）。
func executableDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Dir(exe)
}

func isExistingDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func isExistingFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// canUseDir 判断目录是否可创建/可写。
func canUseDir(path string) bool {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return false
	}
	probe := filepath.Join(path, ".write-probe")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return false
	}
	_ = f.Close()
	_ = os.Remove(probe)
	return true
}

// resolvePaths 是路径决策的纯计算部分（不含副作用），便于单元测试。
//
// 决策顺序：
//  1. cftunnelHome 非空 → 自定义目录
//  2. exeDir 下已有 config/ 目录或 portable 标记文件 → 便携模式
//  3. exeDir 属于 systemDirs（系统级安装位置）→ 系统模式；
//     系统目录不可写时降级到用户模式，并记录 degradedFrom
//  4. 其他位置 → 便携模式
//
// usable 用于判断目录是否可创建/可写；返回 (config, log, bin, run, mode, degradedFrom)。
func resolvePaths(exeDir, cftunnelHome string, systemDirs []string, usable func(string) bool) (
	cfgDir, logDir, binDir, runDir, mode, degradedFrom string,
) {
	if cftunnelHome != "" {
		return filepath.Join(cftunnelHome, "config"),
			filepath.Join(cftunnelHome, "log"),
			filepath.Join(cftunnelHome, "bin"),
			filepath.Join(cftunnelHome, "run"),
			ModeCustom, ""
	}

	if exeDir != "" {
		if isExistingDir(filepath.Join(exeDir, "config")) || isExistingFile(filepath.Join(exeDir, "portable")) {
			return portablePaths(exeDir, ModePortable)
		}
	}

	if exeDir != "" && inDirList(exeDir, systemDirs) {
		sysCfg, sysLog, sysBin, sysRun := systemPaths()
		if usable(sysCfg) {
			return sysCfg, sysLog, sysBin, sysRun, ModeSystem, ""
		}
		usrCfg, usrLog, usrBin, usrRun := userPaths()
		if usable(usrCfg) {
			return usrCfg, usrLog, usrBin, usrRun, ModeUser, sysCfg
		}
		// 都不可写：保留系统路径，让后续操作报出确切路径
		return sysCfg, sysLog, sysBin, sysRun, ModeSystem, ""
	}

	if exeDir != "" {
		return portablePaths(exeDir, ModePortable)
	}

	// 无法定位可执行文件时的兜底
	return "config", "log", filepath.Join("config", "bin"), "config", ModePortable, ""
}

func portablePaths(root, mode string) (cfgDir, logDir, binDir, runDir, m, degraded string) {
	cfgDir = filepath.Join(root, "config")
	return cfgDir,
		filepath.Join(root, "log"),
		filepath.Join(cfgDir, "bin"),
		cfgDir,
		mode, ""
}

func initPaths() {
	pathOnce.Do(func() {
		configDir, logDir, binDir, runDir, mode, degradedTo = resolvePaths(
			executableDir(),
			strings.TrimSpace(os.Getenv("CFTUNNEL_HOME")),
			systemBinDirs(),
			canUseDir,
		)
	})
}

func inDirList(dir string, list []string) bool {
	clean := filepath.Clean(dir)
	for _, d := range list {
		if clean == filepath.Clean(d) {
			return true
		}
	}
	return false
}

// Dir 返回配置目录。
func Dir() string {
	initPaths()
	return configDir
}

// LogDir 返回日志目录。
func LogDir() string {
	initPaths()
	return logDir
}

// BinDir 返回外部依赖二进制目录（cloudflared / frpc / frps）。
func BinDir() string {
	initPaths()
	return binDir
}

// RunDir 返回运行时目录（PID 文件）。系统模式下位于 /run，重启后自动清空，
// 避免残留过期 PID。
func RunDir() string {
	initPaths()
	return runDir
}

// Mode 返回当前路径模式，取值见 ModePortable / ModeSystem / ModeUser / ModeCustom。
func Mode() string {
	initPaths()
	return mode
}

// Portable 返回是否处于便携模式。
func Portable() bool {
	return Mode() == ModePortable
}

// DegradedFrom 在系统级目录不可写、已降级到用户目录时返回原系统目录，否则返回空串。
func DegradedFrom() string {
	initPaths()
	return degradedTo
}

// Ensure 创建运行所需的全部目录。
// 失败时给出包含具体路径的可操作提示，而不是笼统的 permission denied。
func Ensure() error {
	type target struct {
		path string
		perm os.FileMode
	}
	targets := []target{
		{Dir(), 0o700},
		{LogDir(), 0o755},
		{BinDir(), 0o755},
		{RunDir(), 0o755},
	}
	for _, t := range targets {
		if t.path == "" {
			continue
		}
		if err := os.MkdirAll(t.path, t.perm); err != nil {
			return fmt.Errorf(
				"无法创建目录 %s: %w\n"+
					"  提示: 系统级安装请使用 sudo 运行；或设置环境变量 CFTUNNEL_HOME\n"+
					"        指向可写目录（例如 export CFTUNNEL_HOME=$HOME/.cftunnelx）",
				t.path, err)
		}
	}
	migrateLegacyConfigIfNeeded()
	return nil
}

// migrateLegacyConfigIfNeeded 把旧版"可执行文件同级 config/"中的配置迁移到新位置。
//
// v4.92 及更早版本在 Linux 上固定把数据写在 exe 同级目录（例如
// /usr/local/bin/config）。升级到分区路径后，如果新位置还没有配置，
// 就从旧位置搬一份过来，避免用户"配置凭空消失"。
func migrateLegacyConfigIfNeeded() {
	newPath := Path()
	if _, err := os.Stat(newPath); err == nil {
		return
	}
	exeDir := executableDir()
	if exeDir == "" {
		return
	}
	legacyDir := filepath.Join(exeDir, "config")
	if filepath.Clean(legacyDir) == filepath.Clean(Dir()) {
		return // 本身就是便携模式，无需迁移
	}
	legacyConfig := filepath.Join(legacyDir, "config.yml")
	data, err := os.ReadFile(legacyConfig)
	if err != nil {
		return
	}
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return
	}
	if err := os.WriteFile(newPath, data, 0o600); err != nil {
		return
	}
	migratedFrom = legacyConfig
}

// MigratedFrom 在本次运行发生过配置迁移时返回旧配置路径，否则返回空串。
func MigratedFrom() string {
	initPaths()
	return migratedFrom
}

// PathsSummary 返回便于排障的路径与模式摘要。
func PathsSummary() string {
	s := fmt.Sprintf("模式=%s 配置=%s 日志=%s 依赖=%s 运行=%s",
		Mode(), Dir(), LogDir(), BinDir(), RunDir())
	if from := DegradedFrom(); from != "" {
		s += fmt.Sprintf("（注意: %s 不可写，已降级到用户目录）", from)
	}
	if from := MigratedFrom(); from != "" {
		s += fmt.Sprintf("（已从旧位置迁移配置: %s）", from)
	}
	return s
}
