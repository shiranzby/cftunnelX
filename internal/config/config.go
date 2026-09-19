package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Version     int               `yaml:"version"`
	Auth        AuthConfig        `yaml:"auth"`
	Tunnel      TunnelConfig      `yaml:"tunnel,omitempty"`  // 向后兼容（单隧道）
	Tunnels     []TunnelConfig    `yaml:"tunnels,omitempty"` // 多隧道
	Routes      []RouteConfig     `yaml:"routes,omitempty"`  // 向后兼容（单隧道路由）
	Relay       RelayConfig       `yaml:"relay,omitempty"`
	Cloudflared CloudflaredConfig `yaml:"cloudflared"`
	SelfUpdate  SelfUpdateConfig  `yaml:"self_update"`
	WebUI       WebUIConfig       `yaml:"web_ui"`
	Language    string            `yaml:"language,omitempty"` // zh-CN / en
}

// WebUIConfig Web 管理面板配置
type WebUIConfig struct {
	Port          string `yaml:"port"`           // Web UI 监听端口，默认 7860
	Listen        string `yaml:"listen"`         // 监听地址；留空=auto（本机 127.0.0.1，无图形界面的 Linux/OpenWrt 为 0.0.0.0）
	Username      string `yaml:"username"`       // 管理面板用户名（留空则无认证）
	Password      string `yaml:"password"`       // 管理面板密码（留空则无认证）
	Theme         string `yaml:"theme"`          // 主题: light / dark / system / monokai / dracula / nord
	RemoteEnabled bool   `yaml:"remote_enabled"` // 是否通过隧道远程暴露
	RemoteDomain  string `yaml:"remote_domain"`  // 远程访问域名
	TunnelName    string `yaml:"tunnel_name"`    // Web面板使用的隧道名（远程访问用）
	ServiceName   string `yaml:"service_name"`   // Web面板服务名（默认 cftunnel-web）
}

type AuthConfig struct {
	APIToken  string `yaml:"api_token"`
	AccountID string `yaml:"account_id"`
}

type TunnelConfig struct {
	ID     string        `yaml:"id"`
	Name   string        `yaml:"name"`
	Token  string        `yaml:"token"`
	Routes []RouteConfig `yaml:"routes,omitempty"` // 多隧道：路由内嵌
}

type RouteConfig struct {
	Name        string     `yaml:"name"`
	Hostname    string     `yaml:"hostname"`
	Service     string     `yaml:"service"`
	ZoneID      string     `yaml:"zone_id"`
	DNSRecordID string     `yaml:"dns_record_id"`
	Auth        *AuthProxy `yaml:"auth,omitempty"`
}

// AuthProxy 鉴权代理配置
type AuthProxy struct {
	Username   string `yaml:"username"`
	Password   string `yaml:"password"`
	SigningKey string `yaml:"signing_key,omitempty"`
	CookieTTL  int    `yaml:"cookie_ttl,omitempty"`  // 秒，默认 86400
	ListenPort int    `yaml:"listen_port,omitempty"` // 鉴权代理监听端口；0 表示按「目标端口+1」推导
}

// ServicePort 从 Service 中解析出端口号，失败返回 0。
func (r RouteConfig) ServicePort() int {
	s := r.Service
	for _, prefix := range []string{"https://", "http://"} {
		s = strings.TrimPrefix(s, prefix)
	}
	idx := strings.LastIndex(s, ":")
	if idx < 0 {
		return 0
	}
	p, err := strconv.Atoi(strings.TrimSpace(s[idx+1:]))
	if err != nil || p <= 0 || p > 65535 {
		return 0
	}
	return p
}

// AuthProxyPort 返回该路由鉴权代理应监听的端口；未启用鉴权或无法推导时返回 0。
//
// 🔴 必须是**确定性**的：远端 ingress 指向这个端口，端口一变就得重新推送。
// 早期实现从「目标端口+1」开始探测第一个可用端口，一旦被占用就会漂移，
// 造成 ingress 指向的端口与实际监听不一致——表现为 502，或者更糟：
// ingress 还指向源站端口，认证被完全绕过。
func (r RouteConfig) AuthProxyPort() int {
	if r.Auth == nil {
		return 0
	}
	if r.Auth.ListenPort > 0 {
		return r.Auth.ListenPort
	}
	p := r.ServicePort()
	if p <= 0 {
		return 0
	}
	return p + 1
}

// AuthEnabled 表示该路由是否启用了鉴权，且凭据完整。
func (r RouteConfig) AuthEnabled() bool {
	return r.Auth != nil &&
		strings.TrimSpace(r.Auth.Username) != "" &&
		r.Auth.Password != ""
}

