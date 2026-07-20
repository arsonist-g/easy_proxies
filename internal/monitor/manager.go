package monitor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	M "github.com/sagernet/sing/common/metadata"

	"easy_proxies/internal/availability"
)

// Config mirrors user settings needed by the monitoring server.
type Config struct {
	Enabled        bool
	Listen         string
	ProbeTarget    string
	Password       string
	PathPwd        string // 路径密码，访问管理页面需要通过 /路径密码 访问，为空则使用默认路径 /
	ProxyUsername  string // 代理池的用户名（用于导出）
	ProxyPassword  string // 代理池的密码（用于导出）
	ExternalIP     string // 外部 IP 地址，用于导出时替换 0.0.0.0
	SkipCertVerify bool   // 全局跳过 SSL 证书验证
}

// NodeInfo is static metadata about a proxy entry.
type NodeInfo struct {
	Tag           string `json:"tag"`
	StableID      string `json:"stable_id,omitempty"` // 跨刷新稳定主键（data-model §1.4）
	Name          string `json:"name"`
	URI           string `json:"uri"`
	Mode          string `json:"mode"`
	ListenAddress string `json:"listen_address,omitempty"`
	Port          uint16 `json:"port,omitempty"`
	Username      string `json:"username,omitempty"` // 代理用户名
	Password      string `json:"password,omitempty"` // 代理密码
}

// Snapshot is a runtime view of a proxy node.
type Snapshot struct {
	NodeInfo
	FailureCount      int           `json:"failure_count"`
	Blacklisted       bool          `json:"blacklisted"`
	BlacklistedUntil  time.Time     `json:"blacklisted_until"`
	ActiveConnections int32         `json:"active_connections"`
	LastError         string        `json:"last_error,omitempty"`
	LastFailure       time.Time     `json:"last_failure,omitempty"`
	LastSuccess       time.Time     `json:"last_success,omitempty"`
	LastProbeLatency  time.Duration `json:"last_probe_latency,omitempty"`
	LastLatencyMs     int64         `json:"last_latency_ms"`
	Available         bool          `json:"available"`          // 节点是否可用
	InitialCheckDone  bool          `json:"initial_check_done"` // 初始检查是否完成

	// 国家/出口（国家探测后回填，见 internal/countryprobe）
	ExitIP      string `json:"exit_ip,omitempty"`
	CountryCode string `json:"country_code,omitempty"`
	CountryName string `json:"country_name,omitempty"`
	// ASN（本地 GeoLite2-ASN 查询，可选；未配置 geoip 则留空）
	ASN    uint   `json:"asn,omitempty"`
	ASNOrg string `json:"asn_org,omitempty"`

	// 去重（跨订阅按 exit_ip 去重，duplicate_of 指向留存节点 stable_id）
	DuplicateOf string `json:"duplicate_of,omitempty"`

	// 可用率（来自 internal/availability Tracker，由 Manager 合并）
	AvailabilityRate float64 `json:"availability_rate"`
	TotalAttempts    int64   `json:"total_attempts"` // probe_total + call_total
	TotalSuccess     int64   `json:"total_success"`  // probe_success + call_success
	CallTotal        int64   `json:"call_total"`     // 真实转发调用次数（dashboard 单独透出）
}

type probeFunc func(ctx context.Context) (time.Duration, error)
type releaseFunc func()

type EntryHandle struct {
	ref *entry
}

type entry struct {
	info             NodeInfo
	failure          int
	blacklist        bool
	until            time.Time
	lastError        string
	lastFail         time.Time
	lastOK           time.Time
	lastProbe        time.Duration
	active           atomic.Int32
	probe            probeFunc
	release          releaseFunc
	initialCheckDone bool // 初始健康检查是否完成
	available        bool // 节点是否可用（初始检查通过）
	exitIP           string // 国家探测得到的出口真实 IP
	countryCode      string
	countryName      string
	asn              uint // 本地 GeoLite2-ASN 查询（可选）
	asnOrg           string
	duplicateOf      string // 去重：指向留存节点的 stable_id
	mu               sync.RWMutex
}

