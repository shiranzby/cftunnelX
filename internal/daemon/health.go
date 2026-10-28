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

// 隧道健康探测与分级自愈。
//
// 为什么需要这一层：
//
//	原先"隧道是否正常"只靠判断 cloudflared 进程是否存在（kill -0）。
//	但 cloudflared 在边缘不可达时是**指数退避无限重试、不退出**，
//	于是进程永远"活着"：进程存活与隧道可用在这类故障下完全脱钩，
//	面板/API/命令行全部显示 running，而公网已经 530/1033。
//
// 本文件的做法：周期性调用 Cloudflare API 查询隧道的**活动连接数**，
// 得到四态结论，并按故障类型决定是否自愈——
//
//	healthy  有活动连接           不动作
//	stalled  进程在，但 0 连接     持续达阈值后平滑重启（重启有效）
//	blocked  Cloudflare 根本不可达  只记录，不重启（重启无效，徒增抖动）
//	unknown  无法判定（未运行/无凭据）不动作
//
// 刻意不做"定时无脑重启"：边缘整段被阻断时重启只会换来新一轮 timeout，
// 还会瞬断该隧道上的全部在途连接（含 SSH）。
type TunnelHealth string

const (
	HealthHealthy TunnelHealth = "healthy"
	HealthStalled TunnelHealth = "stalled"
	HealthBlocked TunnelHealth = "blocked"
	HealthUnknown TunnelHealth = "unknown"
)

// 探测与自愈参数。
//
// 用 var 而非 const：单元测试需要缩短间隔来验证阈值逻辑。
// ⚠ 修改这些值应在 StartHealthMonitor 之前完成，运行期变更不保证生效。
var (
	// ProbeInterval 探测周期。
	ProbeInterval = 30 * time.Second
	// ProbeTimeout 单次 Cloudflare API 查询的超时。
	ProbeTimeout = 8 * time.Second
	// StalledThreshold 连续 stalled 达到该时长才重启，避免启动瞬间误杀。
	StalledThreshold = 90 * time.Second
	// BlockedThreshold blocked 仅用于对外报告"已中断多久"，不触发重启。
	BlockedThreshold = 60 * time.Second
	// RestartCooldown 同一隧道两次自动重启的最小间隔，防风暴。
	RestartCooldown = 15 * time.Minute
	// RestartGrace 平滑重启时 SIGTERM 后等待退出的宽限期。
	RestartGrace = 5 * time.Second
	// PostRestartDelay 重启后等待多久再复检并写日志。
	PostRestartDelay = 30 * time.Second
	// EdgeProbeTimeout 边缘连通性探测超时。
	EdgeProbeTimeout = 3 * time.Second

	// edgeProbeHost/Port 是 cloudflared 自己使用的边缘入口，
	// 用它做"我能不能连出去"的判据最贴近真实情况。
	edgeProbeHost = "region1.v2.argotunnel.com"
	edgeProbePort = "7844"
)

// healthEntry 单条隧道的探测状态。所有字段受 healthMonitor.mu 保护。
type healthEntry struct {
	state          TunnelHealth
	stateSince     time.Time // 进入当前状态的时间，用于阈值判断
	connectors     int       // 活动连接数；-1 表示未知
	lastRegistered time.Time // 最近一次确认有连接的时间
	lastChecked    time.Time
	detail         string      // 判定依据，便于排查
	restartTimes   []time.Time // 自动重启时间，用于冷却与 24h 计数
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
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return
	}

	client := buildProbeClient(cfg)

	// Load 时 migrateSingleTunnel 已把旧配置搬进 Tunnels，这里直接遍历即可。
	// 极端情况下（未迁移成功）再兜底处理单隧道字段。
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

func buildProbeClient(cfg *config.Config) *cfapi.Client {
	if cfg == nil || cfg.Auth.APIToken == "" || cfg.Auth.AccountID == "" {
		return nil
	}
	return cfapi.New(cfg.Auth.APIToken, cfg.Auth.AccountID)
}

