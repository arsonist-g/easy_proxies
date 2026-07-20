package subscription

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"easy_proxies/internal/boxmgr"
	"easy_proxies/internal/config"
	"easy_proxies/internal/logger"
	"easy_proxies/internal/monitor"
	"easy_proxies/internal/store"
)

// Logger defines logging interface.
type Logger interface {
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
}

// Option configures the Manager.
type Option func(*Manager)

// WithLogger sets a custom logger.
func WithLogger(l Logger) Option {
	return func(m *Manager) { m.logger = l }
}

// WithStore 注入 bbolt 存储（订阅实体/状态）。提供后订阅列表与状态走 bbolt；
// 未提供则回退到 config.Subscriptions（兼容旧配置，仅全量刷新）。
func WithStore(s *store.Store) Option {
	return func(m *Manager) { m.store = s }
}

// Manager handles periodic subscription refresh.
type Manager struct {
	mu sync.RWMutex

	baseCfg *config.Config
	boxMgr  *boxmgr.Manager
	store   *store.Store // bbolt 订阅实体/状态（可空：兼容旧 config.Subscriptions）
	logger  Logger

	status        monitor.SubscriptionStatus
	ctx           context.Context
	cancel        context.CancelFunc
	refreshMu     sync.Mutex // prevents concurrent refreshes
	manualRefresh chan struct{}

	// Track nodes.txt content hash to detect modifications
	lastSubHash      string    // Hash of nodes.txt content after last subscription refresh
	lastNodesModTime time.Time // Last known modification time of nodes.txt
}

// New creates a SubscriptionManager.
func New(cfg *config.Config, boxMgr *boxmgr.Manager, opts ...Option) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		baseCfg:       cfg,
		boxMgr:        boxMgr,
		ctx:           ctx,
		cancel:        cancel,
		manualRefresh: make(chan struct{}, 1),
	}
	for _, opt := range opts {
		opt(m)
	}
	if m.logger == nil {
		m.logger = defaultLogger{}
	}
	return m
}

// SyncSubscriptions 双向同步 config.yaml 与 bbolt 的订阅定义。
// yaml 为权威源（用户可编辑 name/url）：yaml 有而 bbolt 无的 → upsert；
// bbolt 有而 yaml 无的 → 删除（用户从文件移除即生效）。
// 特例：yaml 为空但 bbolt 非空（首次升级/纯 API 管理）→ 把 bbolt 提升写回 yaml，不删除，避免误清。
// 运行时状态（刷新状态/节点数/时间戳）始终留在 bbolt，按 URL 关联保留。
func SyncSubscriptions(cfg *config.Config, st *store.Store) error {
	if st == nil {
		return nil
	}
	yamlNames := make(map[string]string, len(cfg.Subscriptions)) // url -> name
	for _, entry := range cfg.Subscriptions {
		name, u := parseSubscriptionEntry(entry)
		if u == "" {
			continue
		}
		yamlNames[u] = name
	}

	boltSubs, err := st.ListSubscriptions()
	if err != nil {
		return fmt.Errorf("list subscriptions: %w", err)
	}

	// 特例：yaml 空 + bbolt 非空 → 提升 bbolt 到 yaml，不删除
	if len(yamlNames) == 0 && len(boltSubs) > 0 {
		cfg.Subscriptions = store.SubscriptionEntries(boltSubs)
		if err := cfg.SaveSubscriptions(); err != nil {
			return fmt.Errorf("promote subscriptions to yaml: %w", err)
		}
		return nil
	}

	// yaml 权威：upsert yaml 的（已存在则同步 name，状态保留）
	for u, name := range yamlNames {
		existing, err := st.FindSubscriptionByURL(u)
		if err != nil {
			return fmt.Errorf("find subscription %s: %w", u, err)
		}
		if existing != nil {
			if name != "" && existing.Name != name {
				existing.Name = name
				if err := st.UpdateSubscription(existing); err != nil {
					return fmt.Errorf("update subscription %s: %w", u, err)
				}
			}
			continue
		}
		if _, err := st.CreateSubscription(name, u, ""); err != nil {
			return fmt.Errorf("create subscription %s: %w", u, err)
		}
	}
	// yaml 没有的 bbolt 订阅 → 删除（用户从文件移除即生效）
	for _, s := range boltSubs {
		if _, ok := yamlNames[s.URL]; !ok {
			if err := st.DeleteSubscription(s.ID); err != nil {
				return fmt.Errorf("delete orphan subscription %s: %w", s.URL, err)
			}
		}
	}
	return nil
}