// IngressRoutes 返回**用于生成 ingress 规则**的路由视图：
// 启用鉴权的路由，其 Service 会被替换为本地鉴权代理地址。
//
// 所有构造 ingress 规则的代码都必须用这个而不是 t.Routes，
// 否则鉴权路由会被直接指向源站 —— 认证静默失效，且没有任何报错。
func (t *TunnelConfig) IngressRoutes() []RouteConfig {
	if t == nil {
		return nil
	}
	out := make([]RouteConfig, 0, len(t.Routes))
	for _, r := range t.Routes {
		if !r.AuthEnabled() {
			out = append(out, r)
			continue
		}
		mapped := r
		mapped.Service = "http://127.0.0.1:" + strconv.Itoa(r.AuthProxyPort())
		out = append(out, mapped)
	}
	return out
}

// CookieTTLOrDefault 返回 Cookie 有效期（秒），默认 86400
func (a *AuthProxy) CookieTTLOrDefault() int {
	if a.CookieTTL > 0 {
		return a.CookieTTL
	}
	return 86400
}

// RelayConfig 中继模式配置
type RelayConfig struct {
	Server string      `yaml:"server,omitempty"`
	Token  string      `yaml:"token,omitempty"`
	Rules  []RelayRule `yaml:"rules,omitempty"`
}

// RelayRule 中继穿透规则
type RelayRule struct {
	Name       string `yaml:"name"`
	Proto      string `yaml:"proto"`              // tcp/udp/http/https/stcp
	LocalIP    string `yaml:"local_ip,omitempty"` // 默认 127.0.0.1
	LocalPort  int    `yaml:"local_port"`
	RemotePort int    `yaml:"remote_port,omitempty"` // HTTP 模式可选
	Domain     string `yaml:"domain,omitempty"`      // HTTP 模式用
}

type CloudflaredConfig struct {
	Path       string `yaml:"path"`
	AutoUpdate bool   `yaml:"auto_update"`
}

type SelfUpdateConfig struct {
	AutoCheck bool `yaml:"auto_check"` // 启动时自动检查 cftunnel 更新
}

// 路径与模式解析见 paths.go（Dir / LogDir / BinDir / RunDir / Mode / Ensure）。

// Path 返回配置文件路径。
func Path() string {
	return filepath.Join(Dir(), "config.yml")
}