// Manager aggregates all node states for the UI/API.
type Manager struct {
	cfg        Config
	probeDst   M.Socksaddr
	probeReady bool
	mu         sync.RWMutex
	nodes      map[string]*entry
	ctx        context.Context
	cancel     context.CancelFunc
	logger     Logger
	tracker    *availability.Tracker // 可用率统计（转发/探测计数），可空
}

// Logger interface for logging
type Logger interface {
	Info(args ...any)
	Warn(args ...any)
}

// NewManager constructs a manager and pre-validates the probe target.
func NewManager(cfg Config) (*Manager, error) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		cfg:    cfg,
		nodes:  make(map[string]*entry),
		ctx:    ctx,
		cancel: cancel,
	}
	if cfg.ProbeTarget != "" {
		target := cfg.ProbeTarget
		// Strip URL scheme if present (e.g., "https://www.google.com:443" -> "www.google.com:443")
		if strings.HasPrefix(target, "https://") {
			target = strings.TrimPrefix(target, "https://")
		} else if strings.HasPrefix(target, "http://") {
			target = strings.TrimPrefix(target, "http://")
		}
		// Remove trailing path if present
		if idx := strings.Index(target, "/"); idx != -1 {
			target = target[:idx]
		}
		host, port, err := net.SplitHostPort(target)
		if err != nil {
			// If no port specified, use default based on original scheme
			if strings.HasPrefix(cfg.ProbeTarget, "https://") {
				host = target
				port = "443"
			} else {
				host = target
				port = "80"
			}
		}
		parsed := M.ParseSocksaddrHostPort(host, parsePort(port))
		m.probeDst = parsed
		m.probeReady = true
	}
	return m, nil
}

// SetLogger sets the logger for the manager.
func (m *Manager) SetLogger(logger Logger) {
	m.logger = logger
}

// SetTracker 注入可用率统计器，供 Snapshot 合并 probe/call 计数。
func (m *Manager) SetTracker(t *availability.Tracker) {
	m.tracker = t
}

// Tracker 返回可用率统计器（转发/探测路径调用，可能为 nil）。
func (m *Manager) Tracker() *availability.Tracker {
	return m.tracker
}

// StartPeriodicHealthCheck starts a background goroutine that periodically checks all nodes.
// interval: how often to check (e.g., 30 * time.Second)
// timeout: timeout for each probe (e.g., 10 * time.Second)
func (m *Manager) StartPeriodicHealthCheck(interval, timeout time.Duration) {
	if !m.probeReady {
		if m.logger != nil {
			m.logger.Warn("probe target not configured, periodic health check disabled")
		}
		return
	}

	go func() {
		// 启动后立即进行一次检查
		m.probeAllNodes(timeout)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-m.ctx.Done():
				return
			case <-ticker.C:
				m.probeAllNodes(timeout)
			}
		}
	}()

	if m.logger != nil {
		m.logger.Info("periodic health check started, interval: ", interval)
	}
}

