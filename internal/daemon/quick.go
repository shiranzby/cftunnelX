package daemon

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shiranzby/cftunnelX/internal/authproxy"
	"github.com/shiranzby/cftunnelX/internal/config"
)

// quickConfigPath 返回 quick 模式专用的空配置文件路径
// 防止 cloudflared 读取用户已有的 ~/.cloudflared/config.yml 导致 UUID 解析失败
func quickConfigPath() string {
	p := filepath.Join(config.Dir(), "quick-config.yml")
	if _, err := os.Stat(p); os.IsNotExist(err) {
		os.MkdirAll(config.Dir(), 0700)
		os.WriteFile(p, []byte("# cftunnel quick mode - empty config\n"), 0600)
	}
	return p
}

// quickState 免域名模式运行状态（进程内）
var (
	quickState struct {
		sync.Mutex
		cmd       *exec.Cmd
		url       string
		running   bool
		port      string // 保活用：记住端口
		stopWatch bool   // 停止 watchdog 标志
	}
)

// QuickURL 返回免域名模式的临时公网地址（未启动返回空）
func QuickURL() string {
	quickState.Lock()
	defer quickState.Unlock()
	return quickState.url
}

// QuickRunning 免域名模式是否在运行
func QuickRunning() bool {
	quickState.Lock()
	defer quickState.Unlock()
	if !quickState.running || quickState.cmd == nil {
		return false
	}
	if quickState.cmd.ProcessState != nil {
		quickState.running = !quickState.cmd.ProcessState.Exited()
		return quickState.running
	}
	return true
}

// QuickPID 免域名模式进程 PID
func QuickPID() int {
	quickState.Lock()
	defer quickState.Unlock()
	if quickState.cmd != nil && quickState.cmd.Process != nil {
		return quickState.cmd.Process.Pid
	}
	return 0
}

// StartQuickBackground 后台启动免域名模式，返回 URL 通道
// 零配置生成 *.trycloudflare.com 临时公网地址，后台持续运行直到手动停止
// 内置 watchdog：进程异常退出后 5 秒自动重启，软件关闭则连接断开
func StartQuickBackground(port string) (<-chan string, error) {
	binPath, err := EnsureCloudflared()
	if err != nil {
		return nil, err
	}
	if QuickRunning() {
		return nil, fmt.Errorf("免域名模式已在运行")
	}
	if Running() {
		return nil, fmt.Errorf("cloudflared 隧道已在运行，请先停止")
	}

	quickState.Lock()
	quickState.port = port
	quickState.stopWatch = false
	quickState.Unlock()

	urlCh := make(chan string, 1)
	startQuickProcess(binPath, port, urlCh)

	// 启动 watchdog：进程退出后自动重启（最多重试 5 次，间隔 5 秒）
	go func() {
		retries := 0
		for {
			time.Sleep(3 * time.Second)
			quickState.Lock()
			stop := quickState.stopWatch
			cmd := quickState.cmd
			quickState.Unlock()
			if stop {
				return
			}
			// 进程已退出
			if cmd == nil || (cmd.ProcessState != nil && cmd.ProcessState.Exited()) {
				retries++
				if retries > 5 {
					logf("[watchdog] 免域名模式重启次数超限(5次)，停止保活")
					return
				}
				quickState.Lock()
				p := quickState.port
				quickState.Unlock()
				if p == "" {
					return
				}
				logf("[watchdog] 免域名模式进程退出，5秒后重启(第%d次)...", retries)
				time.Sleep(5 * time.Second)
				// 检查是否已被手动停止
				quickState.Lock()
				stop2 := quickState.stopWatch
				quickState.Unlock()
				if stop2 {
					return
				}
				ch := make(chan string, 1)
				startQuickProcess(binPath, p, ch)
				// 等待新 URL
				go func() {
					select {
					case newURL := <-ch:
						if newURL != "" {
							quickState.Lock()
							quickState.url = newURL
							quickState.Unlock()
							retries = 0 // 成功获取URL，重置重试计数器
							logf("[watchdog] 免域名模式已重启: %s", newURL)
						}
					case <-time.After(30 * time.Second):
					}
				}()
			}
		}
	}()

	// 超时兜底：30 秒未获取 URL 视为失败
	go func() {
		select {
		case <-urlCh:
			return
		case <-time.After(30 * time.Second):
			select {
			case urlCh <- "":
			default:
			}
		}
	}()

	return urlCh, nil
}

