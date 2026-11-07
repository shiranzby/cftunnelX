package daemon

import (
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shiranzby/cftunnelX/internal/authproxy"
	"github.com/shiranzby/cftunnelX/internal/config"
)

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

// cfgWithAuthRoute 构造一份含单条鉴权路由的配置，代理端口显式指定。
func cfgWithAuthRoute(proxyPort int) *config.Config {
	return &config.Config{
		Tunnels: []config.TunnelConfig{{
			ID: "t1", Name: "t", Token: "tok",
			Routes: []config.RouteConfig{{
				Name: "pcpanel", Hostname: "root.example.com",
				Service: "http://localhost:5250",
				Auth: &config.AuthProxy{
					Username: "u", Password: "p", ListenPort: proxyPort,
				},
			}},
		}},
	}
}

// TestStartAuthProxiesReportsPortConflict 复刻 CFX-AUTHPROXY-INGRESS-001 的触发条件：
// 鉴权代理端口被别的本地服务占用。
//
// 旧实现用 ingress.AllListening 判断"端口在监听 = 就绪"，于是直接返回 nil，
// 上层认为代理已就绪并继续推送 ingress —— 代理其实从未启动。
// 现在必须返回**明确的错误**，且错误里带端口号，便于用户直接处理。
func TestStartAuthProxiesReportsPortConflict(t *testing.T) {
	port := freePort(t)

	// 模拟"别的本地服务"占住这个端口（不做 HTTP 应答，只监听即可）
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatalf("占用端口失败: %v", err)
	}
	defer func() { _ = ln.Close() }()

	started, err := StartAuthProxies(cfgWithAuthRoute(port))
	if err == nil {
		t.Fatal("端口被外部占用时必须返回错误，不能当作已就绪")
	}
	if started {
		t.Fatal("失败时不应报告\"已启动\"")
	}
	if !strings.Contains(err.Error(), strconv.Itoa(port)) {
		t.Fatalf("错误信息应包含端口号 %d，实际: %v", port, err)
	}
}

// TestStartAuthProxiesRecognizesExistingProxy 端口上确实是我们自己的代理时，
// 应视为已就绪且不重复拉起。
func TestStartAuthProxiesRecognizesExistingProxy(t *testing.T) {
	port := freePort(t)

	p, err := authproxy.NewOnPort(authproxy.Config{
		Username: "u", Password: "p", TargetPort: "9", CookieTTL: time.Hour,
	}, port)
	if err != nil {
		t.Fatalf("创建鉴权代理失败: %v", err)
	}
	if err := p.Start(); err != nil {
		t.Fatalf("启动鉴权代理失败: %v", err)
	}
	defer func() { _ = p.Stop() }()

	// 等它真正可探活
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !authproxy.Probe(port) {
		time.Sleep(20 * time.Millisecond)
	}
	if !authproxy.Probe(port) {
		t.Fatalf("测试前置失败：端口 %d 上的代理未就绪", port)
	}

	started, err := StartAuthProxies(cfgWithAuthRoute(port))
	if err != nil {
		t.Fatalf("已就绪时不应报错: %v", err)
	}
	if started {
		t.Fatal("已就绪时不应报告\"新拉起了进程\"")
	}
}

// TestStartAuthProxiesNoAuthRoutes 没有鉴权路由时应直接返回，不做任何事。
func TestStartAuthProxiesNoAuthRoutes(t *testing.T) {
	cfg := &config.Config{
		Tunnels: []config.TunnelConfig{{
			ID: "t1", Name: "t",
			Routes: []config.RouteConfig{{
				Name: "plain", Hostname: "a.example.com", Service: "http://localhost:3000",
			}},
		}},
	}
	started, err := StartAuthProxies(cfg)
	if err != nil || started {
		t.Fatalf("无鉴权路由时应 (false, nil)，实际 (%v, %v)", started, err)
	}
}