// probeAllNodes checks all registered nodes concurrently.
func (m *Manager) probeAllNodes(timeout time.Duration) {
	m.mu.RLock()
	entries := make([]*entry, 0, len(m.nodes))
	for _, e := range m.nodes {
		entries = append(entries, e)
	}
	m.mu.RUnlock()

	if len(entries) == 0 {
		return
	}

	if m.logger != nil {
		m.logger.Info("starting health check for ", len(entries), " nodes")
	}

	workerLimit := runtime.NumCPU() * 2
	if workerLimit < 8 {
		workerLimit = 8
	}
	sem := make(chan struct{}, workerLimit)
	var wg sync.WaitGroup
	var availableCount atomic.Int32
	var failedCount atomic.Int32

	for _, e := range entries {
		e.mu.RLock()
		probeFn := e.probe
		tag := e.info.Tag
		e.mu.RUnlock()

		if probeFn == nil {
			continue
		}

		sem <- struct{}{}
		wg.Add(1)
		go func(entry *entry, probe probeFunc, tag string) {
			defer wg.Done()
			defer func() { <-sem }()

			ctx, cancel := context.WithTimeout(m.ctx, timeout)
			latency, err := probe(ctx)
			cancel()

			entry.mu.Lock()
			if err != nil {
				failedCount.Add(1)
				entry.lastError = err.Error()
				entry.lastFail = time.Now()
				entry.available = false
				entry.initialCheckDone = true
			} else {
				availableCount.Add(1)
				entry.lastOK = time.Now()
				entry.lastProbe = latency
				entry.available = true
				entry.initialCheckDone = true
			}
			sid := entry.info.StableID
			entry.mu.Unlock()

			// 记录可用率（probe 路），无锁 atomic，不阻塞热路径
			if m.tracker != nil && sid != "" {
				m.tracker.RecordProbe(sid, err == nil)
			}

			if err != nil && m.logger != nil {
				m.logger.Warn("probe failed for ", tag, ": ", err)
			}
		}(e, probeFn, tag)
	}
	wg.Wait()

	if m.logger != nil {
		m.logger.Info("health check completed: ", availableCount.Load(), " available, ", failedCount.Load(), " failed")
	}

	// 周期去重：按出口 IP 标记重复节点（countryprobe 回填 exit_ip 后逐步收敛）
	m.DedupByExitIP()
}

// ProbeAll 触发一次全节点健康探测（API POST /probe/all 用）。
func (m *Manager) ProbeAll(timeout time.Duration) {
	m.probeAllNodes(timeout)
}

// Stop stops the periodic health check.
func (m *Manager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
}

// Reset 清空已注册节点（box 重建前由 boxmgr 调用）。
// 新 box 启动时会经 pool 重新 Register 当前节点集合，避免删除/变更的节点残留 Snapshot。
// 不清 availability Tracker：可用率按 stable_id 累积，跨 reload 保留（同公式同 key）。
func (m *Manager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nodes = make(map[string]*entry)
}

func parsePort(value string) uint16 {
	p, err := strconv.Atoi(value)
	if err != nil || p <= 0 || p > 65535 {
		return 80
	}
	return uint16(p)
}

// Register ensures a node is tracked and returns its entry.
func (m *Manager) Register(info NodeInfo) *EntryHandle {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.nodes[info.Tag]
	if !ok {
		e = &entry{info: info}
		m.nodes[info.Tag] = e
	} else {
		e.info = info
	}
	return &EntryHandle{ref: e}
}

// DestinationForProbe exposes the configured destination for health checks.
func (m *Manager) DestinationForProbe() (M.Socksaddr, bool) {
	if !m.probeReady {
		return M.Socksaddr{}, false
	}
	return m.probeDst, true
}

// Snapshot returns a sorted copy of current node states.
// If onlyAvailable is true, only returns nodes that passed initial health check.
func (m *Manager) Snapshot() []Snapshot {
	return m.SnapshotFiltered(false)
}

