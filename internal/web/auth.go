package web

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"

	"github.com/shiranzby/cftunnelX/internal/config"
)

// isLocalRequest 判断请求是否来自本地。存在代理头时优先使用真实客户端 IP。
func isLocalRequest(r *http.Request) bool {
	host := clientHost(r)
	host = strings.Trim(host, "[]")
	switch host {
	case "127.0.0.1", "::1", "localhost", "":
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func clientHost(r *http.Request) string {
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
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		return host[:idx]
	}
	return host
}

// noAuthPaths 不需要认证的 API 路径（即使远程访问也不拦截）
// 这些是"引导性"端点：配置认证、查看版本、设置主题等
var noAuthPaths = map[string]bool{
	"/api/webpanel": true,
	"/api/config":   true,
	"/api/theme":    true,
	"/api/version":  true,
	"/api/language": true,
	"/api/port":     true,
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

// authMiddleware 当启用远程访问时，对非本地请求进行 Basic Auth
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 本地请求始终放行
		if isLocalRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		// 免认证路径放行（配置/面板/版本等）
		if isNoAuthPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		// 静态资源放行
		if r.URL.Path == "/" || strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}
		cfg, err := config.Load()
		if !authRequired(cfg, err) {
			next.ServeHTTP(w, r)
			return
		}
		user, pass, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(user), []byte(cfg.WebUI.Username)) != 1 ||
			subtle.ConstantTimeCompare([]byte(pass), []byte(cfg.WebUI.Password)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="cftunnel"`)
			http.Error(w, "认证失败", 401)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func authRequired(cfg *config.Config, err error) bool {
	return err == nil &&
		cfg != nil &&
		cfg.WebUI.RemoteEnabled &&
		strings.TrimSpace(cfg.WebUI.Username) != "" &&
		strings.TrimSpace(cfg.WebUI.Password) != ""
}
