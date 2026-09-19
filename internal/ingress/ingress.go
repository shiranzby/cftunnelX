// Package ingress 是 ingress 规则的唯一生成入口。
//
// 存在这个包的原因：此前 cmd/ 与 internal/web/ 各自实现了 9 处
// 「构造 rules → 调 PushIngressConfig」，其中只有 CLI 的 up 路径给鉴权路由
// 做了代理端口映射，Web 面板路径直接推源站端口。两条路径互相覆盖的结果是：
//
//	用 CLI 启动  → ingress 指向代理端口，但 CLI 一退出代理就死 → 502
//	用面板启动  → ingress 指回源站端口，代理根本没起 → 200 且不要密码
//
// 后者是**认证被静默绕过**的安全缺陷。要根治就必须让 ingress 只有一处生成，
// 并在这里统一应用「启用鉴权的路由必须指向鉴权代理」这条规则。
package ingress

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shiranzby/cftunnelX/internal/cfapi"
	"github.com/shiranzby/cftunnelX/internal/config"
)

// Rule 一条 ingress 规则
type Rule struct {
	Hostname string `json:"hostname"`
	Service  string `json:"service"`
}

// Options 生成 ingress 规则时的选项。
type Options struct {
	// BypassAuth 为 true 时，即使路由启用了鉴权也直接指向源站端口，
	// 即**认证不生效**。仅供排障使用，正常路径绝不应打开。
	//
	// 做成显式选项而不是隐式默认：曾经就是因为"忘了做端口映射"而让
	// 认证被静默绕过——那种问题没有任何报错，只有真正 curl 一下才会发现。
	BypassAuth bool
}

// AuthService 返回某条鉴权路由对应代理的 service 地址。
func AuthService(r config.RouteConfig) string {
	return "http://127.0.0.1:" + strconv.Itoa(r.AuthProxyPort())
}

// Rules 计算某条隧道要推送到 Cloudflare 的 ingress 规则（不绕过鉴权）。
func Rules(t *config.TunnelConfig) []Rule {
	return RulesWith(t, Options{})
}

// RulesWith 计算 ingress 规则，可选择绕过鉴权。
//
// 这是全项目唯一允许生成 ingress 规则的地方：
// 启用鉴权的路由一律指向本地鉴权代理端口，而不是源站端口。
func RulesWith(t *config.TunnelConfig, opts Options) []Rule {
	if t == nil {
		return nil
	}
	routes := t.IngressRoutes()
	if opts.BypassAuth {
		// 排障路径：退回原始路由，鉴权不生效
		routes = t.Routes
	}
	rules := make([]Rule, 0, len(routes))
	for _, r := range routes {
		if strings.TrimSpace(r.Hostname) == "" {
			continue
		}
		rules = append(rules, Rule{Hostname: r.Hostname, Service: r.Service})
	}
	return rules
}

// Push 把某条隧道的 ingress 推送到 Cloudflare（不绕过鉴权）。
func Push(ctx context.Context, cfg *config.Config, tunnelID string) error {
	return PushWith(ctx, cfg, tunnelID, Options{})
}

// PushWith 把某条隧道的 ingress 推送到 Cloudflare。
// tunnelID 为空时使用当前活动隧道。
func PushWith(ctx context.Context, cfg *config.Config, tunnelID string, opts Options) error {
	if cfg == nil {
		return fmt.Errorf("配置为空")
	}
	var tunnel *config.TunnelConfig
	if tunnelID != "" {
		tunnel = cfg.FindTunnel(tunnelID)
	}
	if tunnel == nil {
		tunnel = cfg.ActiveTunnel()
	}
	if tunnel == nil {
		return fmt.Errorf("未配置隧道")
	}
	rules := RulesWith(tunnel, opts)
	if len(rules) == 0 {
		return nil
	}
	client := cfapi.New(cfg.Auth.APIToken, cfg.Auth.AccountID)
	cfRules := make([]cfapi.IngressRule, 0, len(rules))
	for _, r := range rules {
		cfRules = append(cfRules, cfapi.IngressRule{Hostname: r.Hostname, Service: r.Service})
	}
	return client.PushIngressConfig(ctx, tunnel.ID, cfRules)
}

// MissingProxy 描述一条「启用了鉴权但代理未监听」的路由
type MissingProxy struct {
	RouteName string
	Hostname  string
	Port      int
}

func (m MissingProxy) String() string {
	if m.Port <= 0 {
		return fmt.Sprintf("%s (%s) 端口无法推导，请检查 service 格式", m.RouteName, m.Hostname)
	}
	return fmt.Sprintf("%s (%s) 期望端口 %d 未监听", m.RouteName, m.Hostname, m.Port)
}

// CheckAuthProxies 返回所有「启用了鉴权但代理尚未监听」的路由。
//
// 调用方在推送 ingress 之前必须先确保这里为空，否则推出去的规则要么指向
// 一个没人监听的端口（502），要么退化成源站端口（认证被绕过）。
func CheckAuthProxies(cfg *config.Config) []MissingProxy {
	if cfg == nil {
		return nil
	}
	var missing []MissingProxy
	checked := map[int]bool{}
	for _, r := range cfg.AllRoutes() {
		if !r.AuthEnabled() {
			continue
		}
		port := r.AuthProxyPort()
		if port <= 0 {
			missing = append(missing, MissingProxy{r.Name, r.Hostname, 0})
			continue
		}
		if checked[port] {
			continue
		}
		checked[port] = true
		if !PortListening(port) {
			missing = append(missing, MissingProxy{r.Name, r.Hostname, port})
		}
	}
	return missing
}

// ProxyPorts 返回配置中所有鉴权路由需要监听的端口（去重、升序）。
func ProxyPorts(cfg *config.Config) []int {
	if cfg == nil {
		return nil
	}
	set := map[int]bool{}
	for _, r := range cfg.AllRoutes() {
		if r.AuthEnabled() {
			if p := r.AuthProxyPort(); p > 0 {
				set[p] = true
			}
		}
	}
	ports := make([]int, 0, len(set))
	for p := range set {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	return ports
}

// PortListening 判断本机端口是否已处于监听状态。
func PortListening(port int) bool {
	if port <= 0 {
		return false
	}
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 800*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// AllListening 判断所有给定端口是否都已监听（空集合视为 true）。
func AllListening(ports []int) bool {
	for _, p := range ports {
		if !PortListening(p) {
			return false
		}
	}
	return true
}

// WaitPortsListening 等待所有端口进入监听状态，超时返回错误。
func WaitPortsListening(ports []int, timeout time.Duration) error {
	if len(ports) == 0 {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for {
		allUp := true
		for _, p := range ports {
			if !PortListening(p) {
				allUp = false
				break
			}
		}
		if allUp {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("等待鉴权代理端口就绪超时（%v）", ports)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
