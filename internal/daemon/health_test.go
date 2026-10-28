package daemon

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func newTestMonitor() *healthMonitor {
	return &healthMonitor{
		entries: make(map[string]*healthEntry),
		stopCh:  make(chan struct{}),
	}
}

// TestMarkStalledRequiresConfirm 验证需要连续 N 次无连接才判定断连，
// 避免单次网络抖动就重启（N 很小，反应时间仍在秒级）。
func TestMarkStalledRequiresConfirm(t *testing.T) {
	m := newTestMonitor()
	now := time.Now()
	// 宽限已在过去，排除宽限干扰
	m.startGraceIfNeeded("t1", now.Add(-time.Minute))

	for i := 1; i < StalledConfirm; i++ {
		if m.markStalled("t1", now) {
			t.Fatalf("第 %d 次探测就触发重启，阈值是 %d 次", i, StalledConfirm)
		}
	}
	if !m.markStalled("t1", now) {
		t.Fatalf("达到 %d 次确认后仍未触发重启", StalledConfirm)
	}
}

// TestMarkStalledBlockedBySettleWindow 验证注册宽限期内绝不重启。
// 这是防止"启动→3 秒后被杀→再启动"死循环的关键。
func TestMarkStalledBlockedBySettleWindow(t *testing.T) {
	m := newTestMonitor()
	now := time.Now()
	m.startGraceIfNeeded("t1", now) // graceUntil = now + RestartSettle

	for i := 0; i < StalledConfirm+3; i++ {
		if m.markStalled("t1", now) {
			t.Fatal("注册宽限期内不应重启（否则会杀掉还没注册完的进程）")
		}
	}

	// 宽限过后应当可以重启
	if !m.markStalled("t1", now.Add(RestartSettle+time.Second)) {
		t.Fatal("宽限已过且确认次数足够，应触发重启")
	}
}

// TestMarkStalledBlockedWhileRestarting 验证重启进行中不会重复触发。
func TestMarkStalledBlockedWhileRestarting(t *testing.T) {
	m := newTestMonitor()
	now := time.Now()
	m.startGraceIfNeeded("t1", now.Add(-time.Minute))
	m.entries["t1"].restarting = true

	for i := 0; i < StalledConfirm+3; i++ {
		if m.markStalled("t1", now) {
			t.Fatal("重启进行中不应再次触发")
		}
	}
}

// TestMarkStalledOneShotUntilReleased 验证触发一次后必须等 restarting 释放，
// 否则会连续重启同一条隧道。
func TestMarkStalledOneShotUntilReleased(t *testing.T) {
	m := newTestMonitor()
	now := time.Now()
	m.startGraceIfNeeded("t1", now.Add(-time.Minute))

	triggerAt := now.Add(RestartSettle)
	// 需要累积到 StalledConfirm 次才会触发
	for i := 1; i < StalledConfirm; i++ {
		if m.markStalled("t1", triggerAt) {
			t.Fatalf("第 %d 次就触发，应等到第 %d 次", i, StalledConfirm)
		}
	}
	if !m.markStalled("t1", triggerAt) {
		t.Fatal("达到确认次数后应触发")
	}
	if m.markStalled("t1", triggerAt.Add(time.Second)) {
		t.Fatal("restarting 未释放前不应再次触发")
	}

	// 模拟 restartTunnel 的 defer 释放
	m.mu.Lock()
	m.entries["t1"].restarting = false
	m.entries["t1"].graceUntil = triggerAt.Add(RestartSettle)
	m.entries["t1"].stalledStreak = 0
	m.mu.Unlock()

	if m.markStalled("t1", triggerAt.Add(time.Second)) {
		t.Fatal("新一轮宽限期内不应立即重启")
	}
}

// simulateRestartDone 模拟 restartTunnel 的 defer：解除标记、重设宽限、清空 stalled 计数。
func simulateRestartDone(m *healthMonitor, id string, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entryLocked(id)
	e.restarting = false
	e.graceUntil = at.Add(RestartSettle)
	e.stalledStreak = 0
}

// fire 把 stalled 计数推到阈值并返回是否触发重启。
// 注意要"任一调用返回真即为真"：重启可能在第一次调用时就触发并置位
// restarting，随后那次调用会被 restarting 拦住而返回 false。
func fire(m *healthMonitor, id string, at time.Time) bool {
	ok := false
	for i := 1; i < StalledConfirm; i++ {
		if m.markStalled(id, at) {
			ok = true
		}
	}
	if m.markStalled(id, at) {
		ok = true
	}
	return ok
}

