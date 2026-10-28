package daemon

import (
	"context"
	"net"
	"os"
	"sync"
	"time"

	"github.com/shiranzby/cftunnelX/internal/cfapi"
	"github.com/shiranzby/cftunnelX/internal/config"
)

// 隧道健康探测与自愈。
//
// 为什么需要这一层：
//
//	原先"隧道是否正常"只靠判断 cloudflared 进程是否存在（kill -0）。
//	但 cloudflared 在边缘不可达时是**指数退避无限重试、不退出**，
//	于是进程永远"活着"：进程存活与隧道可用在这类故障下完全脱钩，
//	面板/API/命令行全部显示 running，而公网已经 530/1033。
//
// 做法：以约 3 秒的间隔轮询 Cloudflare 的隧道活动连接列表（复用 keep-alive
// 连接池，单次请求很小），得到四态结论；确认断连后立即重启**那一条**隧道。
//
//	healthy  有活动连接           不动作
//	stalled  进程在，但 0 连接     连续确认后立即重启（只重启这一条）
//	blocked  Cloudflare 根本不可达  只记录，不重启（重启无效，徒增抖动）
//	unknown  无法判定（未运行/无凭据/无权限）不动作
//
// 关于 blocked 仍不重启：边缘 7844 整段不可达时，重启只能换来新一轮超时，
// 且会瞬断该隧道全部在途连接（含 SSH 路由）。这与"断连就重启"不矛盾 ——
// 能被重启解决的断连，前提是网络本身通、只是我方连接坏了。
type TunnelHealth string

const (
	HealthHealthy TunnelHealth = "healthy"
	HealthStalled TunnelHealth = "stalled"
	HealthBlocked TunnelHealth = "blocked"
	HealthUnknown TunnelHealth = "unknown"
)

// 探测与自愈参数。
//
// 用 var 而非 const：单元测试需要缩短间隔来验证判定逻辑。
// ⚠ 修改这些值应在 StartHealthMonitor 之前完成，运行期变更不保证生效。
var (
	// ProbeInterval 探测周期。接近实时；查询走 keep-alive 连接，
	// 单次是几十毫秒的小请求，这个频率在 Cloudflare 的接口配额内。
	ProbeInterval = 3 * time.Second
	// ProbeTimeout 单次 Cloudflare API 查询的超时。
	ProbeTimeout = 8 * time.Second
	// StalledConfirm 连续多少次探测都"无连接"才判定为真断连。
	// 取 2 是为了滤掉单次网络抖动的误报（约 3 秒），不是延迟策略。
	StalledConfirm = 2
	// RestartSettle 首次发现隧道在运行、以及每次重启之后，留给 cloudflared
	// 完成边缘注册的宽限时间。没有它，会在进程刚起来、还没注册完时就判死
	// 并杀掉，陷入"启动→3 秒后被杀→再启动"的死循环。
	RestartSettle = 15 * time.Second
	// RestartGrace 平滑重启时 SIGTERM 后等待退出的宽限期。
	RestartGrace = 5 * time.Second
	// EdgeProbeTimeout 边缘连通性探测超时。
	EdgeProbeTimeout = 3 * time.Second
	// configTTL 配置缓存有效期。3 秒轮询没必要每次都读盘解析 YAML。
	configTTL = 15 * time.Second

	// edgeProbeHost/Port 是 cloudflared 自己使用的边缘入口，
	// 用它做"我能不能连出去"的判据最贴近真实情况。
	edgeProbeHost = "region1.v2.argotunnel.com"
	edgeProbePort = "7844"
)

// healthEntry 单条隧道的探测状态。所有字段受 healthMonitor.mu 保护。
type healthEntry struct {
	state          TunnelHealth
	stateSince     time.Time // 进入当前状态的时间
	connectors     int       // 活动连接数；-1 表示未知
	lastRegistered time.Time // 最近一次确认有连接的时间
	lastChecked    time.Time
	detail         string      // 判定依据，便于排查
	restartTimes   []time.Time // 自动重启时间，用于 24h 计数

	stalledStreak int       // 连续判定为 stalled 的次数
	graceUntil    time.Time // 在此之前不做重启决策（等 cloudflared 注册）
	restarting    bool      // 重启进行中，避免并发重复触发
}

