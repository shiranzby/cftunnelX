package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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

// openLogFile 打开一个用于子进程输出的日志文件（追加模式）。
//
// 🔴 必须返回 *os.File，不能赋 logutil.Writer 这类普通 io.Writer。
//
// 原因：把非 *os.File 赋给 cmd.Stdout 时，os/exec 会创建一条**管道**，
// 并在**父进程**里起 goroutine 把管道内容搬运到该 Writer。CLI 执行完
// `cftunnelX up` 会立刻退出，管道读端随之关闭；子进程之后任一时刻写
// fd 1 就会拿到 EPIPE —— 而 Go 程序在 fd 1/2 上遇到 EPIPE 会**直接终止**。
// 现象就是"日志说已启动，十几秒后进程凭空消失"，且不留任何错误。
//
// 返回的是追加模式，不会截断已有日志。
func openLogFile(name string) (*os.File, error) {
	path := filepath.Join(config.LogDir(), name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("创建日志目录失败: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("打开日志文件失败: %w", err)
	}
	return f, nil
}

// openCloudflaredLog 打开 cloudflared 的输出日志文件。
func openCloudflaredLog() (*os.File, error) {
	return openLogFile("cftunnelX.log")
}

// reapProcess 在后台回收子进程。
// Unix 上未被 Wait 的子进程退出后会变成僵尸进程，而 cftunnelX 的 Web 进程
// 是长期驻留的，反复启停隧道会持续累积僵尸进程。同时 Wait 也会结束
// exec 为 io.Writer 目标建立的内部拷贝 goroutine。
func reapProcess(cmd *exec.Cmd) {
	go func() { _ = cmd.Wait() }()
}

// WaitTunnelStable 在 timeout 内确认隧道进程仍然存活。
//
// cloudflared 启动后若因 token 无效、出口 TCP 7844 被安全组拦截等原因失败，
// 进程会在数秒内退出。不做这个检查的话，用户只会看到"已启动"，
// 而隧道实际不通——这正是之前最难排查的一类问题。
func WaitTunnelStable(tunnelID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !RunningTunnel(tunnelID) {
			return fmt.Errorf("隧道启动后进程已退出，cloudflared 输出:\n%s", tailLogLines(20))
		}
		time.Sleep(300 * time.Millisecond)
	}
	return nil
}

// tailLogLines 返回日志文件最后 n 行，用于把失败原因直接呈现给用户。
func tailLogLines(n int) string {
	path := filepath.Join(config.LogDir(), "cftunnelX.log")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("  （无法读取日志 %s: %v）", path, err)
	}
	text := strings.TrimRight(string(data), "\n")
	if text == "" {
		return fmt.Sprintf("  （日志为空，文件: %s）", path)
	}
	lines := strings.Split(text, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return "  " + strings.Join(lines, "\n  ")
}

// pidFilePath 返回单隧道 PID 文件路径（向后兼容）
func pidFilePath() string {
	return filepath.Join(config.RunDir(), "cloudflared.pid")
}

// tunnelPIDPath 返回指定隧道的 PID 文件路径
func tunnelPIDPath(tunnelID string) string {
	return filepath.Join(config.RunDir(), "cloudflared-"+tunnelID+".pid")
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
	logFile, err := openCloudflaredLog()
	if err != nil {
		return err
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("启动 cloudflared 失败: %w", err)
	}
	logFile.Close()

	os.MkdirAll(config.RunDir(), 0700)
	os.WriteFile(pidFilePath(), []byte(strconv.Itoa(cmd.Process.Pid)), 0600)
	reapProcess(cmd)
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
	logFile, err := openCloudflaredLog()
	if err != nil {
		return err
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("启动 cloudflared 失败: %w", err)
	}
	// 子进程已持有该 fd 的副本，父进程侧立即关闭，避免长期驻留的
	// Web 进程反复启停隧道时累积文件句柄。
	logFile.Close()

	os.MkdirAll(config.RunDir(), 0700)
	os.WriteFile(tunnelPIDPath(tunnelID), []byte(strconv.Itoa(cmd.Process.Pid)), 0600)
	reapProcess(cmd)
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
	matches, _ := filepath.Glob(filepath.Join(config.RunDir(), "cloudflared-*.pid"))
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
