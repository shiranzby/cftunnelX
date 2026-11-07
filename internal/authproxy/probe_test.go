package authproxy

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// freePort 取一个当前空闲的本地端口（拿完立即释放）。
//
// 测试里存在极小的 TOCTOU 窗口，但同机并行测试抢到同一端口的概率极低，
// 且失败时表现为 Listen 报错、用例直接失败，不会静默通过。
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("取空闲端口失败: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func waitPort(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("端口 %d 未在预期时间内进入监听", port)
}

func portOfURL(t *testing.T, rawURL string) int {
	t.Helper()
	_, portStr, err := net.SplitHostPort(rawURL[len("http://"):])
	if err != nil {
		t.Fatalf("解析测试服务地址失败: %v", err)
	}
	p, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("解析测试服务端口失败: %v", err)
	}
	return p
}

// TestProbeDetectsOurProxy 确认能认出真正的鉴权代理。
func TestProbeDetectsOurProxy(t *testing.T) {
	port := freePort(t)
	p, err := NewOnPort(Config{
		Username: "u", Password: "p", TargetPort: "9", CookieTTL: time.Hour,
	}, port)
	if err != nil {
		t.Fatalf("创建代理失败: %v", err)
	}
	if err := p.Start(); err != nil {
		t.Fatalf("启动代理失败: %v", err)
	}
	defer func() { _ = p.Stop() }()

	waitPort(t, port)
	if !Probe(port) {
		t.Fatal("真实的鉴权代理应被判为就绪")
	}
}

// TestProbeRejectsForeignListener 是本次事故的核心回归测试。
//
// 缺陷场景：代理端口默认取「源站端口 + 1」，该端口被另一个本地服务占用。
// 旧实现用「端口有没有人在监听」判断就绪 → 误判为就绪 → 代理从未启动
// → 云端 ingress 被指向那个端口 → 公网流量被导到无关服务且不经认证。
func TestProbeRejectsForeignListener(t *testing.T) {
	port := freePort(t)
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatalf("占用端口失败: %v", err)
	}
	defer func() { _ = ln.Close() }()

	// 模拟"别的服务"：对任意路径都返回 200，最容易被误判的情况
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("some other service"))
	})}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	waitPort(t, port)
	if Probe(port) {
		t.Fatal("端口被其它服务占用时不得判为就绪（否则会把 ingress 指向别人的端口）")
	}
}

// TestProbeRejectsClosedPort 端口没人监听时应判未就绪。
func TestProbeRejectsClosedPort(t *testing.T) {
	if Probe(freePort(t)) {
		t.Fatal("空端口不应被判为就绪")
	}
}

// TestProbeRejectsImitatedBody 仅状态码为 200 不够，必须匹配身份标记。
func TestProbeRejectsImitatedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"service":"another-tool"}`))
	}))
	defer srv.Close()

	if Probe(portOfURL(t, srv.URL)) {
		t.Fatal("响应内容不是本代理的标记，不应判为就绪")
	}
}

// TestProbeAcceptsMarker 身份标记匹配时判为就绪（确保上面几条不是恒假）。
func TestProbeAcceptsMarker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != HealthPath {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(healthMarker + `,"port":1}`))
	}))
	defer srv.Close()

	if !Probe(portOfURL(t, srv.URL)) {
		t.Fatal("身份标记匹配时应判为就绪")
	}
}

// TestProbePortsReturnsMissing 确认批量探活只返回未就绪的端口。
func TestProbePortsReturnsMissing(t *testing.T) {
	okPort := freePort(t)
	p, err := NewOnPort(Config{
		Username: "u", Password: "p", TargetPort: "9", CookieTTL: time.Hour,
	}, okPort)
	if err != nil {
		t.Fatalf("创建代理失败: %v", err)
	}
	if err := p.Start(); err != nil {
		t.Fatalf("启动代理失败: %v", err)
	}
	defer func() { _ = p.Stop() }()
	waitPort(t, okPort)

	badPort := freePort(t)
	missing := ProbePorts([]int{okPort, badPort})
	if len(missing) != 1 || missing[0] != badPort {
		t.Fatalf("missing = %v，期望仅 [%d]", missing, badPort)
	}
}