// TestRestartBackoffSequence 验证退避梯度：
// 前两次立即重启（覆盖"重启一次就好"的常见情况），
// 第三次起拉长间隔，避免"重启解决不了"时每 18 秒无限重启。
func TestRestartBackoffSequence(t *testing.T) {
	m := newTestMonitor()
	base := time.Now()
	m.startGraceIfNeeded("t1", base.Add(-time.Minute))

	// 第 1 次：立即
	if !fire(m, "t1", base) {
		t.Fatal("第 1 次应立即可重启")
	}
	simulateRestartDone(m, "t1", base)

	// 第 2 次：仍立即（隔一个宽限周期后）
	t2 := base.Add(RestartSettle + 3*time.Second)
	if !fire(m, "t1", t2) {
		t.Fatal("第 2 次应仍可立即重启")
	}
	simulateRestartDone(m, "t1", t2)

	// 第 3 次：进入 60s 退避
	t3 := t2.Add(RestartSettle + 3*time.Second)
	if !fire(m, "t1", t3) {
		t.Fatal("第 3 次本身应被允许（退避是为下一次设置）")
	}
	simulateRestartDone(m, "t1", t3)

	// 宽限刚过就再来 → 应被退避拦住
	tooSoon := t3.Add(RestartSettle + 3*time.Second)
	if fire(m, "t1", tooSoon) {
		t.Fatalf("第 3 次之后应进入退避，%.0fs 后又重启了", tooSoon.Sub(t3).Seconds())
	}

	// 退避期满 → 允许
	afterBackoff := t3.Add(RestartBackoff[2] + time.Second)
	if !fire(m, "t1", afterBackoff) {
		t.Fatal("退避期满后应允许重启")
	}
}

// TestStableHealthyResetsBackoff 验证持续健康够久才清零退避，
// 短暂 healthy 不能清零（否则抖动会让退避失效）。
func TestStableHealthyResetsBackoff(t *testing.T) {
	m := newTestMonitor()
	base := time.Now()

	m.mu.Lock()
	e := m.entryLocked("t1")
	e.restartStreak = 4
	e.nextRestartAt = base.Add(15 * time.Minute)
	m.mu.Unlock()

	// 第一次 healthy：记录起点，但不足以清零
	m.apply("t1", HealthHealthy, 4, base, "")
	if got := m.entries["t1"].restartStreak; got != 4 {
		t.Fatalf("单次 healthy 就清零了退避（restartStreak=%d），会被抖动骗过去", got)
	}

	// 未满稳定期仍不清零
	m.apply("t1", HealthHealthy, 4, base.Add(HealthyStableFor/2), "")
	if got := m.entries["t1"].restartStreak; got != 4 {
		t.Fatalf("稳定期未满就清零了（restartStreak=%d）", got)
	}

	// 超过稳定期 → 清零
	m.apply("t1", HealthHealthy, 4, base.Add(HealthyStableFor+time.Second), "")
	if got := m.entries["t1"].restartStreak; got != 0 {
		t.Fatalf("持续健康 %s 后应清零退避，实际 restartStreak=%d", HealthyStableFor, got)
	}
	if !m.entries["t1"].nextRestartAt.IsZero() {
		t.Fatal("清零退避时应同时清空 nextRestartAt")
	}
}

// TestHealthFlapDoesNotResetBackoff 验证健康/断连反复抖动不会反复清零退避。
func TestHealthFlapDoesNotResetBackoff(t *testing.T) {
	m := newTestMonitor()
	base := time.Now()

	m.mu.Lock()
	m.entryLocked("t1").restartStreak = 3
	m.mu.Unlock()

	// healthy 10 秒 → stalled → healthy 10 秒 …… 反复
	at := base
	for i := 0; i < 5; i++ {
		m.apply("t1", HealthHealthy, 4, at, "")
		at = at.Add(10 * time.Second)
		m.apply("t1", HealthStalled, 0, at, "")
		at = at.Add(3 * time.Second)
	}

	if got := m.entries["t1"].restartStreak; got != 3 {
		t.Fatalf("抖动期间退避被清零（restartStreak=%d），应保持 3", got)
	}
}