// HealthSnapshot 是对外暴露的只读快照。
type HealthSnapshot struct {
	Health           TunnelHealth `json:"health"`
	Connectors       int          `json:"connectors"`
	LastRegisteredAt string       `json:"last_registered_at"`
	LastCheckedAt    string       `json:"last_checked_at"`
	RestartCount24h  int          `json:"restart_count_24h"`
	OutageSeconds    int64        `json:"outage_seconds"`
	Detail           string       `json:"detail,omitempty"`
}

// healthMonitor 是全局单例：探测器在后台写，HTTP handler 只读快照。
// 读路径只做内存拷贝，保证 /api/status 仍然秒回。
type healthMonitor struct {
	mu      sync.RWMutex
	entries map[string]*healthEntry

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}

	// 配置缓存：避免 3 秒一次读盘解析 YAML
	cfgMu     sync.Mutex
	cfgCache  *config.Config
	cfgLoaded time.Time

	// Cloudflare 客户端缓存：复用其底层 keep-alive 连接池
	clientMu  sync.Mutex
	client    *cfapi.Client
	clientKey string
}

var monitor = &healthMonitor{
	entries: make(map[string]*healthEntry),
	stopCh:  make(chan struct{}),
}

// StartHealthMonitor 启动后台探测循环。可重复调用，只有首次生效。
func StartHealthMonitor() {
	monitor.start()
}

// StopHealthMonitor 停止后台探测循环。可重复调用。
func StopHealthMonitor() {
	monitor.stop()
}

func (m *healthMonitor) start() {
	m.startOnce.Do(func() {
		go m.loop()
	})
}

func (m *healthMonitor) stop() {
	m.stopOnce.Do(func() {
		close(m.stopCh)
	})
}

func (m *healthMonitor) loop() {
	ticker := time.NewTicker(ProbeInterval)
	defer ticker.Stop()

	// 启动即探一次，不必等满一个周期
	m.safeProbe()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.safeProbe()
		}
	}
}

// safeProbe 包一层 recover：探测器自身出错不能把后台循环打死，
// 更不能因此影响隧道运行（探测只是观察者）。
func (m *healthMonitor) safeProbe() {
	defer func() {
		if r := recover(); r != nil {
			logf("隧道健康探测发生 panic（已恢复，隧道不受影响）: %v", r)
		}
	}()
	m.probeAll()
}

func (m *healthMonitor) probeAll() {
	cfg := m.loadConfig()
	if cfg == nil {
		return
	}

	client := m.getClient(cfg)

	// Load 时 migrateSingleTunnel 已把旧配置搬进 Tunnels，这里直接遍历即可。
	tunnels := cfg.Tunnels
	if len(tunnels) == 0 && cfg.Tunnel.ID != "" {
		tunnels = []config.TunnelConfig{cfg.Tunnel}
	}

	for _, t := range tunnels {
		if t.ID == "" {
			continue
		}
		m.probeOne(client, t)
	}

	// 清理已从配置中删除的隧道，避免快照无限增长
	m.prune(tunnels)
}

// loadConfig 带 TTL 的配置读取。隧道列表变动不频繁，每次轮询都全量解析没必要。
func (m *healthMonitor) loadConfig() *config.Config {
	m.cfgMu.Lock()
	defer m.cfgMu.Unlock()

	if m.cfgCache != nil && time.Since(m.cfgLoaded) < configTTL {
		return m.cfgCache
	}
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return m.cfgCache // 读失败时沿用旧值，避免瞬时抖动导致漏探
	}
	m.cfgCache = cfg
	m.cfgLoaded = time.Now()
	return cfg
}

// getClient 复用同一个 Cloudflare 客户端，从而复用底层 keep-alive 连接池。
// 这就是"保持一个连接做保活监测"的落点：不必每次探测都新建连接。
func (m *healthMonitor) getClient(cfg *config.Config) *cfapi.Client {
	if cfg == nil || cfg.Auth.APIToken == "" || cfg.Auth.AccountID == "" {
		return nil
	}
	key := cfg.Auth.APIToken + "\x00" + cfg.Auth.AccountID

	m.clientMu.Lock()
	defer m.clientMu.Unlock()
	if m.client != nil && m.clientKey == key {
		return m.client
	}
	m.client = cfapi.New(cfg.Auth.APIToken, cfg.Auth.AccountID)
	m.clientKey = key
	return m.client
}