// SnapshotFiltered returns a sorted copy of current node states.
// If onlyAvailable is true, only returns nodes that passed initial health check.
// Nodes that haven't been checked yet are also included (they will be checked on first use).
func (m *Manager) SnapshotFiltered(onlyAvailable bool) []Snapshot {
	m.mu.RLock()
	list := make([]*entry, 0, len(m.nodes))
	for _, e := range m.nodes {
		list = append(list, e)
	}
	m.mu.RUnlock()
	snapshots := make([]Snapshot, 0, len(list))
	for _, e := range list {
		snap := e.snapshot()
		// 合并可用率统计（probe/call 计数），按 stable_id 查 Tracker
		if m.tracker != nil && snap.StableID != "" {
			av := m.tracker.Snapshot(snap.StableID)
			snap.AvailabilityRate = av.Rate
			snap.TotalAttempts = av.Total
			snap.TotalSuccess = av.Success
			snap.CallTotal = av.CallTotal
		}
		// 如果只要可用节点：
		// - 跳过已完成检查但不可用的节点
		// - 保留未完成检查的节点（它们会在首次使用时被检查）
		if onlyAvailable && snap.InitialCheckDone && !snap.Available {
			continue
		}
		snapshots = append(snapshots, snap)
	}
	// 按延迟排序（延迟小的在前面，未测试的排在最后）
	sort.Slice(snapshots, func(i, j int) bool {
		latencyI := snapshots[i].LastLatencyMs
		latencyJ := snapshots[j].LastLatencyMs
		// -1 表示未测试，排在最后
		if latencyI < 0 && latencyJ < 0 {
			return snapshots[i].Name < snapshots[j].Name // 都未测试时按名称排序
		}
		if latencyI < 0 {
			return false // i 未测试，排在后面
		}
		if latencyJ < 0 {
			return true // j 未测试，i 排在前面
		}
		if latencyI == latencyJ {
			return snapshots[i].Name < snapshots[j].Name // 延迟相同时按名称排序
		}
		return latencyI < latencyJ
	})
	return snapshots
}

// Probe triggers a manual health check.
func (m *Manager) Probe(ctx context.Context, tag string) (time.Duration, error) {
	e, err := m.entry(tag)
	if err != nil {
		return 0, err
	}
	if e.probe == nil {
		return 0, errors.New("probe not available for this node")
	}
	latency, err := e.probe(ctx)
	if err != nil {
		return 0, err
	}
	e.recordProbeLatency(latency)
	return latency, nil
}

// Release clears blacklist state for the given node.
func (m *Manager) Release(tag string) error {
	e, err := m.entry(tag)
	if err != nil {
		return err
	}
	if e.release == nil {
		return errors.New("release not available for this node")
	}
	e.release()
	return nil
}

func (m *Manager) entry(tag string) (*entry, error) {
	m.mu.RLock()
	e, ok := m.nodes[tag]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("node %s not found", tag)
	}
	return e, nil
}

func (e *entry) snapshot() Snapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()

	latencyMs := int64(-1)
	if e.lastProbe > 0 {
		latencyMs = e.lastProbe.Milliseconds()
		if latencyMs == 0 {
			latencyMs = 1 // Round up sub-millisecond latencies to 1ms
		}
	}

	return Snapshot{
		NodeInfo:          e.info,
		FailureCount:      e.failure,
		Blacklisted:       e.blacklist,
		BlacklistedUntil:  e.until,
		ActiveConnections: e.active.Load(),
		LastError:         e.lastError,
		LastFailure:       e.lastFail,
		LastSuccess:       e.lastOK,
		LastProbeLatency:  e.lastProbe,
		LastLatencyMs:     latencyMs,
		Available:         e.available,
		InitialCheckDone:  e.initialCheckDone,
		ExitIP:            e.exitIP,
		CountryCode:       e.countryCode,
		CountryName:       e.countryName,
		ASN:               e.asn,
		ASNOrg:            e.asnOrg,
		DuplicateOf:       e.duplicateOf,
	}
}

func (e *entry) recordFailure(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failure++
	e.lastError = err.Error()
	e.lastFail = time.Now()
}

func (e *entry) recordSuccess() {
	e.mu.Lock()
	e.lastOK = time.Now()
	e.mu.Unlock()
}

func (e *entry) blacklistUntil(until time.Time) {
	e.mu.Lock()
	e.blacklist = true
	e.until = until
	e.mu.Unlock()
}

func (e *entry) clearBlacklist() {
	e.mu.Lock()
	e.blacklist = false
	e.until = time.Time{}
	e.mu.Unlock()
}

func (e *entry) incActive() {
	e.active.Add(1)
}