// startQuickProcess 启动单个 cloudflared 进程
func startQuickProcess(binPath, port string, urlCh chan<- string) {
	cfgPath := quickConfigPath()
	cmd := exec.Command(binPath, "tunnel", "--config", cfgPath, "--url", "http://localhost:"+port)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		select {
		case urlCh <- "":
		default:
		}
		return
	}
	logFile, err := os.OpenFile(quickLogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		select {
		case urlCh <- "":
		default:
		}
		return
	}
	cmd.Stdout = logFile

	if err := cmd.Start(); err != nil {
		logFile.Close()
		select {
		case urlCh <- "":
		default:
		}
		return
	}

	quickState.Lock()
	quickState.cmd = cmd
	quickState.running = true
	quickState.url = ""
	quickState.Unlock()

	os.WriteFile(quickPIDPath(), []byte(strconv.Itoa(cmd.Process.Pid)), 0600)

	// 后台扫描 stderr 提取 URL
	go func() {
		defer logFile.Close()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "trycloudflare.com") {
				url := extractURL(line)
				if url != "" {
					quickState.Lock()
					quickState.url = url
					quickState.Unlock()
					select {
					case urlCh <- url:
					default:
					}
				}
			}
			fmt.Fprintln(logFile, line)
		}
		// 进程退出
		quickState.Lock()
		quickState.running = false
		quickState.Unlock()
	}()
}

// StopQuick 停止免域名模式
func StopQuick() error {
	quickState.Lock()
	quickState.stopWatch = true // 停止 watchdog
	cmd := quickState.cmd
	quickState.Unlock()

	if cmd != nil && cmd.Process != nil {
		stopChildProcess(cmd)
		cmd.Process.Kill()
		cmd.Wait()
	}

	quickState.Lock()
	quickState.cmd = nil
	quickState.running = false
	quickState.url = ""
	quickState.port = ""
	quickState.Unlock()
	os.Remove(quickPIDPath())
	return nil
}

func quickPIDPath() string {
	return filepath.Join(config.Dir(), "quick.pid")
}

func quickLogPath() string {
	return filepath.Join(config.Dir(), "quick.log")
}

// StartQuick 启动免域名模式（前台运行，Ctrl+C 退出）
func StartQuick(port string) error {
	binPath, err := EnsureCloudflared()
	if err != nil {
		return err
	}
	if Running() {
		return fmt.Errorf("cloudflared 已在运行，请先执行 cftunnel down")
	}

	cfgPath := quickConfigPath()
	cmd := exec.Command(binPath, "tunnel", "--config", cfgPath, "--url", "http://localhost:"+port)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	cmd.Stdout = os.Stdout

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 cloudflared 失败: %w", err)
	}

	go scanForURL(stderr)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-sig:
		stopChildProcess(cmd)
		<-done
	case err := <-done:
		if err != nil {
			return fmt.Errorf("cloudflared 异常退出: %w", err)
		}
	}
	return nil
}

func scanForURL(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "trycloudflare.com") {
			url := extractURL(line)
			if url != "" {
				fmt.Printf("\n✔ 隧道已启动: %s\n\n", url)
			}
		}
		fmt.Fprintln(os.Stderr, line)
	}
}

func extractURL(line string) string {
	for _, part := range strings.Fields(line) {
		if strings.Contains(part, "trycloudflare.com") && strings.HasPrefix(part, "http") {
			return part
		}
	}
	return ""
}

// StartQuickWithAuth 启动带鉴权代理的免域名模式
func StartQuickWithAuth(port, username, password string) error {
	binPath, err := EnsureCloudflared()
	if err != nil {
		return err
	}
	if Running() {
		return fmt.Errorf("cloudflared 已在运行，请先执行 cftunnel down")
	}

	proxy, err := authproxy.New(authproxy.Config{
		Username:   username,
		Password:   password,
		TargetPort: port,
		SigningKey:  authproxy.RandomKey(),
		CookieTTL:  24 * time.Hour,
	})
	if err != nil {
		return fmt.Errorf("启动鉴权代理失败: %w", err)
	}
	if err := proxy.Start(); err != nil {
		return fmt.Errorf("启动鉴权代理失败: %w", err)
	}
	defer proxy.Stop()

	proxyPort := fmt.Sprintf("%d", proxy.ListenPort())
	fmt.Printf("鉴权代理已启动 127.0.0.1:%s → 127.0.0.1:%s\n", proxyPort, port)

	cfgPath := quickConfigPath()
	cmd := exec.Command(binPath, "tunnel", "--config", cfgPath, "--url", "http://localhost:"+proxyPort)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	cmd.Stdout = os.Stdout

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 cloudflared 失败: %w", err)
	}

	go scanForURL(stderr)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-sig:
		stopChildProcess(cmd)
		<-done
	case err := <-done:
		if err != nil {
			return fmt.Errorf("cloudflared 异常退出: %w", err)
		}
	}
	return nil
}
