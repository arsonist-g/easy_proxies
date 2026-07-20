// Package availability 统计每个节点（按 stable_id）的可用率。
//
// 可用率拆 probe（健康探测）与 call（真实转发调用）两路（ADR-0005）：
//
//	availability_rate = (probe_success + call_success) / (probe_total + call_total)
//
// 计数走无锁 atomic（ADR-0007），转发路径绝不触碰存储，仅低频管理读取时聚合。
package availability

import (
	"sync"
	"sync/atomic"
)

// counters 单个节点的 probe/call 计数，全部用 atomic，热路径无锁。
type counters struct {
	probeTotal   atomic.Int64
	probeSuccess atomic.Int64
	callTotal    atomic.Int64
	callSuccess  atomic.Int64
}

// Snapshot 是某个节点的可用率聚合视图（读取时计算）。
type Snapshot struct {
	ProbeTotal   int64   `json:"probe_total"`
	ProbeSuccess int64   `json:"probe_success"`
	CallTotal    int64   `json:"call_total"`
	CallSuccess  int64   `json:"call_success"`
	Total        int64   `json:"total_attempts"`  // probe_total + call_total
	Success      int64   `json:"total_success"`   // probe_success + call_success
	Rate         float64 `json:"availability_rate"` // success / total，total=0 时为 0
}

// Tracker 维护 stable_id -> *counters。写入走 atomic，仅新增节点时加写锁。
type Tracker struct {
	mu sync.RWMutex
	m  map[string]*counters
}

// New 构造 Tracker。
func New() *Tracker {
	return &Tracker{m: make(map[string]*counters)}
}

// c4 懒加载某节点的计数器（双检 + 写锁）。
func (t *Tracker) c4(stableID string) *counters {
	t.mu.RLock()
	c, ok := t.m[stableID]
	t.mu.RUnlock()
	if ok {
		return c
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	// 再查一次，防止并发时重复创建
	if c, ok := t.m[stableID]; ok {
		return c
	}
	c = &counters{}
	t.m[stableID] = c
	return c
}

// RecordProbe 记录一次健康探测结果。
func (t *Tracker) RecordProbe(stableID string, success bool) {
	if stableID == "" {
		return
	}
	c := t.c4(stableID)
	c.probeTotal.Add(1)
	if success {
		c.probeSuccess.Add(1)
	}
}

// RecordCall 记录一次真实转发调用结果（来自虚拟池/pool 转发路径）。
func (t *Tracker) RecordCall(stableID string, success bool) {
	if stableID == "" {
		return
	}
	c := t.c4(stableID)
	c.callTotal.Add(1)
	if success {
		c.callSuccess.Add(1)
	}
}

// Snapshot 返回单个节点的可用率聚合；不存在时返回零值。
func (t *Tracker) Snapshot(stableID string) Snapshot {
	t.mu.RLock()
	c := t.m[stableID]
	t.mu.RUnlock()
	if c == nil {
		return Snapshot{}
	}
	return snapshotOf(c)
}

// SnapshotAll 返回全部节点的可用率聚合。
func (t *Tracker) SnapshotAll() map[string]Snapshot {
	t.mu.RLock()
	out := make(map[string]Snapshot, len(t.m))
	for id, c := range t.m {
		out[id] = snapshotOf(c)
	}
	t.mu.RUnlock()
	return out
}

func snapshotOf(c *counters) Snapshot {
	pt := c.probeTotal.Load()
	ps := c.probeSuccess.Load()
	ct := c.callTotal.Load()
	cs := c.callSuccess.Load()
	total := pt + ct
	success := ps + cs
	var rate float64
	if total > 0 {
		rate = float64(success) / float64(total)
	}
	return Snapshot{
		ProbeTotal:   pt,
		ProbeSuccess: ps,
		CallTotal:    ct,
		CallSuccess:  cs,
		Total:        total,
		Success:      success,
		Rate:         rate,
	}
}