func (e *entry) decActive() {
	e.active.Add(-1)
}

func (e *entry) setProbe(fn probeFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.probe = fn
}

func (e *entry) setRelease(fn releaseFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.release = fn
}

func (e *entry) recordProbeLatency(d time.Duration) {
	e.mu.Lock()
	e.lastProbe = d
	e.mu.Unlock()
}

// RecordFailure updates failure counters.
func (h *EntryHandle) RecordFailure(err error) {
	if h == nil || h.ref == nil {
		return
	}
	h.ref.recordFailure(err)
}

// RecordSuccess updates the last success timestamp.
func (h *EntryHandle) RecordSuccess() {
	if h == nil || h.ref == nil {
		return
	}
	h.ref.recordSuccess()
}

// RecordSuccessWithLatency updates the last success timestamp and latency.
func (h *EntryHandle) RecordSuccessWithLatency(latency time.Duration) {
	if h == nil || h.ref == nil {
		return
	}
	h.ref.recordSuccess()
	h.ref.recordProbeLatency(latency)
}

// Blacklist marks the node unavailable until the given deadline.
func (h *EntryHandle) Blacklist(until time.Time) {
	if h == nil || h.ref == nil {
		return
	}
	h.ref.blacklistUntil(until)
}

// ClearBlacklist removes the blacklist flag.
func (h *EntryHandle) ClearBlacklist() {
	if h == nil || h.ref == nil {
		return
	}
	h.ref.clearBlacklist()
}

// IncActive increments the active connection counter.
func (h *EntryHandle) IncActive() {
	if h == nil || h.ref == nil {
		return
	}
	h.ref.incActive()
}

// DecActive decrements the active connection counter.
func (h *EntryHandle) DecActive() {
	if h == nil || h.ref == nil {
		return
	}
	h.ref.decActive()
}

// SetProbe assigns a probe function.
func (h *EntryHandle) SetProbe(fn func(ctx context.Context) (time.Duration, error)) {
	if h == nil || h.ref == nil {
		return
	}
	h.ref.setProbe(fn)
}

// SetRelease assigns a release function.
func (h *EntryHandle) SetRelease(fn func()) {
	if h == nil || h.ref == nil {
		return
	}
	h.ref.setRelease(fn)
}

// MarkInitialCheckDone marks the initial health check as completed.
func (h *EntryHandle) MarkInitialCheckDone(available bool) {
	if h == nil || h.ref == nil {
		return
	}
	h.ref.mu.Lock()
	h.ref.initialCheckDone = true
	h.ref.available = available
	h.ref.mu.Unlock()
}

// MarkAvailable updates the availability status.
func (h *EntryHandle) MarkAvailable(available bool) {
	if h == nil || h.ref == nil {
		return
	}
	h.ref.mu.Lock()
	h.ref.available = available
	h.ref.mu.Unlock()
}

// SetCountry 回填国家探测结果（出口 IP / 国家码 / 国名）。
func (h *EntryHandle) SetCountry(exitIP, code, name string) {
	if h == nil || h.ref == nil {
		return
	}
	h.ref.mu.Lock()
	defer h.ref.mu.Unlock()
	h.ref.exitIP = exitIP
	h.ref.countryCode = code
	h.ref.countryName = name
}

// SetASN 回填本地 GeoLite2-ASN 查询结果（ASN / 组织名）。asn=0 且 org="" 表示未取到。
func (h *EntryHandle) SetASN(asn uint, org string) {
	if h == nil || h.ref == nil {
		return
	}
	h.ref.mu.Lock()
	defer h.ref.mu.Unlock()
	h.ref.asn = asn
	h.ref.asnOrg = org
}

// SetDuplicateOf 标记去重指向（duplicate_of 指向留存节点 stable_id）。
func (h *EntryHandle) SetDuplicateOf(stableID string) {
	if h == nil || h.ref == nil {
		return
	}
	h.ref.mu.Lock()
	defer h.ref.mu.Unlock()
	h.ref.duplicateOf = stableID
}

