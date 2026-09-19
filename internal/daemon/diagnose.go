package daemon

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/shiranzby/cftunnelX/internal/cfapi"
)

const diagnoseTimeout = 8 * time.Second

// DiagnoseResult 诊断总结果
type DiagnoseResult struct {
	Environment EnvironmentCheck `json:"environment"`
	Cloudflared CloudflaredCheck `json:"cloudflared"`
	API         APICheck         `json:"api"`
	Routes      []RouteDiagnose  `json:"routes"`
	Total       int              `json:"total"`
	Passed      int              `json:"passed"`
	Failed      int              `json:"failed"`
}

// EnvironmentCheck 运行环境前置检查。
// 缺少根证书会让所有 HTTPS 请求以证书校验失败告终，表现为"链路不可达"，
// 因此在诊断里单独列出。
type EnvironmentCheck struct {
	CAOK         bool   `json:"ca_ok"`
	CAPath       string `json:"ca_path,omitempty"`
	CAHint       string `json:"ca_hint,omitempty"`
	OutboundOK   bool   `json:"outbound_ok"`
	OutboundHint string `json:"outbound_hint,omitempty"`
}

// CloudflaredCheck cloudflared 二进制和进程检测
type CloudflaredCheck struct {
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
	Running   bool   `json:"running"`
	PID       int    `json:"pid,omitempty"`
	Scope     string `json:"scope,omitempty"`    // tunnel:<id> / legacy / quick
	PidFile   string `json:"pid_file,omitempty"` // 实际读到 PID 的文件（排障用）
	Embedded  bool   `json:"embedded"`           // 本二进制是否内嵌了 cloudflared
	Hint      string `json:"hint,omitempty"`
}

// APICheck Cloudflare API 连通性与凭据校验（分层结果）
type APICheck struct {
	Reachable      bool   `json:"reachable"`
	LatencyMS      int64  `json:"latency_ms,omitempty"`
	Status         int    `json:"status,omitempty"`
	Err            string `json:"err,omitempty"`
	Hint           string `json:"hint,omitempty"`
	AccountOK      bool   `json:"account_ok"`
	AccountMessage string `json:"account_message,omitempty"`
	ZonesOK        bool   `json:"zones_ok"`
	ZoneCount      int    `json:"zone_count,omitempty"`
}