func (m *healthMonitor) probeOne(client *cfapi.Client, t config.TunnelConfig) {
	now := time.Now()

	// 进程不在：不调 API，直接 unknown（运行态由 running 字段表达）
	if !RunningTunnel(t.ID) {
		m.apply(t.ID, HealthUnknown, -1, now, "进程未运行")
		m.resetStreak(t.ID)
		return
	}

	if client == nil {
		m.apply(t.ID, HealthUnknown, -1, now, "未配置 API Token / Account ID，无法探测连接状态")
		return
	}

	// 首次发现该隧道在运行 → 给 cloudflared 留出注册时间
	m.startGraceIfNeeded(t.ID, now)

	res := probeTunnel(client, t.ID)
	m.apply(t.ID, res.state, res.connectors, now, res.detail)

	if res.state != HealthStalled {
		m.resetStreak(t.ID)
		return
	}
	if !m.markStalled(t.ID, now) {
		return
	}
	go m.restartTunnel(t.ID, t.Token)
}

// probeResult 一次探测的原始结论，不涉及状态机。
type probeResult struct {
	state      TunnelHealth
	connectors int
	detail     string
}

// probeTunnel 只回答"现在是什么状态"，不修改任何状态、不做自愈。
func probeTunnel(client *cfapi.Client, tunnelID string) probeResult {
	ctx, cancel := context.WithTimeout(context.Background(), ProbeTimeout)
	defer cancel()

	conns, err := client.GetTunnelConnections(ctx, tunnelID)
	if err != nil {
		// 凭据/权限问题、隧道已被远端删除：都不代表"隧道卡死"，
		// 重启也解决不了，一律按 unknown 处理，绝不自愈。
		// 若把 403 当成"边缘可达 → stalled"，会让权限不足的用户
		// 反复重启一个健康的隧道。
		if cfapi.IsAuthError(err) {
			return probeResult{HealthUnknown, -1, "API 凭据或权限不足，无法探测连接: " + err.Error()}
		}
		if cfapi.IsTunnelNotFound(err) {
			return probeResult{HealthUnknown, -1, "隧道在 Cloudflare 侧不存在: " + err.Error()}
		}

		// API 不可达。关键区分：
		//   能连上边缘 7844 ⇒ 网络通，是我方连接坏了 ⇒ stalled（重启有效）
		//   连不上边缘     ⇒ 外部阻断 ⇒ blocked（重启无效，别动）
		if edgeReachable() {
			return probeResult{HealthStalled, -1, "API 不可达但边缘可达，判定为连接卡死: " + err.Error()}
		}
		return probeResult{HealthBlocked, -1, "无法连接 Cloudflare: " + err.Error()}
	}

	if len(conns) > 0 {
		return probeResult{HealthHealthy, len(conns), ""}
	}
	return probeResult{HealthStalled, 0, "进程在运行但没有任何活动连接"}
}

// edgeReachable 判断本机能否建立到 Cloudflare 隧道边缘的 TCP 连接。
// 只用于区分"我方连接坏了"与"根本连不出去"，因此用短超时。
func edgeReachable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), EdgeProbeTimeout)
	defer cancel()

	addrs, err := net.DefaultResolver.LookupHost(ctx, edgeProbeHost)
	if err != nil || len(addrs) == 0 {
		return false
	}
	for _, addr := range addrs {
		d := net.Dialer{Timeout: EdgeProbeTimeout}
		conn, derr := d.DialContext(ctx, "tcp", net.JoinHostPort(addr, edgeProbePort))
		if derr == nil {
			_ = conn.Close()
			return true
		}
	}
	return false
}