// RecordProbeAvailability 记一次健康探测结果到可用率统计（probe 路）。
func (h *EntryHandle) RecordProbeAvailability(tracker *availability.Tracker, success bool) {
	if h == nil || h.ref == nil || tracker == nil {
		return
	}
	tracker.RecordProbe(h.ref.info.StableID, success)
}

// SnapshotByStableID 按 stable_id 查找单个节点快照（含可用率合并）。
func (m *Manager) SnapshotByStableID(stableID string) (Snapshot, bool) {
	m.mu.RLock()
	var found *entry
	for _, e := range m.nodes {
		if e.info.StableID == stableID {
			found = e
			break
		}
	}
	m.mu.RUnlock()
	if found == nil {
		return Snapshot{}, false
	}
	snap := found.snapshot()
	if m.tracker != nil && snap.StableID != "" {
		av := m.tracker.Snapshot(snap.StableID)
		snap.AvailabilityRate = av.Rate
		snap.TotalAttempts = av.Total
		snap.TotalSuccess = av.Success
		snap.CallTotal = av.CallTotal
	}
	return snap, true
}

// ProbeByStableID 按 stable_id 触发单节点探测（API 端点用，内部映射 stable_id→tag）。
func (m *Manager) ProbeByStableID(ctx context.Context, stableID string) (time.Duration, error) {
	m.mu.RLock()
	var tag string
	found := false
	for t, e := range m.nodes {
		if e.info.StableID == stableID {
			tag = t
			found = true
			break
		}
	}
	m.mu.RUnlock()
	if !found {
		return 0, fmt.Errorf("node %s not found", stableID)
	}
	return m.Probe(ctx, tag)
}

// DedupByExitIP 跨订阅按出口 IP 去重：同 exit_ip 的节点保留"可用优先、延迟低优先"的最优者，
// 其余标记 duplicate_of 指向最优节点 stable_id。exit_ip 为空（未探测）的节点不参与去重。
// 由周期健康检查触发；随 countryprobe 回填 exit_ip 逐步收敛。
func (m *Manager) DedupByExitIP() {
	type ei struct {
		stableID string
		exitIP   string
		latency  int64 // -1 未测
		avail    bool
	}

	m.mu.RLock()
	snap := make([]ei, 0, len(m.nodes))
	for _, e := range m.nodes {
		e.mu.RLock()
		latMs := int64(-1)
		if e.lastProbe > 0 {
			latMs = e.lastProbe.Milliseconds()
		}
		snap = append(snap, ei{e.info.StableID, e.exitIP, latMs, e.available})
		e.mu.RUnlock()
	}
	m.mu.RUnlock()

	better := func(a, b ei) bool {
		if a.avail != b.avail {
			return a.avail // 可用优于不可用
		}
		la, lb := a.latency, b.latency
		if la < 0 {
			la = 1 << 62
		}
		if lb < 0 {
			lb = 1 << 62
		}
		if la != lb {
			return la < lb
		}
		return a.stableID < b.stableID // 确定性兜底
	}

	// 每个 exit_ip 选最优
	best := make(map[string]ei)
	for _, in := range snap {
		if in.exitIP == "" || in.stableID == "" {
			continue
		}
		if cur, ok := best[in.exitIP]; !ok || better(in, cur) {
			best[in.exitIP] = in
		}
	}

	// 写回 duplicate_of
	m.mu.RLock()
	for _, e := range m.nodes {
		e.mu.RLock()
		ip := e.exitIP
		sid := e.info.StableID
		e.mu.RUnlock()
		var dup string
		if ip != "" && sid != "" {
			if b, ok := best[ip]; ok && b.stableID != sid {
				dup = b.stableID
			}
		}
		e.mu.Lock()
		e.duplicateOf = dup
		e.mu.Unlock()
	}
	m.mu.RUnlock()
}
