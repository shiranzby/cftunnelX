package daemon

import (
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
