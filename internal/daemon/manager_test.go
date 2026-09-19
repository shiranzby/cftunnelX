package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shiranzby/cftunnelX/internal/config"
)

// TestNoPipeWriterForCloudflared 锁定"子进程输出必须重定向到真实文件"这一约束。
//
// 根因回顾：把 logutil.Writer 这类非 *os.File 赋给 cmd.Stdout 时，
// os/exec 会创建管道并在**父进程**里起 goroutine 搬运数据。CLI 执行完
// `cftunnelX up` 立即退出 → 管道读端关闭 → cloudflared 之后写 fd 1 拿到 EPIPE
// → Go 程序在 fd 1/2 上遇 EPIPE 会**直接终止**。
// 现象是"日志显示已启动，十几秒后进程凭空消失"，且不留下任何错误信息。
//
// 这是最难排查的一类缺陷，因此用源码断言把它钉死。
func TestNoPipeWriterForCloudflared(t *testing.T) {
	data, err := os.ReadFile("manager.go")
	if err != nil {
		t.Skipf("无法读取 manager.go: %v", err)
	}
	src := string(data)

	if strings.Contains(src, "logutil.Writer{") {
		t.Error("manager.go 又把 logutil.Writer 赋给了子进程 stdout —— " +
			"这会让 os/exec 建管道，CLI 退出后 cloudflared 会因 EPIPE 被终止")
	}
	if !strings.Contains(src, "openCloudflaredLog") {
		t.Error("manager.go 应通过 openCloudflaredLog() 以文件方式重定向子进程输出")
	}
	// 打开的文件不应被 defer 到函数返回后才关闭——那样父进程整个生命周期
	// 都持有写端，长期驻留的 Web 进程反复启停隧道会泄漏句柄。
	if strings.Contains(src, "defer logFile.Close()") {
		t.Error("日志文件不应 defer 到函数返回：cmd.Start() 之后应立刻关闭父进程侧句柄")
	}
}

// TestOpenCloudflaredLogAppends 日志文件必须为追加模式，不能截断已有内容。
func TestOpenCloudflaredLogAppends(t *testing.T) {
	if err := config.Ensure(); err != nil {
		t.Fatalf("准备目录失败: %v", err)
	}
	logPath := filepath.Join(config.LogDir(), "cftunnelX.log")
	original, err := os.ReadFile(logPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	marker := "OPENCLOUDFLAREDLOG-APPEND-TEST"
	if err := os.WriteFile(logPath, append(original, []byte(marker+"\n")...), 0644); err != nil {
		t.Fatal(err)
	}

	f, err := openCloudflaredLog()
	if err != nil {
		t.Fatalf("打开日志失败: %v", err)
	}
	if _, err := f.WriteString("child-output-line\n"); err != nil {
		f.Close()
		t.Fatalf("写入失败: %v", err)
	}
	f.Close()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), marker) {
		t.Error("原有日志内容被截断——必须使用 O_APPEND")
	}
	if !strings.Contains(string(data), "child-output-line") {
		t.Error("子进程输出未写入日志")
	}
}

// TestWaitTunnelStableAlive 进程存活时应返回 nil。
func TestWaitTunnelStableAlive(t *testing.T) {
	if err := config.Ensure(); err != nil {
		t.Fatal(err)
	}
	id := "stable-alive-test"
	p := tunnelPIDPath(id)
	if err := os.WriteFile(p, []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(p) })

	if err := WaitTunnelStable(id, 500*time.Millisecond); err != nil {
		t.Errorf("进程存活时不应报错: %v", err)
	}
}

// TestWaitTunnelStableDead 进程已消失时必须报错，并把日志尾部带给用户。
func TestWaitTunnelStableDead(t *testing.T) {
	if err := config.Ensure(); err != nil {
		t.Fatal(err)
	}
	id := "stable-dead-test"
	p := tunnelPIDPath(id)
	// 一个几乎不可能存在的 PID
	if err := os.WriteFile(p, []byte("4194303"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(p) })

	err := WaitTunnelStable(id, 400*time.Millisecond)
	if err == nil {
		t.Fatal("进程已退出时应当返回错误")
	}
	if !strings.Contains(err.Error(), "已退出") {
		t.Errorf("错误信息应说明进程已退出, 实际: %v", err)
	}
	// 不能只报"失败了"——必须把 cloudflared 的输出带给用户，否则无从下手
	if !strings.Contains(err.Error(), "cloudflared 输出") {
		t.Errorf("错误信息应附带 cloudflared 输出, 实际: %v", err)
	}
}

// TestTailLogLines 日志尾部摘取不应 panic，且行数受控。
func TestTailLogLines(t *testing.T) {
	if err := config.Ensure(); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(config.LogDir(), "cftunnelX.log")
	var sb strings.Builder
	for i := 1; i <= 50; i++ {
		sb.WriteString("line-" + strconv.Itoa(i) + "\n")
	}
	if err := os.WriteFile(logPath, []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}

	got := tailLogLines(5)
	if !strings.Contains(got, "line-50") {
		t.Errorf("应包含最后一行, 实际:\n%s", got)
	}
	if strings.Contains(got, "line-44") {
		t.Errorf("应只取最后 5 行, 却包含了 line-44:\n%s", got)
	}
	if n := strings.Count(got, "line-"); n > 5 {
		t.Errorf("行数应受控为 5, 实际 %d", n)
	}
}