func Load() (*Config, error) {
	data, err := os.ReadFile(Path())
	if err != nil {
		if os.IsNotExist(err) {
			cfg := &Config{Version: 1}
			cfg.applyEnvOverrides()
			return cfg, nil
		}
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	cfg.applyEnvOverrides()
	// 迁移：单隧道 → 多隧道数组
	cfg.migrateSingleTunnel()
	return &cfg, nil
}

// migrateSingleTunnel 将旧的单隧道配置迁移到 Tunnels 数组
func (c *Config) migrateSingleTunnel() {
	if len(c.Tunnels) == 0 && c.Tunnel.ID != "" {
		t := c.Tunnel
		t.Routes = c.Routes
		c.Tunnels = []TunnelConfig{t}
		c.Tunnel = TunnelConfig{}
		c.Routes = nil
	}
}

// ActiveTunnel 返回当前生效的隧道。
// 统一取值入口：Tunnels 为唯一数据源，Tunnel/Routes 仅作为旧配置的兼容回退
// （Loaded 时 migrateSingleTunnel 已完成搬运，正常情况下 Tunnels 非空）。
func (c *Config) ActiveTunnel() *TunnelConfig {
	if len(c.Tunnels) > 0 {
		return &c.Tunnels[0]
	}
	if c.Tunnel.ID != "" {
		return &c.Tunnel
	}
	return nil
}

// EnsureActiveTunnel 返回当前生效的可写隧道。
// 若配置仍处于旧单隧道形态，会先归一化到 Tunnels 再返回；无隧道时返回 nil。
func (c *Config) EnsureActiveTunnel() *TunnelConfig {
	c.migrateSingleTunnel()
	if len(c.Tunnels) > 0 {
		return &c.Tunnels[0]
	}
	if c.Tunnel.ID != "" {
		return &c.Tunnel
	}
	return nil
}

// ActiveTunnelID 返回当前生效隧道的 ID，无隧道返回空串。
func (c *Config) ActiveTunnelID() string {
	if t := c.ActiveTunnel(); t != nil {
		return t.ID
	}
	return ""
}

// ActiveTunnelName 返回当前生效隧道的名称，无隧道返回空串。
func (c *Config) ActiveTunnelName() string {
	if t := c.ActiveTunnel(); t != nil {
		return t.Name
	}
	return ""
}

// ActiveToken 返回当前生效隧道的 token，无隧道返回空串。
func (c *Config) ActiveToken() string {
	if t := c.ActiveTunnel(); t != nil {
		return t.Token
	}
	return ""
}

// ActiveRoutes 返回当前生效隧道的路由列表。
func (c *Config) ActiveRoutes() []RouteConfig {
	if len(c.Tunnels) > 0 {
		return c.Tunnels[0].Routes
	}
	return c.Routes
}

// AddActiveRoute 向当前生效隧道追加一条路由，并持久化到 Tunnels。
// 返回 false 表示当前没有可用的隧道。
func (c *Config) AddActiveRoute(r RouteConfig) bool {
	if c.EnsureActiveTunnel() == nil {
		return false
	}
	c.Tunnels[0].Routes = append(c.Tunnels[0].Routes, r)
	return true
}

// RemoveActiveTunnel 移除当前生效的隧道（供 CLI destroy 使用）。
// 返回 false 表示没有可移除的隧道。
func (c *Config) RemoveActiveTunnel() bool {
	if len(c.Tunnels) > 0 {
		c.Tunnels = c.Tunnels[1:]
		return true
	}
	if c.Tunnel.ID != "" {
		c.Tunnel = TunnelConfig{}
		c.Routes = nil
		return true
	}
	return false
}

// FindTunnel 按 ID 查找隧道
func (c *Config) FindTunnel(id string) *TunnelConfig {
	for i := range c.Tunnels {
		if c.Tunnels[i].ID == id {
			return &c.Tunnels[i]
		}
	}
	return nil
}

// RemoveTunnel 删除隧道
func (c *Config) RemoveTunnel(id string) bool {
	for i, t := range c.Tunnels {
		if t.ID == id {
			c.Tunnels = append(c.Tunnels[:i], c.Tunnels[i+1:]...)
			return true
		}
	}
	return false
}

// FindRoute 在所有隧道中查找路由
func (c *Config) FindRoute(name string) *RouteConfig {
	for i := range c.Tunnels {
		for j := range c.Tunnels[i].Routes {
			if c.Tunnels[i].Routes[j].Name == name {
				return &c.Tunnels[i].Routes[j]
			}
		}
	}
	// 向后兼容：顶层 routes
	for i := range c.Routes {
		if c.Routes[i].Name == name {
			return &c.Routes[i]
		}
	}
	return nil
}

// RemoveRoute 删除路由
func (c *Config) RemoveRoute(name string) bool {
	for i := range c.Tunnels {
		for j, r := range c.Tunnels[i].Routes {
			if r.Name == name {
				c.Tunnels[i].Routes = append(c.Tunnels[i].Routes[:j], c.Tunnels[i].Routes[j+1:]...)
				return true
			}
		}
	}
	for i, r := range c.Routes {
		if r.Name == name {
			c.Routes = append(c.Routes[:i], c.Routes[i+1:]...)
			return true
		}
	}
	return false
}

// applyEnvOverrides 用环境变量覆盖配置（CI/CD 和 Docker 场景）
func (c *Config) applyEnvOverrides() {
	if v := os.Getenv("CFTUNNEL_API_TOKEN"); v != "" {
		c.Auth.APIToken = v
	}
	if v := os.Getenv("CFTUNNEL_ACCOUNT_ID"); v != "" {
		c.Auth.AccountID = v
	}
	if v := os.Getenv("CFTUNNEL_RELAY_SERVER"); v != "" {
		c.Relay.Server = v
	}
	if v := os.Getenv("CFTUNNEL_RELAY_TOKEN"); v != "" {
		c.Relay.Token = v
	}
}

func (c *Config) Save() error {
	if err := os.MkdirAll(Dir(), 0700); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(Path(), data, 0600)
}

// FindRelayRule 查找中继规则
// AllRoutes 返回所有隧道的所有路由（展平）
func (c *Config) AllRoutes() []RouteConfig {
	var routes []RouteConfig
	for _, t := range c.Tunnels {
		routes = append(routes, t.Routes...)
	}
	routes = append(routes, c.Routes...)
	return routes
}

// IngressRoutes 返回所有隧道的路由（已应用鉴权代理映射）。
// 需要构造 ingress 规则时应该用它，而不是 AllRoutes()。
func (c *Config) IngressRoutes() []RouteConfig {
	var out []RouteConfig
	for i := range c.Tunnels {
		out = append(out, c.Tunnels[i].IngressRoutes()...)
	}
	if len(c.Tunnels) == 0 {
		legacy := TunnelConfig{Routes: c.Routes}
		out = append(out, legacy.IngressRoutes()...)
	}
	return out
}

func (c *Config) FindRelayRule(name string) *RelayRule {
	for i := range c.Relay.Rules {
		if c.Relay.Rules[i].Name == name {
			return &c.Relay.Rules[i]
		}
	}
	return nil
}

// RemoveRelayRule 删除中继规则
func (c *Config) RemoveRelayRule(name string) bool {
	for i, r := range c.Relay.Rules {
		if r.Name == name {
			c.Relay.Rules = append(c.Relay.Rules[:i], c.Relay.Rules[i+1:]...)
			return true
		}
	}
	return false
}
