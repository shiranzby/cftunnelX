package authproxy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shiranzby/cftunnelX/internal/config"
)

// Desired 期望运行的一个鉴权代理。
type Desired struct {
	Port       int
	TargetPort string
	Username   string
	Password   string
	SigningKey []byte
	CookieTTL  time.Duration
	// Salt 用于在未配置 signing_key 时派生一个稳定的密钥（通常取路由名或域名）。
	Salt string
}

// Fingerprint 返回该期望配置的指纹（已哈希，可安全记录到日志）。
func (d Desired) Fingerprint() string {
	raw := strings.Join([]string{
		strconv.Itoa(d.Port),
		d.TargetPort,
		d.Username,
		d.Password,
		hex.EncodeToString(d.SigningKey),
		strconv.Itoa(int(d.CookieTTL.Seconds())),
		d.Salt,
	}, "\x00")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:8])
}

// key 返回实际用于签名的密钥。
//
// 未配置 signing_key 时按 用户名+密码+盐 派生，保证同一配置在重启后
// 得到同一密钥（否则已发放的登录 Cookie 会在重启后全部失效）。
// 不做随机生成，是为了避免"每次重启都换密钥"这种看似安全实则烦人的行为。
func (d Desired) key() []byte {
	if len(d.SigningKey) > 0 {
		return d.SigningKey
	}
	sum := sha256.Sum256([]byte("cftunnelX-auth-cookie|" +
		d.Username + "|" + d.Password + "|" + d.Salt))
	return sum[:]
}

// DesiredFromConfig 从配置推导期望运行的代理集合。
// 同时返回被跳过的路由说明（例如 service 端口无法解析）。
func DesiredFromConfig(cfg *config.Config) ([]Desired, []string) {
	if cfg == nil {
		return nil, nil
	}
	byPort := map[int]Desired{}
	var problems []string

	for _, r := range cfg.AllRoutes() {
		if !r.AuthEnabled() {
			continue
		}
		port := r.AuthProxyPort()
		if port <= 0 {
			problems = append(problems,
				fmt.Sprintf("路由 %s 的 service 端口无法解析（%s），无法确定鉴权代理端口", r.Name, r.Service))
			continue
		}
		target := strconv.Itoa(r.ServicePort())
		if target == "0" {
			problems = append(problems, fmt.Sprintf("路由 %s 的 service 端口无效", r.Name))
			continue
		}
		salt := r.Name
		if r.Hostname != "" {
			salt = r.Hostname
		}
		sk, err := hex.DecodeString(strings.TrimSpace(r.Auth.SigningKey))
		if err != nil {
			problems = append(problems,
				fmt.Sprintf("路由 %s 的 signing_key 不是合法十六进制，已改用按域名派生", r.Name))
			sk = nil
		}
		ttl := time.Duration(r.Auth.CookieTTL) * time.Second
		if r.Auth.CookieTTL <= 0 {
			ttl = 24 * time.Hour
		}

		d := Desired{
			Port:       port,
			TargetPort: target,
			Username:   r.Auth.Username,
			Password:   r.Auth.Password,
			SigningKey: sk,
			CookieTTL:  ttl,
			Salt:       salt,
		}
		// 同一端口被多条路由引用时取第一条并提示，避免静默覆盖
		if prev, ok := byPort[port]; ok && prev.Fingerprint() != d.Fingerprint() {
			problems = append(problems,
				fmt.Sprintf("端口 %d 被多条鉴权路由使用且配置不同（%s / %s），已采用先出现者",
					port, prev.Username, d.Username))
			continue
		}
		byPort[port] = d
	}

	desired := make([]Desired, 0, len(byPort))
	for _, d := range byPort {
		desired = append(desired, d)
	}
	sort.Slice(desired, func(i, j int) bool { return desired[i].Port < desired[j].Port })
	return desired, problems
}

type managedProxy struct {
	proxy       *Proxy
	fingerprint string
}

// Manager 维护一组按端口绑定的鉴权代理，支持随配置热更新。
//
// 之所以要"按端口绑定 + 指纹比对"：
//   - 端口必须与远端 ingress 中写的值一致，因此精确绑定、冲突即报错；
//   - 配置变化（改密码、改目标端口）必须重启对应代理才会生效，
//     否则用户改了密码却发现旧密码仍然可用。
type Manager struct {
	running map[int]*managedProxy
}

func NewManager() *Manager {
	return &Manager{running: map[int]*managedProxy{}}
}

// Reconcile 使运行集合与期望集合一致。返回是否有任何增删改。
func (m *Manager) Reconcile(desired []Desired) (changed bool, errs []string) {
	want := make(map[int]Desired, len(desired))
	for _, d := range desired {
		want[d.Port] = d
	}

	// 停止不再需要、或配置已变化的代理
	for port, mp := range m.running {
		d, keep := want[port]
		if keep && d.Fingerprint() == mp.fingerprint {
			continue
		}
		if err := mp.proxy.Stop(); err != nil {
			errs = append(errs, fmt.Sprintf("停止端口 %d 的旧代理失败: %v", port, err))
		}
		delete(m.running, port)
		changed = true
	}

	// 启动缺失的代理
	for port, d := range want {
		if _, ok := m.running[port]; ok {
			continue
		}
		p, err := NewOnPort(Config{
			Username:   d.Username,
			Password:   d.Password,
			TargetPort: d.TargetPort,
			SigningKey: d.key(),
			CookieTTL:  d.CookieTTL,
		}, port)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		if err := p.Start(); err != nil {
			errs = append(errs, fmt.Sprintf("启动端口 %d 的鉴权代理失败: %v", port, err))
			continue
		}
		m.running[port] = &managedProxy{proxy: p, fingerprint: d.Fingerprint()}
		changed = true
	}
	return changed, errs
}

// Ports 返回当前正在监听的端口（升序）。
func (m *Manager) Ports() []int {
	ports := make([]int, 0, len(m.running))
	for p := range m.running {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	return ports
}

// Active 返回当前运行的代理数量。
func (m *Manager) Active() int {
	return len(m.running)
}

// StopAll 停止全部代理。
func (m *Manager) StopAll() {
	for port, mp := range m.running {
		_ = mp.proxy.Stop()
		delete(m.running, port)
	}
}