// TestClassifyAPIError 锁定"查询失败 → 健康态"的映射矩阵。
//
// 这条分支在纯 WSL1 环境里无法做真实现场验证（iptables 不可用、
// /etc/hosts 只影响 DNS 不影响直连探测），但它直接决定会不会误重启，
// 所以必须用单测把每种组合钉死。
func TestClassifyAPIError(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		edgeOK   bool
		want     TunnelHealth
		wantText string
	}{
		{"权限不足+边缘可达", errors.New("403 Forbidden"), true, HealthUnknown, "凭据或权限不足"},
		{"权限不足+边缘不可达", errors.New("401 Unauthorized"), false, HealthUnknown, "凭据或权限不足"},
		{"隧道已删除", errors.New("404 Not Found"), true, HealthUnknown, "不存在"},
		{"超时+边缘可达→stalled", errors.New("dial tcp: i/o timeout"), true, HealthStalled, "edge_reachable=true"},
		{"超时+边缘不可达→blocked", errors.New("dial tcp: i/o timeout"), false, HealthBlocked, "edge_reachable=false"},
		{"DNS失败+边缘不可达→blocked", errors.New("no such host"), false, HealthBlocked, "edge_reachable=false"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyAPIError(tc.err, tc.edgeOK)
			if got.state != tc.want {
				t.Fatalf("state = %q，期望 %q", got.state, tc.want)
			}
			if !strings.Contains(got.detail, tc.wantText) {
				t.Fatalf("detail = %q，应包含 %q", got.detail, tc.wantText)
			}
			if got.connectors != -1 {
				t.Fatalf("查询失败时 connectors 应为 -1，实际 %d", got.connectors)
			}
		})
	}
}

// TestBlockedNeverCountsAsStalled 强调 blocked 与 stalled 必须互斥：
// 两者都表示"没连上"，但只有 stalled 允许重启。
func TestBlockedNeverCountsAsStalled(t *testing.T) {
	res := classifyAPIError(errors.New("dial tcp: i/o timeout"), false)
	if res.state == HealthStalled {
		t.Fatal("边缘不可达时不能判 stalled（会导致封锁期反复重启）")
	}
}

// TestNoLongThresholds 锁定"断连即重启"的策略：
// 若有人把确认次数或宽限改得过大，这里会失败。
func TestNoLongThresholds(t *testing.T) {
	if StalledConfirm > 3 {
		t.Errorf("StalledConfirm = %d，过大；要求断连后秒级反应", StalledConfirm)
	}
	if ProbeInterval > 10*time.Second {
		t.Errorf("ProbeInterval = %s，过大；要求接近实时监测", ProbeInterval)
	}
	if RestartSettle > 30*time.Second {
		t.Errorf("RestartSettle = %s，过大；这是等注册的宽限，不是冷却策略", RestartSettle)
	}
	if RestartSettle <= 0 {
		t.Error("RestartSettle 必须为正，否则会杀掉尚未注册完成的进程")
	}
}

// TestRecordRestartPrunesOldEntries 验证 24 小时窗口外的重启记录被裁掉。
func TestRecordRestartPrunesOldEntries(t *testing.T) {
	m := newTestMonitor()
	now := time.Now()

	m.recordRestart("t1", now.Add(-48*time.Hour))
	m.recordRestart("t1", now.Add(-25*time.Hour))
	m.recordRestart("t1", now.Add(-2*time.Hour))

	if snap := m.snapshot("t1"); snap.RestartCount24h != 1 {
		t.Fatalf("24h 内重启计数 = %d，期望 1", snap.RestartCount24h)
	}
}

// TestSnapshotUnknownForUntrackedTunnel 验证从未探测过的隧道返回 unknown 而非空结构。
func TestSnapshotUnknownForUntrackedTunnel(t *testing.T) {
	m := newTestMonitor()
	snap := m.snapshot("missing")

	if snap.Health != HealthUnknown {
		t.Fatalf("health = %q，期望 unknown", snap.Health)
	}
	if snap.Connectors != -1 {
		t.Fatalf("connectors = %d，期望 -1（未知）", snap.Connectors)
	}
	if snap.OutageSeconds != 0 {
		t.Fatalf("outage_seconds = %d，期望 0", snap.OutageSeconds)
	}
}