// Start 启动刷新循环。即使 subscription_refresh.enabled=false 或启动时无订阅也启动循环，
// 以保证运行时经 API 新增订阅后，手动刷新（manualRefresh 信号）始终可用；
// 定时 ticker 仅在 enabled=true 时生效（refreshLoop 内用 nil-channel 模式控制）。
func (m *Manager) Start() {
	interval := m.baseCfg.SubscriptionRefresh.Interval
	if interval <= 0 {
		interval = time.Hour
	}
	go m.refreshLoop(interval)
}

// Stop stops the periodic refresh.
func (m *Manager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
}

// RefreshNow triggers an immediate refresh.
func (m *Manager) RefreshNow() error {
	select {
	case m.manualRefresh <- struct{}{}:
	default:
		// Already a refresh pending
	}

	// Wait for refresh to complete or timeout
	timeout := m.baseCfg.SubscriptionRefresh.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(m.ctx, timeout+m.baseCfg.SubscriptionRefresh.HealthCheckTimeout)
	defer cancel()

	// Poll status until refresh completes
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	startCount := m.Status().RefreshCount
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("refresh timeout")
		case <-ticker.C:
			status := m.Status()
			if status.RefreshCount > startCount {
				if status.LastError != "" {
					return fmt.Errorf("refresh failed: %s", status.LastError)
				}
				return nil
			}
		}
	}
}

// RefreshOne 刷新单个订阅（POST /subscriptions/{id}/refresh）。
// 当前实现触发一次完整刷新，以保证手动节点保留 + 跨订阅去重一致；
// 单订阅增量合并（仅替换该订阅节点）留待后续优化。
func (m *Manager) RefreshOne(subID uint64) error {
	if m.store != nil && subID != 0 {
		if sub, err := m.store.GetSubscription(subID); err != nil || sub == nil {
			return fmt.Errorf("订阅 %d 不存在", subID)
		}
	}
	return m.RefreshNow()
}

// Status returns the current refresh status.
func (m *Manager) Status() monitor.SubscriptionStatus {
	m.mu.RLock()
	status := m.status
	m.mu.RUnlock()

	// Check if nodes have been modified since last refresh
	status.NodesModified = m.CheckNodesModified()
	return status
}

// refreshLoop 消费手动刷新信号；定时 ticker 仅在 enabled=true 时创建并生效。
// enabled=false 时 tickCh 为 nil channel，select 该 case 永久阻塞，只剩手动刷新与退出信号——
// 这样手动刷新始终可用，不受 enabled 开关影响。
func (m *Manager) refreshLoop(interval time.Duration) {
	var ticker *time.Ticker
	var tickCh <-chan time.Time
	if m.baseCfg.SubscriptionRefresh.Enabled {
		ticker = time.NewTicker(interval)
		defer ticker.Stop()
		tickCh = ticker.C
		m.mu.Lock()
		m.status.NextRefresh = time.Now().Add(interval)
		m.mu.Unlock()
		m.logger.Infof("subscription periodic refresh started, interval: %s", interval)
	} else {
		m.logger.Infof("subscription periodic refresh idle (disabled); manual refresh still available")
	}

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-tickCh:
			m.doRefresh()
			m.mu.Lock()
			m.status.NextRefresh = time.Now().Add(interval)
			m.mu.Unlock()
		case <-m.manualRefresh:
			m.doRefresh()
			if ticker != nil {
				ticker.Reset(interval)
				m.mu.Lock()
				m.status.NextRefresh = time.Now().Add(interval)
				m.mu.Unlock()
			}
		}
	}
}

