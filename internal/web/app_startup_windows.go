//go:build windows

package web

import (
	"debug/pe"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"github.com/shiranzby/cftunnelX/internal/config"
)

const (
	appStartupTaskName = "cftunnelX-webui"
	appStartupShimName = "cftunnelX-autostart.vbs"
)

// autostartArgs 自启动时传给程序自身的参数。
// 使用 web --open=false：只把 Web 服务拉到后台，不弹浏览器窗口。
var autostartArgs = []string{"web", "--open=false"}

// peSubsystemIsGUI 判断可执行文件是否为 GUI 子系统（启动时不创建控制台窗口）。
// ok=false 表示无法判定（非 PE 文件或读取失败）。
func peSubsystemIsGUI(path string) (gui bool, ok bool) {
	f, err := pe.Open(path)
	if err != nil {
		return false, false
	}
	defer f.Close()
	switch oh := f.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		return oh.Subsystem == pe.IMAGE_SUBSYSTEM_WINDOWS_GUI, true
	case *pe.OptionalHeader32:
		return oh.Subsystem == pe.IMAGE_SUBSYSTEM_WINDOWS_GUI, true
	}
	return false, false
}

// autostartPlan 描述如何注册自启动。
type autostartPlan struct {
	Exe     string // 实际要拉起的可执行文件
	Command string // 写入 schtasks /TR 的完整命令
	Mode    string // gui = 直接拉起无控制台程序；hidden = 经 VBScript 隐藏窗口拉起
	Shim    string // hidden 模式下的 vbs 路径，gui 模式为空
}

// buildAutostartPlan 选择不会弹出控制台窗口的自启动方案。
//
// 优先级：
//  1. 当前程序自身是 GUI 子系统 → 直接注册（完全无窗口）
//  2. 同目录存在 GUI 子系统的 cftunnelX.exe → 注册它
//  3. 只有控制台程序 → 生成 VBScript 隐藏启动器（以窗口样式 0 拉起，无闪窗）
func buildAutostartPlan() (*autostartPlan, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	self, err = filepath.Abs(self)
	if err != nil {
		return nil, err
	}
	selfDir := filepath.Dir(self)

	// 1) 优先选择无控制台窗口的候选程序
	candidates := []string{self, filepath.Join(selfDir, "cftunnelX.exe")}
	seen := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		key := strings.ToLower(c)
		if seen[key] {
			continue
		}
		seen[key] = true
		if _, statErr := os.Stat(c); statErr != nil {
			continue
		}
		if gui, ok := peSubsystemIsGUI(c); ok && gui {
			return &autostartPlan{
				Exe:     c,
				Command: quoteWindowsArg(c) + " " + strings.Join(autostartArgs, " "),
				Mode:    "gui",
			}, nil
		}
	}

	// 2) 只有控制台程序：用 VBScript 隐藏窗口启动，避免登录时弹出 cmd 窗口
	shim := filepath.Join(config.Dir(), appStartupShimName)
	if err := writeHiddenLauncher(shim, self); err != nil {
		return nil, fmt.Errorf("生成隐藏启动器失败: %w", err)
	}
	return &autostartPlan{
		Exe:     self,
		Command: "wscript.exe " + quoteWindowsArg(shim),
		Mode:    "hidden",
		Shim:    shim,
	}, nil
}

// quoteWindowsArg 为路径加双引号（schtasks /TR 需要）。
func quoteWindowsArg(s string) string {
	return `"` + s + `"`
}

// writeHiddenLauncher 生成以隐藏窗口方式拉起程序的 VBScript。
// 文件使用 UTF-16LE + BOM 保存，以正确支持含中文的路径。
func writeHiddenLauncher(path, exe string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("' cftunnelX auto-generated hidden autostart launcher\r\n")
	b.WriteString("' 由 cftunnelX 自动生成，请勿手工修改；关闭软件开机自启时会一并删除。\r\n")
	b.WriteString("Set sh = CreateObject(\"WScript.Shell\")\r\n")
	b.WriteString(`sh.CurrentDirectory = "` + filepath.Dir(exe) + "\"\r\n")
	// 窗口样式 0 = 隐藏；最后一个参数 False 表示不等待返回
	b.WriteString(`sh.Run """` + exe + `"" ` + strings.Join(autostartArgs, " ") + `", 0, False` + "\r\n")
	return os.WriteFile(path, utf16LEWithBOM(b.String()), 0600)
}

// utf16LEWithBOM 把字符串编码为 UTF-16LE 并加 BOM（wscript 可正确识别）。
func utf16LEWithBOM(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, 0, 2+len(units)*2)
	out = append(out, 0xFF, 0xFE)
	for _, u := range units {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

func appStartupStatus() map[string]interface{} {
	status := map[string]interface{}{
		"supported":  true,
		"installed":  false,
		"running":    appProcessRunning(),
		"task_name":  appStartupTaskName,
		"os_trigger": "ONLOGON",
	}
	out, err := hiddenCommand("schtasks", "/Query", "/TN", appStartupTaskName, "/FO", "LIST").Output()
	if err != nil {
		return status
	}
	status["installed"] = true
	status["raw"] = string(out)
	if plan, planErr := buildAutostartPlan(); planErr == nil {
		status["command"] = plan.Command
		status["mode"] = plan.Mode
	}
	return status
}

func appStartupInstall() error {
	plan, err := buildAutostartPlan()
	if err != nil {
		return err
	}
	// 先删旧任务，避免同名冲突与旧参数残留
	_ = hiddenCommand("schtasks", "/Delete", "/TN", appStartupTaskName, "/F").Run()
	out, err := hiddenCommand("schtasks", "/Create",
		"/TN", appStartupTaskName,
		"/SC", "ONLOGON",
		"/TR", plan.Command,
		"/RL", "LIMITED",
		"/F").CombinedOutput()
	if err != nil {
		return fmt.Errorf("创建自启动任务失败: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func appStartupUninstall() error {
	err := hiddenCommand("schtasks", "/Delete", "/TN", appStartupTaskName, "/F").Run()
	// 清理隐藏启动器
	_ = os.Remove(filepath.Join(config.Dir(), appStartupShimName))
	return err
}

func appProcessRunning() bool {
	out, err := hiddenCommand("tasklist", "/FO", "CSV").Output()
	if err != nil {
		return false
	}
	s := strings.ToLower(string(out))
	return strings.Contains(s, "cftunnelx.exe") ||
		strings.Contains(s, "cftunnelx-cli.exe") ||
		strings.Contains(s, "cftunnelx-desktop.exe")
}