// RouteDiagnose 单条路由诊断
type RouteDiagnose struct {
	Name       string `json:"name"`
	Hostname   string `json:"hostname"`
	Service    string `json:"service"`
	LocalOK    bool   `json:"local_ok"`
	LocalErr   string `json:"local_err,omitempty"`
	LocalHint  string `json:"local_hint,omitempty"`
	DNSOK      bool   `json:"dns_ok"`
	DNSErr     string `json:"dns_err,omitempty"`
	DNSHint    string `json:"dns_hint,omitempty"`
	HTTPOK     bool   `json:"http_ok"`
	HTTPStatus int    `json:"http_status,omitempty"`
	HTTPErr    string `json:"http_err,omitempty"`
	HTTPHint   string `json:"http_hint,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

// RouteInput 诊断输入
type RouteInput struct {
	Name     string
	Hostname string
	Service  string
}

// Diagnose 执行 Cloud 模式链路诊断。
// apiToken / accountID 用于分层校验凭据（此前只传网络层检测，无法区分权限问题）。
func Diagnose(routes []RouteInput, apiToken, accountID string) DiagnoseResult {
	var result DiagnoseResult

	result.Environment = checkEnvironment()
	result.Cloudflared = checkCloudflared()
	result.API = checkAPI(apiToken, accountID)

	result.Routes = make([]RouteDiagnose, len(routes))
	var wg sync.WaitGroup
	for i, r := range routes {
		wg.Add(1)
		go func(idx int, route RouteInput) {
			defer wg.Done()
			result.Routes[idx] = diagnoseRoute(route)
		}(i, r)
	}
	wg.Wait()

	result.Total = len(result.Routes)
	for _, r := range result.Routes {
		// 统计口径与界面展示保持一致：本地服务 + DNS + HTTPS 三者都通过才算通
		if r.LocalOK && r.DNSOK && (r.Hostname == "" || r.HTTPOK) {
			result.Passed++
		} else {
			result.Failed++
		}
	}
	return result
}

// ---- 环境前置检查 ----

// caBundlePaths 常见发行版的根证书位置
var caBundlePaths = []string{
	"/etc/ssl/certs/ca-certificates.crt", // Debian / Ubuntu
	"/etc/pki/tls/certs/ca-bundle.crt",   // RHEL / CentOS
	"/etc/ssl/ca-bundle.pem",             // openSUSE
	"/etc/ssl/cert.pem",                  // Alpine
}

// CheckCACertificates 检查系统根证书是否就绪。
//
// 缺失根证书时 Go 的所有 HTTPS 请求都会以 "x509: certificate signed by
// unknown authority" 失败 —— 这是"填了正确的 API 却始终链路不可达"的常见原因，
// 尤其在精简的 WSL / 容器镜像里。
func CheckCACertificates() (ok bool, path string, hint string) {
	if runtime.GOOS == "windows" {
		return true, "windows 系统证书库", ""
	}
	for _, p := range caBundlePaths {
		if info, err := os.Stat(p); err == nil && info.Size() > 0 {
			return true, p, ""
		}
	}
	return false, "", "系统根证书缺失，HTTPS 无法完成校验。" +
		"Debian/Ubuntu: apt-get install -y ca-certificates；" +
		"OpenWrt: opkg install ca-bundle；Alpine: apk add ca-certificates"
}

// CheckEnvironmentForAPI 返回便于 WebUI 直接序列化的运行环境检查结果。
func CheckEnvironmentForAPI() map[string]interface{} {
	env := checkEnvironment()
	return map[string]interface{}{
		"ca_ok":         env.CAOK,
		"ca_path":       env.CAPath,
		"ca_hint":       env.CAHint,
		"outbound_ok":   env.OutboundOK,
		"outbound_hint": env.OutboundHint,
	}
}

func checkEnvironment() EnvironmentCheck {
	ok, path, hint := CheckCACertificates()
	env := EnvironmentCheck{CAOK: ok, CAPath: path, CAHint: hint}

	// 到 Cloudflare API 的出方向连通性
	client := &http.Client{Timeout: 6 * time.Second}
	start := time.Now()
	resp, err := client.Get("https://api.cloudflare.com/client/v4/")
	if err == nil {
		resp.Body.Close()
		env.OutboundOK = true
		env.OutboundHint = fmt.Sprintf("出方向到 api.cloudflare.com 正常（%dms）", time.Since(start).Milliseconds())
		return env
	}
	env.OutboundHint = "无法连接 api.cloudflare.com：" + classifyHTTPSError(err)
	return env
}

// checkCloudflared 检测 cloudflared 与隧道进程状态
func checkCloudflared() CloudflaredCheck {
	var c CloudflaredCheck
	c.Embedded = len(cloudflaredAsset) > 0
	path, err := EnsureCloudflared()
	if err != nil {
		c.Hint = "cloudflared 未安装且自动获取失败：" + err.Error()
		return c
	}
	c.Installed = true
	c.Path = path

	if out, vErr := exec.Command(path, "version").CombinedOutput(); vErr == nil {
		c.Version = strings.TrimSpace(string(out))
	}

	// 用 AnyRunning 汇总全部 PID 文件形态（多隧道 / 旧版 / 免域名）。
	// 旧实现只读 cloudflared.pid，而 WebUI 启动隧道写的是
	// cloudflared-<隧道ID>.pid，于是诊断显示"未运行"、/api/status 显示
	// "运行中"，两个接口自相矛盾。
	inst := AnyRunning()
	c.Running = inst.Running
	c.PID = inst.PID
	c.Scope = inst.Scope
	c.PidFile = inst.PidFile
	if inst.Running {
		c.Hint = inst.Hint() + "。若域名仍不可达，请确认 cloudflared 日志中出现 Registered tunnel connection"
	} else {
		c.Hint = "cloudflared 已安装，但未检测到运行中的进程" +
			"（已检查多隧道、旧版单隧道、免域名三种 PID 文件）。" +
			"仅填写 API/账号不会启动隧道，请执行 `cftunnelX up` 或在 Web 面板点击「启动隧道」；" +
			"服务器长期运行可执行 `cftunnelX install` 注册为开机自启服务"
	}
	return c
}

// checkAPI 校验 Cloudflare API 凭据（分层：Token → Account → Zone 权限）。
//
// 旧实现没有携带任何凭据、也不看响应状态码，只要 TCP/TLS 能通就报"可达"，
// 无法区分"网络通"与"Token 有效"；后来又只用 Zones.List 一个接口来判断，
// 于是"Token 有效但缺 Zone Read 权限"会被误报成"Token 无效"。
func checkAPI(apiToken, accountID string) APICheck {
	var a APICheck
	if strings.TrimSpace(apiToken) == "" {
		a.Err = "API Token 未配置"
		a.Hint = "请在「基本配置」中填写 API Token 与 Account ID 后保存"
		return a
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	d := cfapi.New(apiToken, accountID).DiagnoseCredentials(ctx)

	a.Reachable = d.Reachable()
	a.LatencyMS = d.LatencyMS
	a.Status = d.TokenStatus
	a.Hint = d.Hint
	a.AccountOK = d.AccountOK
	a.AccountMessage = d.AccountMessage
	a.ZonesOK = d.ZonesOK
	a.ZoneCount = d.ZoneCount
	if !d.Reachable() {
		a.Err = d.TokenMessage
	}
	return a
}

func diagnoseRoute(r RouteInput) RouteDiagnose {
	d := RouteDiagnose{
		Name:     r.Name,
		Hostname: r.Hostname,
		Service:  r.Service,
	}

	port := extractPort(r.Service)
	if port != "" {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, diagnoseTimeout)
		if err == nil {
			conn.Close()
			d.LocalOK = true
			d.LocalHint = "本地服务 127.0.0.1:" + port + " 正常监听"
		} else {
			d.LocalErr = "未监听"
			d.LocalHint = "本地端口 " + port + " 未监听，请确认目标服务已启动并监听该端口"
		}
	} else {
		d.LocalErr = "无法解析端口"
		d.LocalHint = "Service 格式应为 http://localhost:端口"
	}

	if r.Hostname != "" {
		ips, err := net.LookupHost(r.Hostname)
		if err == nil {
			d.DNSOK = true
			if len(ips) > 0 {
				d.DNSHint = "DNS 解析正常，IP: " + ips[0]
			}
		} else {
			d.DNSErr = "解析失败"
			d.DNSHint = "DNS 解析失败，请确认 CNAME 已创建且为「已代理」状态，并等待传播"
		}
	}

	if r.Hostname != "" && d.DNSOK {
		p := ProbeHTTPS(r.Hostname, diagnoseTimeout)
		d.HTTPOK = p.OK
		d.HTTPStatus = p.Status
		d.HTTPErr = p.Err
		d.HTTPHint = p.Hint
	}

	return d
}

// httpsProbe HTTPS 探测结果
type httpsProbe struct {
	OK     bool
	Status int
	Err    string
	Hint   string
}

// ProbeHTTPS 探测 https://hostname 并翻译失败原因。
//
// 旧实现只看 `err == nil`，于是 Cloudflare 返回 530（隧道未连接）这类
// "有响应但不可用"的情况会被误判为可达；而真正的网络/TLS 失败又只显示
// 一个"不可达"，看不到原因。现在区分三类并给出可操作提示。
func ProbeHTTPS(hostname string, timeout time.Duration) httpsProbe {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get("https://" + hostname)
	if err != nil {
		msg := err.Error()
		if len(msg) > 300 {
			msg = msg[:300] + "..."
		}
		return httpsProbe{
			OK:   false,
			Err:  "请求失败",
			Hint: classifyHTTPSError(err) + "（原始错误: " + msg + "）",
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return httpsProbe{
			OK:     true,
			Status: resp.StatusCode,
			Hint:   fmt.Sprintf("HTTPS 可达，状态码 %d", resp.StatusCode),
		}
	}
	return httpsProbe{
		OK:     false,
		Status: resp.StatusCode,
		Err:    fmt.Sprintf("HTTP %d", resp.StatusCode),
		Hint:   describeEdgeStatus(resp.StatusCode),
	}
}

// describeEdgeStatus 把 Cloudflare 边缘的状态码翻译成具体原因。
func describeEdgeStatus(code int) string {
	switch code {
	case 530:
		return "Cloudflare 返回 530：隧道未连接（cloudflared 未运行，或运行了但未能向 Cloudflare 注册连接）。" +
			"请执行 `cftunnelX up` 启动隧道；若已运行，请查看日志中是否有 Registered tunnel connection"
	case 502:
		return "Cloudflare 返回 502：隧道已连接，但无法访问源站。" +
			"通常是路由指向的本地端口未监听，或 Service 地址写错"
	case 404:
		return "Cloudflare 返回 404：该域名在隧道里没有匹配的路由。" +
			"请确认已执行 `cftunnelX add` 且 ingress 已成功推送"
	case 403:
		return "Cloudflare 返回 403：被 Cloudflare Access / WAF / 安全规则拦截"
	case 521, 522, 523, 524:
		return fmt.Sprintf("Cloudflare 返回 %d：回源异常，隧道连接不稳定或已断开", code)
	default:
		return fmt.Sprintf("HTTPS 返回状态码 %d，不属于成功范围", code)
	}
}

// classifyHTTPSError 把网络层错误翻译成可操作的原因。
func classifyHTTPSError(err error) string {
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "certificate") || strings.Contains(s, "x509"):
		return "TLS 证书校验失败：系统根证书缺失或过期。" +
			"Debian/Ubuntu: apt-get install -y ca-certificates；OpenWrt: opkg install ca-bundle"
	case strings.Contains(s, "no such host") || strings.Contains(s, "server misbehaving"):
		return "域名解析失败：DNS 记录可能尚未生效或不是「已代理」状态"
	case strings.Contains(s, "timeout") || strings.Contains(s, "deadline exceeded"):
		return "连接超时：出方向 443 可能被安全组/防火墙拦截，或网络不通"
	case strings.Contains(s, "connection refused"):
		return "连接被拒绝：目标端口未提供服务"
	case strings.Contains(s, "network is unreachable") || strings.Contains(s, "no route to host"):
		return "网络不可达：请检查出方向路由与安全组"
	case strings.Contains(s, "eof"):
		return "连接被对端中断：可能是 TLS 握手被中间设备干扰"
	default:
		return "HTTPS 请求失败"
	}
}

// extractPort 从 service 字符串提取端口号（如 http://localhost:3000 → 3000）
func extractPort(service string) string {
	s := service
	for _, prefix := range []string{"https://", "http://"} {
		s = strings.TrimPrefix(s, prefix)
	}
	_, port, err := net.SplitHostPort(s)
	if err != nil {
		return ""
	}
	return port
}
