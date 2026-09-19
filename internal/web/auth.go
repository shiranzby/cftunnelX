package web

import (
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"github.com/shiranzby/cftunnelX/internal/config"
)

// isLocalRequest 判断请求是否来自本机浏览器。
//
// 安全模型（重要）：
//   - 先看 TCP 连接的真实来源地址。非回环连接一律视为远程，代理头在此完全不可信，
//     否则外部攻击者只需伪造 X-Forwarded-For: 127.0.0.1 即可绕过认证。
//   - 连接确实来自回环时，仍可能是 cloudflared 隧道转发的流量（cloudflared 在本机，
//     因此 RemoteAddr 也是回环）。此时若携带 Cloudflare/代理来源头，说明请求来自
//     隧道之外，必须按远程处理。
func isLocalRequest(r *http.Request) bool {
	if !isLoopbackRemote(r.RemoteAddr) {
		return false
	}
	return forwardedClientHost(r) == ""
}

// isLoopbackRemote 判断 TCP 对端地址是否为回环地址。
func isLoopbackRemote(remoteAddr string) bool {
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// forwardedClientHost 返回代理头中声明的真实客户端地址（无则返回空串）。
// 仅用于识别"经隧道转发"的流量，不再用于判定请求是否本地。
func forwardedClientHost(r *http.Request) string {
	for _, name := range []string{"CF-Connecting-IP", "X-Real-IP", "X-Forwarded-For"} {
		v := strings.TrimSpace(r.Header.Get(name))
		if v == "" {
			continue
		}
		if name == "X-Forwarded-For" {
			v = strings.TrimSpace(strings.Split(v, ",")[0])
		}
		if v != "" {
			return strings.Trim(v, "[]")
		}
	}
	return ""
}

// noAuthPaths 不需要认证的资源/探测路径。配置类接口在远程认证开启后必须受保护。
var noAuthPaths = map[string]bool{
	"/api/theme":       true,
	"/api/version":     true,
	"/api/language":    true,
	"/api/port":        true,
	"/assets/logo.png": true,
}

// isNoAuthPath 检查路径是否在免认证白名单中
func isNoAuthPath(path string) bool {
	// 精确匹配
	if noAuthPaths[path] {
		return true
	}
	// 前缀匹配（如 /api/config/test）
	for p := range noAuthPaths {
		if strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// authMiddleware 对非本地请求实施访问控制。
//
// 策略（兼顾安全与可用性）：
//  1. 本机浏览器：直接放行。
//  2. 已配置管理账号密码：所有非本地请求强制 Basic Auth。
//  3. 未配置账号密码且开启了远程访问：一律拒绝。否则等于把带终端执行能力的
//     接口暴露到公网（远程链路必须配凭据）。
//  4. 未配置账号密码且未开启远程访问：仅放行私有网段来源。
//     OpenWrt / 服务器是 headless 环境，管理面板需要从局域网打开，
//     因此不能像桌面端那样只认回环；但公网来源仍然拒绝。
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isLocalRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		// 非敏感的探测路径与静态资源放行
		if isNoAuthPath(r.URL.Path) ||
			strings.HasPrefix(r.URL.Path, "/static/") ||
			strings.HasPrefix(r.URL.Path, "/assets/") {
			next.ServeHTTP(w, r)
			return
		}
		cfg, err := config.Load()
		if err != nil || cfg == nil {
			denyUnauthorized(w, r, "读取配置失败")
			return
		}
		if hasWebCredentials(cfg) {
			user, pass, ok := r.BasicAuth()
			if !ok ||
				subtle.ConstantTimeCompare([]byte(user), []byte(cfg.WebUI.Username)) != 1 ||
				subtle.ConstantTimeCompare([]byte(pass), []byte(cfg.WebUI.Password)) != 1 {
				denyUnauthorized(w, r, "认证失败")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		// 凭据只配了一半：属于配置错误，明确拒绝而不是静默放行。
		// 旧行为下"只填密码不填用户名"会让远程请求落到免认证分支，
		// 用户以为有密码保护，实际谁都能进。
		if (strings.TrimSpace(cfg.WebUI.Username) == "") != (cfg.WebUI.Password == "") {
			denyUnauthorized(w, r,
				"管理用户名与密码只设置了一项，认证不会生效。请同时填写两者，或同时清空以关闭认证")
			return
		}
		if cfg.WebUI.RemoteEnabled {
			denyUnauthorized(w, r, "已开启远程访问但未配置管理账号密码，请先在设置中配置账号密码")
			return
		}
		if isPrivateClient(r) {
			next.ServeHTTP(w, r)
			return
		}
		denyUnauthorized(w, r, "来源不在内网且未配置管理账号密码")
	})
}

// hasWebCredentials 判断是否已配置完整的 Web 管理账号密码。
func hasWebCredentials(cfg *config.Config) bool {
	return cfg != nil &&
		strings.TrimSpace(cfg.WebUI.Username) != "" &&
		strings.TrimSpace(cfg.WebUI.Password) != ""
}

// effectiveClientIP 返回用于访问控制的客户端 IP。
// 隧道流量（回环连接 + 代理头）取代理头声明的真实客户端地址。
func effectiveClientIP(r *http.Request) net.IP {
	if isLoopbackRemote(r.RemoteAddr) {
		if h := forwardedClientHost(r); h != "" {
			if ip := net.ParseIP(h); ip != nil {
				return ip
			}
		}
	}
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return net.ParseIP(strings.Trim(host, "[]"))
}

// isPrivateClient 判断客户端是否位于本机或私有网段。
func isPrivateClient(r *http.Request) bool {
	ip := effectiveClientIP(r)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

// denyUnauthorized 统一输出 401：API 返回 JSON，页面返回浏览器认证框。
func denyUnauthorized(w http.ResponseWriter, r *http.Request, reason string) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": reason})
		return
	}
	w.Header().Set("WWW-Authenticate", `Basic realm="cftunnelX"`)
	http.Error(w, reason, http.StatusUnauthorized)
}