// apply 写入一次探测结果，处理"进入状态"的时间戳。
func (m *healthMonitor) apply(tunnelID string, state TunnelHealth, connectors int, now time.Time, detail string) {
	changed := false

	m.mu.Lock()
	e := m.entryLocked(tunnelID)
	if e.state != state {
		e.state = state
		e.stateSince = now
		changed = true
	}
	e.connectors = connectors
	e.lastChecked = now
	e.detail = detail
	if state == HealthHealthy {
		e.lastRegistered = now
	}
	m.mu.Unlock()

	// 日志放在锁外：logf 会做磁盘写入，持锁调用会拖慢 /api/status 的快照读。
	if changed {
		logf("隧道 %s 健康状态变更: %s（连接 %d）%s", tunnelID, state, connectors, detail)
	}
}

// startGraceIfNeeded 首次发现隧道在运行时，给它留出注册宽限。
func (m *healthMonitor) startGraceIfNeeded(tunnelID string, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e := m.entryLocked(tunnelID)
	if e.graceUntil.IsZero() {
		e.graceUntil = now.Add(RestartSettle)
	}
}

// resetStreak 清空连续 stalled 计数。
func (m *healthMonitor) resetStreak(tunnelID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.entries[tunnelID]; ok {
		e.stalledStreak = 0
	}
}

// markStalled 累加连续 stalled 次数，并判断当前是否应当重启。
// 返回 true 时调用方负责真正执行重启（本函数已把 restarting 置位）。
func (m *healthMonitor) markStalled(tunnelID string, now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	e := m.entryLocked(tunnelID)
	e.stalledStreak++

	// 三个前提：连续确认够次数、已过注册宽限、没有正在进行的重启
	if e.stalledStreak < StalledConfirm {
		return false
	}
	if now.Before(e.graceUntil) {
		return false
	}
	if e.restarting {
		return false
	}

	e.restarting = true
	return true
}

// recordRestart 记录一次自动重启，并顺手裁掉 24 小时之外的记录。
func (m *healthMonitor) recordRestart(tunnelID string, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e := m.entryLocked(tunnelID)
	kept := e.restartTimes[:0]
	for _, rt := range e.restartTimes {
		if now.Sub(rt) <= 24*time.Hour {
			kept = append(kept, rt)
		}
	}
	e.restartTimes = append(kept, now)
}

// restartTunnel 平滑重启**这一条**隧道：SIGTERM → 宽限 → SIGKILL，然后重新拉起。
//
// 只操作目标隧道的 PID 文件与进程，其余隧道完全不受影响。
func (m *healthMonitor) restartTunnel(tunnelID, token string) {
	defer func() {
		// 无论成败都要解除标记并给出新一轮注册宽限，
		// 否则该隧道会永远不再被自愈。
		m.mu.Lock()
		if e, ok := m.entries[tunnelID]; ok {
			e.restarting = false
			e.graceUntil = time.Now().Add(RestartSettle)
			e.stalledStreak = 0
		}
		m.mu.Unlock()

		if r := recover(); r != nil {
			logf("隧道 %s 重启过程发生 panic（已恢复）: %v", tunnelID, r)
		}
	}()

	if token == "" {
		logf("隧道 %s 判定为断连，但配置里没有 token，无法自动重启", tunnelID)
		return
	}

	m.recordRestart(tunnelID, time.Now())
	logf("隧道 %s 连接中断（stalled），立即重启该隧道", tunnelID)

	if pid := TunnelPID(tunnelID); pid > 0 {
		if err := processTerminateGraceful(pid, RestartGrace); err != nil {
			logf("隧道 %s 终止旧进程失败: %v", tunnelID, err)
		}
	}
	os.Remove(tunnelPIDPath(tunnelID))
	time.Sleep(500 * time.Millisecond)

	if err := StartTunnel(tunnelID, token); err != nil {
		logf("隧道 %s 自动重启失败: %v", tunnelID, err)
		return
	}
	// 重启后的复检由探测循环自然完成：宽限期内不会再次重启，
	// 注册成功后状态会切回 healthy 并单独记一条日志。
	logf("隧道 %s 已重启，等待边缘注册（宽限 %s）", tunnelID, RestartSettle)
}

