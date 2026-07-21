// Package accesslog 提供代理访问日志的内存环形缓冲。
//
// 架构红线:转发路径绝不触碰 bbolt。访问日志热路径只写内存环形缓冲
// (make([]Entry, N) + head/count + Mutex,O(1) 写),管理 API 低频读取时持锁快照。
// 满足"看刚才谁在什么时间调了代理"的需求;持久化默认关闭(persist 接口预留,见 store 包)。
package accesslog

import (
	"sync"
	"sync/atomic"
	"time"
)

// Verdict 常量:allow 放行 / deny 拒绝。
const (
	VerdictAllow = "allow"
	VerdictDeny  = "deny"
)

// Entry 单条访问日志。
type Entry struct {
	Time     time.Time `json:"time"`              // 连接时间(UTC)
	SrcIP    string    `json:"src_ip"`            // 调用方源 IP
	Verdict  string    `json:"verdict"`           // allow / deny
	Reason   string    `json:"reason"`            // 放行/拒绝原因(如 "命中IP白名单"/"境外拒绝")
	Province string    `json:"province,omitempty"` // 命中的省份(GeoCN 归一化后)
	ISP      string    `json:"isp,omitempty"`     // 命中的运营商(GeoCN)
	NetType  string    `json:"net_type,omitempty"` // 网络类型(GeoCN,如 IDC/宽带/基站)
	Target   string    `json:"target,omitempty"`  // 目标地址(拒绝早于读请求时可能为空)
	Inbound  string    `json:"inbound"`           // 入口标识(如 "virtual-pool:US_Pool"、"pool")
}

// Logger 访问日志环形缓冲。线程安全;nil 接收者方法为 no-op(便于未启用时直接调用)。
type Logger struct {
	mu    sync.Mutex
	buf   []Entry
	head  int // 下一个写入位置
	count int // 已写入条数(<= cap)
}

// New 创建容量为 capacity 的日志缓冲。capacity<=0 时默认 10000。
func New(capacity int) *Logger {
	if capacity <= 0 {
		capacity = 10000
	}
	return &Logger{buf: make([]Entry, capacity)}
}

// Append 追加一条日志(热路径,O(1))。未填 Time 时补当前时间。
func (l *Logger) Append(e Entry) {
	if l == nil {
		return
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	l.mu.Lock()
	l.buf[l.head] = e
	l.head = (l.head + 1) % len(l.buf)
	if l.count < len(l.buf) {
		l.count++
	}
	l.mu.Unlock()
}

// Filter 查询过滤条件。零值=不过滤。
type Filter struct {
	SrcIP   string // 精确匹配源 IP(空=不限)
	Verdict string // 精确匹配结果 allow/deny(空=不限)
}

// matches 判断条目是否满足过滤条件。
func (f Filter) matches(e Entry) bool {
	if f.SrcIP != "" && e.SrcIP != f.SrcIP {
		return false
	}
	if f.Verdict != "" && e.Verdict != f.Verdict {
		return false
	}
	return true
}

// Recent 返回倒序(最新在前)且满足 filter 的日志快照。
// 持锁拷贝(管理查询低频,可接受);最多返回 max 条(max<=0 表示全部)。
func (l *Logger) Recent(filter Filter, max int) []Entry {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	out := make([]Entry, 0, 256)
	for i := 0; i < l.count; i++ {
		// head 是下一个写入位,故 head-1 是最新,逐个回退
		idx := (l.head - 1 - i + len(l.buf)) % len(l.buf)
		e := l.buf[idx]
		if filter.matches(e) {
			out = append(out, e)
			if max > 0 && len(out) >= max {
				break
			}
		}
	}
	l.mu.Unlock()
	return out
}

// Count 返回当前缓冲区内日志总数(供状态展示)。
func (l *Logger) Count() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.count
}

// --- 进程级默认 Logger（供代理入口热路径调用，与 accesscontrol 全局策略风格一致） ---

var defaultLogger atomic.Pointer[Logger]

// SetDefault 设置进程级默认 Logger（app 装配时调用一次）。
func SetDefault(l *Logger) {
	defaultLogger.Store(l)
}

// Default 返回进程级默认 Logger（未设置返回 nil）。
func Default() *Logger {
	return defaultLogger.Load()
}

// Record 按判定结果便捷追加一条访问日志到默认 Logger。未设置默认 Logger 时为 no-op。
// 用基本类型参数而非 accesscontrol.Decision，避免 accesslog→accesscontrol 循环依赖。
func Record(allowed bool, srcIP, reason, province, isp, netType, target, inbound string) {
	l := defaultLogger.Load()
	if l == nil {
		return
	}
	verdict := VerdictAllow
	if !allowed {
		verdict = VerdictDeny
	}
	l.Append(Entry{
		SrcIP:    srcIP,
		Verdict:  verdict,
		Reason:   reason,
		Province: province,
		ISP:      isp,
		NetType:  netType,
		Target:   target,
		Inbound:  inbound,
	})
}