// TestSnapshotUnknownKeepsOutageZero unknown 表示"无法判定"，不是"中断"。
// 隧道本来就处于停止状态时，不该累计出一个天文数字的中断时长。
func TestSnapshotUnknownKeepsOutageZero(t *testing.T) {
	m := newTestMonitor()
	now := time.Now()

	m.apply("t1", HealthUnknown, -1, now.Add(-3*time.Hour), "")
	snap := m.snapshot("t1")

	if snap.Health != HealthUnknown {
		t.Fatalf("health = %q，期望 unknown", snap.Health)
	}
	if snap.OutageSeconds != 0 {
		t.Fatalf("unknown 状态不应累计中断时长，实际 %d 秒", snap.OutageSeconds)
	}
}

// TestSnapshotHealthyClearsOutage 验证恢复健康后中断时长归零。
func TestSnapshotHealthyClearsOutage(t *testing.T) {
	m := newTestMonitor()
	now := time.Now()

	m.apply("t1", HealthStalled, 0, now.Add(-5*time.Minute), "")
	if got := m.snapshot("t1").OutageSeconds; got <= 0 {
		t.Fatalf("stalled 中断时长应大于 0，实际 %d", got)
	}

	m.apply("t1", HealthHealthy, 4, now, "")
	healthy := m.snapshot("t1")

	if healthy.Health != HealthHealthy {
		t.Fatalf("health = %q，期望 healthy", healthy.Health)
	}
	if healthy.OutageSeconds != 0 {
		t.Fatalf("恢复后中断时长应归零，实际 %d", healthy.OutageSeconds)
	}
	if healthy.Connectors != 4 {
		t.Fatalf("connectors = %d，期望 4", healthy.Connectors)
	}
	if healthy.LastRegisteredAt == "" {
		t.Fatal("healthy 状态应写入 last_registered_at")
	}
}

// TestOverallPriority 验证多隧道时汇总取最紧急的一档。
func TestOverallPriority(t *testing.T) {
	now := time.Now()

	m := newTestMonitor()
	m.apply("a", HealthHealthy, 4, now, "")
	m.apply("b", HealthHealthy, 4, now, "")
	if got := m.overall().State; got != HealthHealthy {
		t.Fatalf("全部健康时 state = %q，期望 healthy", got)
	}

	m.apply("b", HealthBlocked, -1, now, "")
	if got := m.overall().State; got != HealthBlocked {
		t.Fatalf("存在 blocked 时 state = %q，期望 blocked", got)
	}

	// stalled 优先级高于 blocked：它能被自救，更该被关注
	m.apply("a", HealthStalled, 0, now, "")
	if got := m.overall().State; got != HealthStalled {
		t.Fatalf("存在 stalled 时 state = %q，期望 stalled", got)
	}
}

// TestOverallEmpty 验证没有隧道时返回 unknown 而不是 healthy（避免假绿）。
func TestOverallEmpty(t *testing.T) {
	m := newTestMonitor()
	if got := m.overall().State; got != HealthUnknown {
		t.Fatalf("无隧道时 state = %q，期望 unknown", got)
	}
}

// TestPruneRemovesStaleTunnels 验证配置里删掉的隧道不会残留在快照里。
func TestPruneRemovesStaleTunnels(t *testing.T) {
	m := newTestMonitor()
	now := time.Now()
	m.apply("keep", HealthHealthy, 1, now, "")
	m.apply("drop", HealthStalled, 0, now, "")

	m.prune(nil)
	if len(m.entries) != 0 {
		t.Fatalf("prune 后仍有 %d 条残留", len(m.entries))
	}
}

// TestApplyResetsStateSinceOnTransition 验证状态变化时才重置计时，
// 同状态重复探测不应刷新 stateSince。
func TestApplyResetsStateSinceOnTransition(t *testing.T) {
	m := newTestMonitor()
	base := time.Now()

	m.apply("t1", HealthStalled, 0, base, "")
	first := m.entries["t1"].stateSince

	m.apply("t1", HealthStalled, 0, base.Add(30*time.Second), "")
	if !m.entries["t1"].stateSince.Equal(first) {
		t.Fatal("同状态重复探测不应重置 stateSince")
	}

	m.apply("t1", HealthHealthy, 2, base.Add(60*time.Second), "")
	if !m.entries["t1"].stateSince.After(base) {
		t.Fatal("状态切换时应更新 stateSince")
	}
}
