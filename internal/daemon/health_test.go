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

// TestShouldRestartRequiresThreshold 验证"卡死必须持续够久才重启"。
// 这条是防误杀的核心：cloudflared 刚启动时短暂 0 连接是正常的。
func TestShouldRestartRequiresThreshold(t *testing.T) {
	m := newTestMonitor()
	now := time.Now()
	m.apply("t1", HealthStalled, 0, now, "")

	if m.shouldRestart("t1", now.Add(StalledThreshold-time.Second)) {
		t.Fatalf("未达阈值就触发重启：阈值 %s", StalledThreshold)
	}
	if !m.shouldRestart("t1", now.Add(StalledThreshold+time.Second)) {
		t.Fatalf("已超过阈值却未触发重启：阈值 %s", StalledThreshold)
	}
}

// TestShouldRestartIgnoresHealthyAndBlocked 验证只有 stalled 才重启。
func TestShouldRestartIgnoresHealthyAndBlocked(t *testing.T) {
	now := time.Now()

	healthy := newTestMonitor()
	healthy.apply("t1", HealthHealthy, 4, now, "")
	if healthy.shouldRestart("t1", now.Add(time.Hour)) {
		t.Fatal("healthy 状态不应触发重启")
	}

	blocked := newTestMonitor()
	blocked.apply("t1", HealthBlocked, -1, now, "")
	if blocked.shouldRestart("t1", now.Add(time.Hour)) {
		t.Fatal("blocked 状态不应触发重启（外部阻断，重启无效）")
	}

	unknown := newTestMonitor()
	unknown.apply("t1", HealthUnknown, -1, now, "")
	if unknown.shouldRestart("t1", now.Add(time.Hour)) {
		t.Fatal("unknown 状态不应触发重启")
	}
}

// TestShouldRestartHonorsCooldown 验证冷却期内不重复重启，冷却过后允许。
func TestShouldRestartHonorsCooldown(t *testing.T) {
	m := newTestMonitor()
	now := time.Now()
	m.apply("t1", HealthStalled, 0, now, "")
	m.markRestart("t1", now)

	if m.shouldRestart("t1", now.Add(RestartCooldown-time.Minute)) {
		t.Fatalf("冷却期内仍然重启，冷却时间 %s", RestartCooldown)
	}
	if !m.shouldRestart("t1", now.Add(RestartCooldown+time.Minute)) {
		t.Fatal("冷却期已过却仍不重启")
	}
}

// TestMarkRestartPrunesOldEntries 验证 24 小时窗口外的重启记录被裁掉。
func TestMarkRestartPrunesOldEntries(t *testing.T) {
	m := newTestMonitor()
	now := time.Now()

	m.markRestart("t1", now.Add(-48*time.Hour))
	m.markRestart("t1", now.Add(-25*time.Hour))
	m.markRestart("t1", now.Add(-2*time.Hour))

	// 冷却判断只看最近一次（-2h），但窗口裁剪应在 markRestart 内完成
	snap := m.snapshot("t1")
	if snap.RestartCount24h != 1 {
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

// TestSnapshotHealthyClearsOutage 验证恢复健康后中断时长归零。
func TestSnapshotHealthyClearsOutage(t *testing.T) {
	m := newTestMonitor()
	now := time.Now()

	m.apply("t1", HealthStalled, 0, now.Add(-5*time.Minute), "")
	stalled := m.snapshot("t1")
	if stalled.OutageSeconds <= 0 {
		t.Fatalf("stalled 中断时长应大于 0，实际 %d", stalled.OutageSeconds)
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

	m.prune(nil) // 全部清掉
	if len(m.entries) != 0 {
		t.Fatalf("prune 后仍有 %d 条残留", len(m.entries))
	}
}

// TestSnapshotUnknownKeepsOutageZero unknown 表示"无法判定"，不是"中断"。
// 例如隧道本来就处于停止状态时，不该累计出一个天文数字的中断时长。
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

// TestApplyResetsStateSinceOnTransition 验证状态变化时才重置计时，
// 同状态重复探测不应刷新 stateSince（否则阈值永远达不到）。
func TestApplyResetsStateSinceOnTransition(t *testing.T) {
	m := newTestMonitor()
	base := time.Now()

	m.apply("t1", HealthStalled, 0, base, "")
	first := m.entries["t1"].stateSince

	m.apply("t1", HealthStalled, 0, base.Add(30*time.Second), "")
	if !m.entries["t1"].stateSince.Equal(first) {
		t.Fatal("同状态重复探测不应重置 stateSince，否则永远无法达到重启阈值")
	}

	m.apply("t1", HealthHealthy, 2, base.Add(60*time.Second), "")
	if !m.entries["t1"].stateSince.After(base) {
		t.Fatal("状态切换时应更新 stateSince")
	}
}