func (m *healthMonitor) probeOne(client *cfapi.Client, t config.TunnelConfig) {
	now := time.Now()

	// 进程不在：不调 API，直接 unknown（null 态由 running 字段表达）
	if !RunningTunnel(t.ID) {
		m.apply(t.ID, HealthUnknown, -1, now, "进程未运行")
		return
	}

	if client == nil {
		m.apply(t.ID, HealthUnknown, -1, now, "未配置 API Token / Account ID，无法探测连接状态")
		return
	}

	res := probeTunnel(client, t.ID)
	m.apply(t.ID, res.state, res.connectors, now, res.detail)

	if res.state != HealthStalled {
		return
	}
	if !m.shouldRestart(t.ID, now) {
		return
	}
	m.markRestart(t.ID, now)
	go m.restartTunnel(t.ID, t.Token)
}

// probeResult 一次探测的原始结论，不涉及状态机。
type probeResult struct {
	state      TunnelHealth
	connectors int
	detail     string
}

// probeTunnel 只回答"现在是什么状态"，不修改任何状态、不做自愈。
// 单独拆出来便于单元测试与重启后复检复用。
func probeTunnel(client *cfapi.Client, tunnelID string) probeResult {
	ctx, cancel := context.WithTimeout(context.Background(), ProbeTimeout)
	defer cancel()

	conns, err := client.GetTunnelConnections(ctx, tunnelID)
	if err != nil {
		// 凭据/权限问题、隧道已被远端删除：都不代表"隧道卡死"，
		// 重启也解决不了，一律按 unknown 处理，绝不自愈。
		// 这一支尤其重要：若把 403 当成"边缘可达 → stalled"，
		// 会让权限不足的用户每 15 分钟被无谓重启一次健康隧道。
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

	// 日志放在锁外：logf 会做磁盘写入，持锁调用会拖慢 /api/status 的快照读，
	// 违反"状态接口必须秒回"的约束。
	if changed {
		logf("隧道 %s 健康状态变更: %s（连接 %d）%s", tunnelID, state, connectors, detail)
	}
}

// shouldRestart 判断是否满足重启条件：stalled 持续达阈值，且不在冷却期。
func (m *healthMonitor) shouldRestart(tunnelID string, now time.Time) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	e, ok := m.entries[tunnelID]
	if !ok || e.state != HealthStalled {
		return false
	}
	if now.Sub(e.stateSince) < StalledThreshold {
		return false
	}
	for _, rt := range e.restartTimes {
		if now.Sub(rt) < RestartCooldown {
			return false
		}
	}
	return true
}

// markRestart 记录一次自动重启，并顺手裁掉 24 小时之外的记录。
func (m *healthMonitor) markRestart(tunnelID string, now time.Time) {
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

// restartTunnel 平滑重启：SIGTERM → 宽限 → SIGKILL，然后重新拉起并复检。
func (m *healthMonitor) restartTunnel(tunnelID, token string) {
	if token == "" {
		logf("隧道 %s 判定为 stalled，但配置里没有 token，无法自动重启", tunnelID)
		return
	}

	logf("隧道 %s 判定为 stalled 已超过 %s，开始自动平滑重启", tunnelID, StalledThreshold)

	if pid := TunnelPID(tunnelID); pid > 0 {
		if err := processTerminateGraceful(pid, RestartGrace); err != nil {
			logf("隧道 %s 终止旧进程失败: %v", tunnelID, err)
		}
	}
	os.Remove(tunnelPIDPath(tunnelID))
	time.Sleep(750 * time.Millisecond)

	if err := StartTunnel(tunnelID, token); err != nil {
		logf("隧道 %s 自动重启失败: %v", tunnelID, err)
		return
	}
	logf("隧道 %s 已自动重启，%s 后复检", tunnelID, PostRestartDelay)

	// 复检：把结果写进日志，便于确认自愈是否真的有效
	select {
	case <-m.stopCh:
		return
	case <-time.After(PostRestartDelay):
	}

	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return
	}
	client := buildProbeClient(cfg)
	if client == nil {
		return
	}
	res := probeTunnel(client, tunnelID)
	logf("隧道 %s 重启后复检: %s（连接 %d）%s", tunnelID, res.state, res.connectors, res.detail)
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
			Health:        HealthUnknown,
			Connectors:    -1,
			OutageSeconds: 0,
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
	// 全部 healthy
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
