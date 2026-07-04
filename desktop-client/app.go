package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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

func (a *App) OpenWebUI() string {
	if err := a.ensureWebUI(); err != nil {
		return err.Error()
	}
	return a.WebURL()
}

func (a *App) WebURL() string {
	return "http://127.0.0.1:7860"
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