// entryLocked 取（必要时创建）条目。调用方必须已持有 m.mu。
func (m *healthMonitor) entryLocked(tunnelID string) *healthEntry {
	e, ok := m.entries[tunnelID]
	if !ok {
		e = &healthEntry{state: HealthUnknown, stateSince: time.Now(), connectors: -1}
		m.entries[tunnelID] = e
	}
	return e
}

// prune 删除已不在配置中的隧道条目。
func (m *healthMonitor) prune(tunnels []config.TunnelConfig) {
	alive := make(map[string]bool, len(tunnels))
	for _, t := range tunnels {
		alive[t.ID] = true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id := range m.entries {
		if !alive[id] {
			delete(m.entries, id)
		}
	}
}

// HealthOf 返回单条隧道的健康快照。未探测过时返回 unknown。
// 路径极短（一次内存读），可安全用于每次 /api/status。
func HealthOf(tunnelID string) HealthSnapshot {
	return monitor.snapshot(tunnelID)
}

func (m *healthMonitor) snapshot(tunnelID string) HealthSnapshot {
	now := time.Now()

	m.mu.RLock()
	defer m.mu.RUnlock()

	e, ok := m.entries[tunnelID]
	if !ok {
		return HealthSnapshot{
			Health:     HealthUnknown,
			Connectors: -1,
		}
	}

	snap := HealthSnapshot{
		Health:          e.state,
		Connectors:      e.connectors,
		RestartCount24h: countRecent(e.restartTimes, now),
		Detail:          e.detail,
	}
	if !e.lastRegistered.IsZero() {
		snap.LastRegisteredAt = e.lastRegistered.UTC().Format(time.RFC3339)
	}
	if !e.lastChecked.IsZero() {
		snap.LastCheckedAt = e.lastChecked.UTC().Format(time.RFC3339)
	}
	// 只有 stalled / blocked 才算"中断"。unknown 表示无法判定
	//（例如隧道本来就是停止状态），此时累计中断时长只会无限增长、误导使用者。
	if isOutage(e.state) && !e.stateSince.IsZero() {
		snap.OutageSeconds = int64(now.Sub(e.stateSince).Seconds())
	}
	return snap
}

// isOutage 判断该状态是否代表"本应可用但当前不可用"。
func isOutage(s TunnelHealth) bool {
	return s == HealthStalled || s == HealthBlocked
}

// OverallHealth 汇总所有隧道的健康度，取"最需要关注"的那一档。
// 优先级：stalled（可自救）> blocked（外部阻断）> unknown > healthy。
type OverallHealth struct {
	State         TunnelHealth `json:"state"`
	Since         string       `json:"since"`
	OutageSeconds int64        `json:"outage_seconds"`
}

func OverallHealthOf() OverallHealth {
	return monitor.overall()
}

func (m *healthMonitor) overall() OverallHealth {
	now := time.Now()

	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.entries) == 0 {
		return OverallHealth{State: HealthUnknown}
	}

	rank := func(s TunnelHealth) int {
		switch s {
		case HealthStalled:
			return 3
		case HealthBlocked:
			return 2
		case HealthUnknown:
			return 1
		default:
			return 0
		}
	}

	worst := HealthHealthy
	var worstSince time.Time
	seenHealthy := false

	for _, e := range m.entries {
		if e.state == HealthHealthy {
			seenHealthy = true
			continue
		}
		if rank(e.state) > rank(worst) {
			worst = e.state
			worstSince = e.stateSince
		}
	}
	if worst == HealthHealthy && seenHealthy {
		return OverallHealth{State: HealthHealthy}
	}
	if worst == HealthHealthy {
		return OverallHealth{State: HealthUnknown}
	}

	out := OverallHealth{State: worst, Since: worstSince.UTC().Format(time.RFC3339)}
	if isOutage(worst) && !worstSince.IsZero() {
		out.OutageSeconds = int64(now.Sub(worstSince).Seconds())
	}
	return out
}

// countRecent 统计 24 小时内的重启次数。
func countRecent(times []time.Time, now time.Time) int {
	n := 0
	for _, t := range times {
		if now.Sub(t) <= 24*time.Hour {
			n++
		}
	}
	return n
}
