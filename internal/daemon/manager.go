package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shiranzby/cftunnelX/internal/config"
	"github.com/shiranzby/cftunnelX/internal/logutil"
)

// ts 返回时间戳 [2026-07-03 23:05:40]
func ts() string {
	return logutil.Timestamp()
}

// logf 打印带时间戳的日志
func logf(format string, args ...interface{}) {
	logutil.Write(filepath.Join(config.LogDir(), "cftunnelX.log"), "INFO", format, args...)
}

// pidFilePath 返回单隧道 PID 文件路径（向后兼容）
func pidFilePath() string {
	return filepath.Join(config.Dir(), "cloudflared.pid")
}

// tunnelPIDPath 返回指定隧道的 PID 文件路径
func tunnelPIDPath(tunnelID string) string {
	return filepath.Join(config.Dir(), "cloudflared-"+tunnelID+".pid")
}

// Start 启动 cloudflared（token 模式，单隧道向后兼容）
func Start(token string) error {
	binPath, err := EnsureCloudflared()
	if err != nil {
		return err
	}
	if Running() {
		return fmt.Errorf("cloudflared 已在运行")
	}

	cmd := exec.Command(binPath, "tunnel", "--protocol", "http2", "run", "--token", token)
	configureCommand(cmd)
	cmd.Stdout = &logutil.Writer{Path: filepath.Join(config.LogDir(), "cftunnelX.log")}
	cmd.Stderr = &logutil.Writer{Path: filepath.Join(config.LogDir(), "cftunnelX.log")}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 cloudflared 失败: %w", err)
	}

	os.MkdirAll(config.Dir(), 0700)
	os.WriteFile(pidFilePath(), []byte(strconv.Itoa(cmd.Process.Pid)), 0600)
	logf("cloudflared 已启动 (PID: %d)", cmd.Process.Pid)
	return nil
}

// StartTunnel 启动指定隧道（多隧道模式）
func StartTunnel(tunnelID, token string) error {
	binPath, err := EnsureCloudflared()
	if err != nil {
		return err
	}
	if RunningTunnel(tunnelID) {
		return fmt.Errorf("隧道 %s 已在运行", tunnelID)
	}

	cmd := exec.Command(binPath, "tunnel", "--protocol", "http2", "run", "--token", token)
	configureCommand(cmd)
	cmd.Stdout = &logutil.Writer{Path: filepath.Join(config.LogDir(), "cftunnelX.log")}
	cmd.Stderr = &logutil.Writer{Path: filepath.Join(config.LogDir(), "cftunnelX.log")}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 cloudflared 失败: %w", err)
	}

	os.MkdirAll(config.Dir(), 0700)
	os.WriteFile(tunnelPIDPath(tunnelID), []byte(strconv.Itoa(cmd.Process.Pid)), 0600)
	logf("隧道 %s 已启动 (PID: %d)", tunnelID, cmd.Process.Pid)
	return nil
}

// Stop 停止 cloudflared（单隧道向后兼容）
func Stop() error {
	pid, err := readPID()
	if err != nil {
		return fmt.Errorf("未找到运行中的 cloudflared")
	}
	if err := processKill(pid); err != nil {
		return fmt.Errorf("停止 cloudflared 失败: %w", err)
	}
	os.Remove(pidFilePath())
	logf("cloudflared 已停止")
	return nil
}

// StopTunnel 停止指定隧道
func StopTunnel(tunnelID string) error {
	pidFile := tunnelPIDPath(tunnelID)
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return fmt.Errorf("未找到运行中的隧道 %s", tunnelID)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return err
	}
	if err := processKill(pid); err != nil {
		return fmt.Errorf("停止隧道 %s 失败: %w", tunnelID, err)
	}
	os.Remove(pidFile)
	logf("隧道 %s 已停止", tunnelID)
	return nil
}

// Running 检查 cloudflared 是否在运行（单隧道向后兼容）
func Running() bool {
	pid, err := readPID()
	if err != nil {
		return false
	}
	return processRunning(pid)
}

// RunningTunnel 检查指定隧道是否在运行
func RunningTunnel(tunnelID string) bool {
	data, err := os.ReadFile(tunnelPIDPath(tunnelID))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false
	}
	return processRunning(pid)
}

// TunnelPID 返回指定隧道的 PID
func TunnelPID(tunnelID string) int {
	data, err := os.ReadFile(tunnelPIDPath(tunnelID))
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return pid
}

// RunningTunnels 返回所有运行中的隧道 ID → PID 映射
func RunningTunnels() map[string]int {
	result := make(map[string]int)
	matches, _ := filepath.Glob(filepath.Join(config.Dir(), "cloudflared-*.pid"))
	for _, m := range matches {
		base := filepath.Base(m)
		// cloudflared-<id>.pid
		tunnelID := strings.TrimSuffix(strings.TrimPrefix(base, "cloudflared-"), ".pid")
		data, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			continue
		}
		if processRunning(pid) {
			result[tunnelID] = pid
		} else {
			os.Remove(m) // 清理过期 PID 文件
		}
	}
	return result
}

// PID 返回当前运行的 PID（单隧道向后兼容）
func PID() int {
	pid, _ := readPID()
	return pid
}

func readPID() (int, error) {
	data, err := os.ReadFile(pidFilePath())
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}
