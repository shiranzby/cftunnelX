package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type App struct {
	ctx    context.Context
	webCmd *exec.Cmd
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	go a.ensureWebUI()
}

// shutdown 在窗口关闭时终止由本客户端拉起的 Web 服务子进程，
// 避免桌面端退出后残留一个后台 cftunnelX-cli web 进程。
func (a *App) shutdown(ctx context.Context) {
	if a.webCmd == nil || a.webCmd.Process == nil {
		return
	}
	_ = a.webCmd.Process.Kill()
	a.webCmd = nil
}

func (a *App) OpenWebUI() string {
	if err := a.ensureWebUI(); err != nil {
		return err.Error()
	}
	return a.WebURL()
}

// WebURL 返回 Web 服务地址。
// 端口必须与 CLI 实际使用的端口一致：CLI 在未显式传 --port 时会优先采用
// config.yml 里的 web_ui.port，因此这里读取同一份配置，而不是硬编码 7860。
func (a *App) WebURL() string {
	return "http://127.0.0.1:" + a.webPort()
}

// webPort 读取 CLI 配置文件中的 web_ui.port，读取失败时回退到 7860。
func (a *App) webPort() string {
	exe, err := os.Executable()
	if err != nil {
		return defaultWebPort
	}
	configPath := filepath.Join(filepath.Dir(exe), "config", "config.yml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return defaultWebPort
	}
	return parseWebUIPort(string(data))
}

const defaultWebPort = "7860"

// parseWebUIPort 在不引入 YAML 依赖的前提下，从配置文本中取出 web_ui.port。
// 只做极小的缩进感知扫描，够用且不会因 YAML 特性产生歧义。
func parseWebUIPort(cfg string) string {
	inWebUI := false
	for _, raw := range strings.Split(cfg, "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			// 顶层键
			inWebUI = trimmed == "web_ui:"
			continue
		}
		if !inWebUI {
			continue
		}
		if strings.HasPrefix(trimmed, "port:") {
			value := strings.TrimSpace(strings.TrimPrefix(trimmed, "port:"))
			value = strings.Trim(value, `"'`)
			if value != "" {
				return value
			}
		}
	}
	return defaultWebPort
}

func (a *App) CLIStatus() string {
	name := a.cliPath()
	out, err := exec.Command(name, "version").CombinedOutput()
	if err != nil {
		return fmt.Sprintf("%s not ready: %v", name, err)
	}
	return string(out)
}

func (a *App) ensureWebUI() error {
	if webReady(a.WebURL()) {
		return nil
	}
	if a.webCmd != nil && a.webCmd.Process != nil {
		return waitForWeb(a.WebURL(), 5*time.Second)
	}
	cli := a.cliPath()
	cmd := exec.Command(cli, "web", "--open=false")
	cmd.Dir = filepath.Dir(cli)
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	a.webCmd = cmd
	go cmd.Wait()
	return waitForWeb(a.WebURL(), 10*time.Second)
}

func (a *App) cliPath() string {
	exe, _ := os.Executable()
	exeDir := filepath.Dir(exe)
	candidates := []string{}
	if isWindows() {
		candidates = append(candidates,
			filepath.Join(exeDir, "cftunnelX-cli.exe"),
			filepath.Join(exeDir, "cftunnelX.exe"),
			"cftunnelX-cli.exe",
			"cftunnelX.exe",
		)
	} else {
		candidates = append(candidates,
			filepath.Join(exeDir, "cftunnelX-cli"),
			filepath.Join(exeDir, "cftunnelX"),
			"cftunnelX-cli",
			"cftunnelX",
		)
	}
	for _, candidate := range candidates {
		if resolved, err := exec.LookPath(candidate); err == nil {
			return resolved
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return candidates[len(candidates)-1]
}

func webReady(url string) bool {
	client := http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}

func waitForWeb(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if webReady(url) {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("Web service startup timed out; keep cftunnelX-cli beside cftunnelX-desktop")
}
