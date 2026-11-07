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
	"sort"
	"strconv"
	"strings"

	"github.com/shiranzby/cftunnelX/internal/authproxy"
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

// BuildRules 计算要推送到 Cloudflare 的规则，并在返回前确认**鉴权代理真实就绪**。
//
// 这是所有推送路径应当使用的唯一入口。改动前，有 9 处代码各自
// 「遍历 IngressRoutes() → 拼规则 → PushIngressConfig」，全程不检查代理状态，
// 由此产生缺陷 CFX-AUTHPROXY-INGRESS-001：
//
//	路由配了 auth 但未写 listen_port → 端口推导为 service+1
//	该端口恰好被另一个本地服务占用   → 代理精确绑定失败、从未启动
//	推送方仍按配置把 service 改写成   127.0.0.1:<那个端口>
//	→ 公网流量被导向无关服务，且完全不经认证
//
// 采用 fail-closed：只要有一条鉴权路由的代理没就绪，就整体返回错误、一条都不推。
// 刻意不做「降级为源站端口」的兜底 —— 那等于静默关掉认证，比 502 危险得多。
func BuildRules(t *config.TunnelConfig) ([]cfapi.IngressRule, error) {
	if t == nil {
		return nil, fmt.Errorf("隧道为空")
	}
	if err := checkRoutesReady(t.Routes); err != nil {
		return nil, err
	}
	return rulesFrom(t.IngressRoutes()), nil
}

// BuildRulesForConfig 与 BuildRules 相同，但覆盖配置里的所有隧道路由。
// 供 CLI 的单隧道兼容路径使用（它按整份配置生成规则）。
func BuildRulesForConfig(cfg *config.Config) ([]cfapi.IngressRule, error) {
	if cfg == nil {
		return nil, fmt.Errorf("配置为空")
	}
	if err := checkRoutesReady(cfg.AllRoutes()); err != nil {
		return nil, err
	}
	return rulesFrom(cfg.IngressRoutes()), nil
}

// checkRoutesReady 确认所有启用鉴权的路由，其代理在本机**确实是我们的进程**。
//
// 判据用 authproxy.Probe（带身份校验），不能用"端口有没有人在监听"：
// 默认端口取 service+1，很可能被别的本地服务占用，只看监听会把它当成
// 自己的代理，从而把公网流量导过去。
func checkRoutesReady(routes []config.RouteConfig) error {
	var missing []string
	checked := map[int]bool{}
	for _, r := range routes {
		if !r.AuthEnabled() {
			continue
		}
		port := r.AuthProxyPort()
		if port <= 0 {
			missing = append(missing,
				fmt.Sprintf("%s (%s) 无法推导鉴权代理端口", r.Name, r.Hostname))
			continue
		}
		if checked[port] {
			continue
		}
		checked[port] = true
		if !authproxy.Probe(port) {
			missing = append(missing,
				fmt.Sprintf("%s (%s) 端口 %d 未就绪或已被其它进程占用", r.Name, r.Hostname, port))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf(
			"鉴权代理未就绪，已拒绝推送 ingress（否则公网流量会被导向错误端口并绕过认证）：%s",
			strings.Join(missing, "; "))
	}
	return nil
}

// rulesFrom 把路由视图转换成可推送的规则（跳过没有 hostname 的项）。
func rulesFrom(routes []config.RouteConfig) []cfapi.IngressRule {
	rules := make([]cfapi.IngressRule, 0, len(routes))
	for _, r := range routes {
		if strings.TrimSpace(r.Hostname) == "" {
			continue
		}
		rules = append(rules, cfapi.IngressRule{Hostname: r.Hostname, Service: r.Service})
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
	var cfRules []cfapi.IngressRule
	if opts.BypassAuth {
		// 排障路径：直接用源站端口，鉴权不生效。显式选择才走这里。
		cfRules = make([]cfapi.IngressRule, 0, len(tunnel.Routes))
		for _, r := range tunnel.Routes {
			if strings.TrimSpace(r.Hostname) == "" {
				continue
			}
			cfRules = append(cfRules, cfapi.IngressRule{Hostname: r.Hostname, Service: r.Service})
		}
	} else {
		// 正常路径：代理未就绪时直接失败，绝不推出一份会绕过认证的规则
		var err error
		cfRules, err = BuildRules(tunnel)
		if err != nil {
			return err
		}
	}
	if len(cfRules) == 0 {
		return nil
	}

	client := cfapi.New(cfg.Auth.APIToken, cfg.Auth.AccountID)
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
	return fmt.Sprintf("%s (%s) 端口 %d 未就绪（未监听，或已被其它进程占用）", m.RouteName, m.Hostname, m.Port)
}

// CheckAuthProxies 返回所有「启用了鉴权但代理尚未就绪」的路由。
//
// 调用方在推送 ingress 之前必须先确保这里为空，否则推出去的规则要么指向
// 一个没人监听的端口（502），要么指向**被其它本地服务占用**的端口
// （公网流量被导到无关服务，且完全不经认证 —— 这是更危险的一种）。
//
// 判据是 authproxy.Probe（带身份校验），不是"端口有没有人在监听"：
// 后者会被任何一种占用该端口的服务骗过。
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
		if !authproxy.Probe(port) {
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

// 🔴 关于「端口就绪」的判断 —— 这里曾有三个函数，已移除，原因值得记住：
//
//	PortListening / AllListening / WaitPortsListening 的判据是
//	「端口上有没有人在监听」。在鉴权代理场景下这个判据是**错的**：
//	代理端口默认取「源站端口 + 1」，很容易被别的本地服务占用，
//	此时"有人在监听"恰恰意味着最危险的情况 —— 一旦据此判定"代理已就绪"，
//	就会把云端 ingress 指向那个别人的端口，公网流量被导向无关服务
//	且完全不经认证（缺陷 CFX-AUTHPROXY-INGRESS-001 的实际事故）。
//
// 现在统一改用 authproxy.Probe：向 /.cftunnelx/authproxy 发一次请求，
// 校验响应里的身份标记，只有本程序会这样应答。
//
// 同理，authproxy.FindAvailableListener 只适用于「代理生命周期与当前进程一致」
// 的场景（免域名模式，端口不写进远端 ingress）；需要精确绑定端口时必须用
// NewOnPort —— 它会冲突即报错，而不是就近换一个端口。