// doRefresh performs a single refresh operation.
func (m *Manager) doRefresh() {
	// Prevent concurrent refreshes
	if !m.refreshMu.TryLock() {
		m.logger.Warnf("refresh already in progress, skipping")
		return
	}
	defer m.refreshMu.Unlock()

	m.mu.Lock()
	m.status.IsRefreshing = true
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.status.IsRefreshing = false
		m.status.RefreshCount++
		m.mu.Unlock()
	}()

	m.logger.Infof("starting subscription refresh")

	// 无订阅时跳过（不报错、不 reload），避免定时/手动空跑把状态污染成 error。
	if len(m.subscriptions()) == 0 {
		m.logger.Infof("no subscriptions configured, skip refresh")
		m.mu.Lock()
		m.status.LastRefresh = time.Now()
		m.status.LastError = ""
		m.mu.Unlock()
		return
	}

	// Fetch nodes from all subscriptions
	nodes, err := m.fetchAllSubscriptions()
	if err != nil {
		m.logger.Errorf("fetch subscriptions failed: %v", err)
		m.mu.Lock()
		m.status.LastError = err.Error()
		m.status.LastRefresh = time.Now()
		m.mu.Unlock()
		return
	}

	if len(nodes) == 0 {
		m.logger.Warnf("no nodes fetched from subscriptions")
		m.mu.Lock()
		m.status.LastError = "no nodes fetched"
		m.status.LastRefresh = time.Now()
		m.mu.Unlock()
		return
	}

	m.logger.Infof("fetched %d nodes from subscriptions", len(nodes))

	// 订阅节点不再持久化到 nodes.txt：每次刷新重新拉取；手动节点已在 config.yaml（修 D1）

	// Get current port mapping to preserve existing node ports
	portMap := m.boxMgr.CurrentPortMap()

	// Create new config with updated nodes
	newCfg := m.createNewConfig(nodes)

	// Trigger BoxManager reload with port preservation
	if err := m.boxMgr.ReloadWithPortMap(newCfg, portMap); err != nil {
		m.logger.Errorf("reload failed: %v", err)
		m.mu.Lock()
		m.status.LastError = err.Error()
		m.status.LastRefresh = time.Now()
		m.mu.Unlock()
		return
	}

	m.mu.Lock()
	m.status.LastRefresh = time.Now()
	m.status.NodeCount = len(newCfg.Nodes) // 手动 + 订阅
	m.status.LastError = ""
	m.mu.Unlock()

	m.logger.Infof("subscription refresh completed, %d nodes active", len(newCfg.Nodes))
}

// getNodesFilePath returns the path to nodes.txt.
func (m *Manager) getNodesFilePath() string {
	if m.baseCfg.NodesFile != "" {
		return m.baseCfg.NodesFile
	}
	return filepath.Join(filepath.Dir(m.baseCfg.FilePath()), "nodes.txt")
}

