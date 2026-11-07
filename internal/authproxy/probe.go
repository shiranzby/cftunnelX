package authproxy

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// HealthPath 是鉴权代理**自己应答**的探活端点。
//
// 为什么需要它：判断"鉴权代理是否就绪"不能只看端口有没有人在监听。
// 代理端口默认取「源站端口 + 1」，在真实机器上极易被其它本地服务占用
// （本次事故里 5251 被网心云监控占用）。只看监听会把**别人的服务**当成
// 自己的代理，进而把云端 ingress 指向那个端口 —— 公网流量被导向无关服务，
// 而且完全不经认证。
//
// 因此这里用一个只有本程序会应答的路径来做身份校验：返回的 JSON 里带固定
// 标记，其它服务不可能恰好返回同样的内容。
const HealthPath = "/.cftunnelx/authproxy"

// healthMarker 用于确认响应确实来自本代理。
const healthMarker = `{"service":"cftunnelx-authproxy"`

// ProxyHeader 探活响应里附带的标识头，便于人工 curl 核对。
const ProxyHeader = "X-Cftunnelx-Proxy"

// ProbeTimeout 单次探活超时。本机回环，几百毫秒足够。
const ProbeTimeout = 800 * time.Millisecond

// Probe 判断指定端口上跑的**是不是我们的鉴权代理**。
//
// 返回 false 可能是三种情况，调用方一律按"未就绪"处理：
//   - 端口没人监听
//   - 端口被其它进程监听（这就是必须防住的那一种）
//   - 端口是我们的代理但已经卡死、来不及应答
func Probe(port int) bool {
	return ProbeWithTimeout(port, ProbeTimeout)
}

// ProbeWithTimeout 是带自定义超时的 Probe。
func ProbeWithTimeout(port int, timeout time.Duration) bool {
	if port <= 0 || port > 65535 {
		return false
	}
	if timeout <= 0 {
		timeout = ProbeTimeout
	}

	client := &http.Client{
		Timeout: timeout,
		// 不跟随重定向：代理对未认证请求会 302 到登录页，
		// 跟随会让"另一个恰好会跳转的服务"更容易蒙混过关。
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + HealthPath)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	return strings.HasPrefix(string(body), healthMarker)
}

// ProbePorts 返回其中**未就绪**的端口（空切片表示全部就绪）。
func ProbePorts(ports []int) []int {
	var missing []int
	for _, p := range ports {
		if !Probe(p) {
			missing = append(missing, p)
		}
	}
	return missing
}

// WaitReady 等待所有端口就绪（**以身份探活为准**，不是以端口监听为准）。
func WaitReady(ports []int, timeout time.Duration) error {
	if len(ports) == 0 {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for {
		if len(ProbePorts(ports)) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("等待鉴权代理就绪超时（%v）：端口未监听或被其它进程占用，可用 curl http://127.0.0.1:<端口>%s 核对", ports, HealthPath)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// PortAvailable 判断端口当前能否被本进程监听，用于启动前的冲突预检。
//
// 注意：本函数会真的 Listen 再立即关闭，存在极小的 TOCTOU 窗口；
// 它只用于「给出确定的错误信息」，真正的绑定仍由 NewOnPort 精确完成。
func PortAvailable(port int) bool {
	if port <= 0 || port > 65535 {
		return false
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}