// writeNodesToFile writes nodes to a file (one URI per line).
func (m *Manager) writeNodesToFile(path string, nodes []config.NodeConfig) error {
	var lines []string
	for _, node := range nodes {
		lines = append(lines, node.URI)
	}
	content := strings.Join(lines, "\n")
	if len(lines) > 0 {
		content += "\n"
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// computeNodesHash computes a hash of node URIs for change detection.
func (m *Manager) computeNodesHash(nodes []config.NodeConfig) string {
	var uris []string
	for _, node := range nodes {
		uris = append(uris, node.URI)
	}
	content := strings.Join(uris, "\n")
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}

// CheckNodesModified checks if nodes.txt has been modified since last refresh.
// Uses file modification time as a fast path to avoid unnecessary file reads.
func (m *Manager) CheckNodesModified() bool {
	m.mu.RLock()
	lastHash := m.lastSubHash
	lastMod := m.lastNodesModTime
	m.mu.RUnlock()

	if lastHash == "" {
		return false // No previous refresh, can't determine modification
	}

	nodesFilePath := m.getNodesFilePath()

	// Fast path: check modification time first
	info, err := os.Stat(nodesFilePath)
	if err != nil {
		return false // File doesn't exist or can't stat
	}
	modTime := info.ModTime()
	if !modTime.After(lastMod) {
		return false // File hasn't been modified
	}

	// Slow path: file was modified, compute hash
	data, err := os.ReadFile(nodesFilePath)
	if err != nil {
		return false // File doesn't exist or can't read
	}

	// Parse nodes from file content
	var nodes []config.NodeConfig
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if isProxyURI(line) {
			nodes = append(nodes, config.NodeConfig{URI: line})
		}
	}

	currentHash := m.computeNodesHash(nodes)
	changed := currentHash != lastHash

	// Update cached mod time
	m.mu.Lock()
	m.lastNodesModTime = modTime
	m.mu.Unlock()

	return changed
}

// MarkNodesModified updates the modification status.
func (m *Manager) MarkNodesModified() {
	m.mu.Lock()
	m.status.NodesModified = true
	m.mu.Unlock()
}

// parseSubscriptionEntry 解析订阅条目，支持 "订阅名字:URL" 或 "URL" 格式
// 返回订阅名字和URL，如果没有订阅名字则返回空字符串
func parseSubscriptionEntry(entry string) (name, url string) {
	entry = strings.TrimSpace(entry)

	// 查找第一个冒号的位置
	colonIdx := strings.Index(entry, ":")

	// 如果没有冒号，或者冒号是 http:// 或 https:// 的一部分，则没有订阅名字
	if colonIdx == -1 || strings.HasPrefix(entry, "http://") || strings.HasPrefix(entry, "https://") {
		return "", entry
	}

	// 检查冒号后面是否是 //，如果是则说明这是 URL 的一部分
	if colonIdx+2 < len(entry) && entry[colonIdx+1:colonIdx+3] == "//" {
		return "", entry
	}

	// 分割订阅名字和URL
	name = strings.TrimSpace(entry[:colonIdx])
	url = strings.TrimSpace(entry[colonIdx+1:])

	return name, url
}

// fetchAllSubscriptions fetches nodes from all configured subscription URLs.
// subItem 订阅刷新单元（来自 bbolt store 或 config.Subscriptions）。
type subItem struct {
	ID   uint64 // store 订阅 ID（回退 config.Subscriptions 时为 0）
	Name string
	URL  string
}

// subscriptions 返回待刷新的订阅列表：优先 bbolt store，回退 config.Subscriptions。
func (m *Manager) subscriptions() []subItem {
	if m.store != nil {
		if subs, err := m.store.ListSubscriptions(); err == nil && len(subs) > 0 {
			items := make([]subItem, 0, len(subs))
			for _, s := range subs {
				items = append(items, subItem{ID: s.ID, Name: s.Name, URL: s.URL})
			}
			return items
		}
	}
	entries := m.baseCfg.SubscriptionsList() // 持锁拷贝，防与订阅 CRUD 写 yaml 竞争
	items := make([]subItem, 0, len(entries))
	for _, entry := range entries {
		name, u := parseSubscriptionEntry(entry)
		items = append(items, subItem{Name: name, URL: u})
	}
	return items
}

// updateSubStatus 写订阅刷新状态到 bbolt（best-effort；id=0 或无 store 时跳过）。
func (m *Manager) updateSubStatus(id uint64, status, lastErr string, nodeCount int) {
	if m.store == nil || id == 0 {
		return
	}
	sub, err := m.store.GetSubscription(id)
	if err != nil || sub == nil {
		return
	}
	sub.LastRefreshStatus = status
	sub.LastError = lastErr
	sub.NodeCount = nodeCount
	if status == store.SubStatusSuccess || status == store.SubStatusFailed {
		sub.LastRefreshAt = time.Now()
	}
	_ = m.store.UpdateSubscription(sub)
}

// fetchOne 拉取单个订阅：标注 source=subscription、stable_id（订阅公式）、写 bbolt 状态。
func (m *Manager) fetchOne(item subItem, timeout time.Duration) ([]config.NodeConfig, error) {
	m.updateSubStatus(item.ID, store.SubStatusRunning, "", 0)
	nodes, err := m.fetchSubscription(item.URL, timeout)
	if err != nil {
		m.updateSubStatus(item.ID, store.SubStatusFailed, err.Error(), 0)
		return nil, err
	}
	// 订阅名后缀 + 从 URI fragment 提取节点名
	if item.Name != "" {
		for idx := range nodes {
			if nodes[idx].Name == "" {
				if parsed, err := url.Parse(nodes[idx].URI); err == nil && parsed.Fragment != "" {
					if decoded, err := url.QueryUnescape(parsed.Fragment); err == nil {
						nodes[idx].Name = decoded
					} else {
						nodes[idx].Name = parsed.Fragment
					}
				}
			}
			if nodes[idx].Name != "" {
				nodes[idx].Name = nodes[idx].Name + "|" + item.Name
			}
		}
	}
	// 标注订阅来源 + 跨刷新稳定主键（订阅公式，data-model §1.4）
	for idx := range nodes {
		nodes[idx].Source = config.NodeSourceSubscription
		nodes[idx].SubscriptionID = item.ID
		nodes[idx].StableID = config.StableID(config.NodeSourceSubscription, item.ID, nodes[idx].URI)
	}
	m.updateSubStatus(item.ID, store.SubStatusSuccess, "", len(nodes))
	m.logger.Infof("fetched %d nodes from subscription %s", len(nodes), item.URL)
	return nodes, nil
}

func (m *Manager) fetchAllSubscriptions() ([]config.NodeConfig, error) {
	var allNodes []config.NodeConfig
	var lastErr error

	timeout := m.baseCfg.SubscriptionRefresh.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	for _, item := range m.subscriptions() {
		nodes, err := m.fetchOne(item, timeout)
		if err != nil {
			m.logger.Warnf("failed to fetch %s: %v", item.URL, err)
			lastErr = err
			continue
		}
		allNodes = append(allNodes, nodes...)
	}

	if len(allNodes) == 0 && lastErr != nil {
		return nil, lastErr
	}

	return allNodes, nil
}

// fetchSubscription fetches and parses a single subscription URL.
func (m *Manager) fetchSubscription(subURL string, timeout time.Duration) ([]config.NodeConfig, error) {
	ctx, cancel := context.WithTimeout(m.ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", subURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "*/*")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	return config.ParseSubscriptionContent(string(body))
}

// createNewConfig 构建刷新后的配置：保留手动节点（source!=subscription）+ 新拉订阅节点。
// 不再覆写 nodes.txt；手动节点持久化于 config.yaml，订阅节点每次刷新重新拉取（修 D1）。
func (m *Manager) createNewConfig(subNodes []config.NodeConfig) *config.Config {
	// Deep copy base config
	newCfg := *m.baseCfg

	// 合并：保留非订阅节点（manual/inline/file）+ 新拉订阅节点（旧订阅节点丢弃）
	merged := make([]config.NodeConfig, 0, len(m.baseCfg.Nodes)+len(subNodes))
	for _, n := range m.baseCfg.Nodes {
		if n.Source == config.NodeSourceSubscription {
			continue
		}
		merged = append(merged, n)
	}
	merged = append(merged, subNodes...)

	// multi-port 模式端口分配
	if newCfg.Mode == "multi-port" {
		portCursor := newCfg.MultiPort.BasePort
		for i := range merged {
			merged[i].Port = portCursor
			portCursor++
			if merged[i].Username == "" {
				merged[i].Username = newCfg.MultiPort.Username
				merged[i].Password = newCfg.MultiPort.Password
			}
		}
	}

	// 名称处理（仅对缺名节点，手动节点保留原名）
	for i := range merged {
		merged[i].Name = strings.TrimSpace(merged[i].Name)
		merged[i].URI = strings.TrimSpace(merged[i].URI)
		if merged[i].Name == "" {
			if parsed, err := url.Parse(merged[i].URI); err == nil && parsed.Fragment != "" {
				if decoded, err := url.QueryUnescape(parsed.Fragment); err == nil {
					merged[i].Name = decoded
				} else {
					merged[i].Name = parsed.Fragment
				}
			}
		}
		if merged[i].Name == "" {
			merged[i].Name = fmt.Sprintf("node-%d", i)
		}
	}

	newCfg.Nodes = merged
	return &newCfg
}

func isProxyURI(s string) bool {
	schemes := []string{"vmess://", "vless://", "trojan://", "ss://", "ssr://", "hysteria://", "hysteria2://", "hy2://"}
	lower := strings.ToLower(s)
	for _, scheme := range schemes {
		if strings.HasPrefix(lower, scheme) {
			return true
		}
	}
	return false
}

type defaultLogger struct{}

func (defaultLogger) Infof(format string, args ...any) {
	logger.Infof("[subscription] "+format, args...)
}

func (defaultLogger) Warnf(format string, args ...any) {
	logger.Warnf("[subscription] "+format, args...)
}

func (defaultLogger) Errorf(format string, args ...any) {
	logger.Errorf("[subscription] "+format, args...)
}
