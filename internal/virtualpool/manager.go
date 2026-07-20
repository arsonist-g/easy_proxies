// Package virtualpool 实现虚拟池功能
// 虚拟池允许用户通过正则表达式筛选节点，创建独立的负载均衡入口
package virtualpool

import (
	"context"
	"fmt"
	"net"
	"sync"

	"easy_proxies/internal/config"
	"easy_proxies/internal/logger"
	"easy_proxies/internal/monitor"
)

// Manager 虚拟池管理器
// 负责管理所有虚拟池的生命周期
type Manager struct {
	pools      map[string]*VirtualPool // 虚拟池映射表，key 为池名称
	monitorMgr *monitor.Manager        // 节点监控管理器
	cfg        *config.Config          // 配置
	mu         sync.RWMutex
	ctx        context.Context
	cancel     context.CancelFunc
}

// NewManager 创建虚拟池管理器
func NewManager(cfg *config.Config, monitorMgr *monitor.Manager) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		pools:      make(map[string]*VirtualPool),
		monitorMgr: monitorMgr,
		cfg:        cfg,
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Start 启动所有虚拟池
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.cfg.VirtualPools) == 0 {
		logger.Infof("📦 No virtual pools configured")
		return nil
	}

	logger.Infof("📦 Starting %d virtual pool(s)...", len(m.cfg.VirtualPools))

	for _, poolCfg := range m.cfg.VirtualPools {
		pool, err := NewVirtualPool(m.ctx, poolCfg, m.monitorMgr, m.cfg)
		if err != nil {
			// 关闭已启动的池
			for _, p := range m.pools {
				p.Stop()
			}
			return fmt.Errorf("create virtual pool %q: %w", poolCfg.Name, err)
		}

		if err := pool.Start(); err != nil {
			// 关闭已启动的池
			for _, p := range m.pools {
				p.Stop()
			}
			return fmt.Errorf("start virtual pool %q: %w", poolCfg.Name, err)
		}

		m.pools[poolCfg.Name] = pool
		// 获取匹配的节点数量
		nodeCount := len(pool.GetMatchingNodes())
		logger.Infof("✅ Virtual pool %q started on %s:%d (strategy: %s, nodes: %d)",
			poolCfg.Name, poolCfg.Address, poolCfg.Port, poolCfg.Strategy, nodeCount)
	}

	return nil
}

// Stop 停止所有虚拟池
func (m *Manager) Stop() {
	m.cancel()

	m.mu.Lock()
	defer m.mu.Unlock()

	for name, pool := range m.pools {
		pool.Stop()
		logger.Infof("🛑 Virtual pool %q stopped", name)
	}
	m.pools = make(map[string]*VirtualPool)
}

// GetPool 获取指定名称的虚拟池
// 返回 monitor.VirtualPoolInstance 接口以满足 monitor.VirtualPoolManager 接口
func (m *Manager) GetPool(name string) monitor.VirtualPoolInstance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pool := m.pools[name]
	if pool == nil {
		return nil
	}
	return pool
}

// GetAllPools 获取所有虚拟池
func (m *Manager) GetAllPools() []*VirtualPool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pools := make([]*VirtualPool, 0, len(m.pools))
	for _, pool := range m.pools {
		pools = append(pools, pool)
	}
	return pools
}

// ListVirtualPools 返回所有虚拟池配置（GET /api/v1/virtual-pools）。
func (m *Manager) ListVirtualPools() []config.VirtualPoolConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]config.VirtualPoolConfig, len(m.cfg.VirtualPools))
	copy(out, m.cfg.VirtualPools)
	return out
}

// CreateVirtualPool 创建并启动一个虚拟池（POST /api/v1/virtual-pools）。
func (m *Manager) CreateVirtualPool(pcfg config.VirtualPoolConfig) (config.VirtualPoolConfig, error) {
	// API CRUD 不走配置加载校验，这里显式校验+补默认值（weighted 需权重>0）。
	if err := config.ValidateVirtualPoolConfig(&pcfg); err != nil {
		return config.VirtualPoolConfig{}, err
	}
	m.mu.Lock()
	if pcfg.ID == 0 {
		pcfg.ID = m.nextPoolIDLocked()
	}
	// 端口：留空自动分配；指定端口(来自 next-port 预览)若已被占则重新分配
	if pcfg.Port == 0 {
		port, err := m.allocPortLocked(0)
		if err != nil {
			m.mu.Unlock()
			return config.VirtualPoolConfig{}, err
		}
		pcfg.Port = port
	} else if used := m.usedPortsLocked(0); used[pcfg.Port] != "" || !isPortAvailable("0.0.0.0", pcfg.Port) {
		port, err := m.allocPortLocked(0)
		if err != nil {
			m.mu.Unlock()
			return config.VirtualPoolConfig{}, err
		}
		pcfg.Port = port
	}
	if err := m.checkPoolConflictLocked(pcfg, 0); err != nil {
		m.mu.Unlock()
		return config.VirtualPoolConfig{}, err
	}
	m.cfg.VirtualPools = append(m.cfg.VirtualPools, pcfg)
	pool, err := NewVirtualPool(m.ctx, pcfg, m.monitorMgr, m.cfg)
	if err != nil {
		m.cfg.VirtualPools = m.cfg.VirtualPools[:len(m.cfg.VirtualPools)-1]
		m.mu.Unlock()
		return config.VirtualPoolConfig{}, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Start(); err != nil {
		m.cfg.VirtualPools = m.cfg.VirtualPools[:len(m.cfg.VirtualPools)-1]
		m.mu.Unlock()
		return config.VirtualPoolConfig{}, fmt.Errorf("start pool: %w", err)
	}
	m.pools[pcfg.Name] = pool
	saveErr := m.cfg.SaveVirtualPools()
	m.mu.Unlock()
	if saveErr != nil {
		return config.VirtualPoolConfig{}, fmt.Errorf("save config: %w", saveErr)
	}
	logger.Infof("✅ Virtual pool %q created (id: %d)", pcfg.Name, pcfg.ID)
	return pcfg, nil
}

// UpdateVirtualPool 更新虚拟池（PATCH /api/v1/virtual-pools/{id}）：停旧池、建新池。
func (m *Manager) UpdateVirtualPool(id uint64, pcfg config.VirtualPoolConfig) (config.VirtualPoolConfig, error) {
	// 同 Create：显式校验+补默认值，弥补 API CRUD 绕过加载校验的缺口。
	if err := config.ValidateVirtualPoolConfig(&pcfg); err != nil {
		return config.VirtualPoolConfig{}, err
	}
	m.mu.Lock()
	idx := m.poolIndexByIDLocked(id)
	if idx == -1 {
		m.mu.Unlock()
		return config.VirtualPoolConfig{}, fmt.Errorf("虚拟池 %d 不存在", id)
	}
	old := m.cfg.VirtualPools[idx]
	pcfg.ID = id
	// 编辑时端口保持稳定（集成方依赖）；前端只读传原值，异常 0 时沿用旧端口
	if pcfg.Port == 0 {
		pcfg.Port = old.Port
	}
	if err := m.checkPoolConflictLocked(pcfg, id); err != nil {
		m.mu.Unlock()
		return config.VirtualPoolConfig{}, err
	}
	if pool, ok := m.pools[old.Name]; ok {
		pool.Stop()
		delete(m.pools, old.Name)
	}
	m.cfg.VirtualPools[idx] = pcfg
	pool, err := NewVirtualPool(m.ctx, pcfg, m.monitorMgr, m.cfg)
	if err != nil {
		m.cfg.VirtualPools[idx] = old
		m.mu.Unlock()
		return config.VirtualPoolConfig{}, fmt.Errorf("recreate pool: %w", err)
	}
	if err := pool.Start(); err != nil {
		m.cfg.VirtualPools[idx] = old
		m.mu.Unlock()
		return config.VirtualPoolConfig{}, fmt.Errorf("restart pool: %w", err)
	}
	m.pools[pcfg.Name] = pool
	saveErr := m.cfg.SaveVirtualPools()
	m.mu.Unlock()
	if saveErr != nil {
		return config.VirtualPoolConfig{}, fmt.Errorf("save config: %w", saveErr)
	}
	logger.Infof("✅ Virtual pool %q updated (id: %d)", pcfg.Name, id)
	return pcfg, nil
}

// DeleteVirtualPool 删除虚拟池（DELETE /api/v1/virtual-pools/{id}）。
func (m *Manager) DeleteVirtualPool(id uint64) error {
	m.mu.Lock()
	idx := m.poolIndexByIDLocked(id)
	if idx == -1 {
		m.mu.Unlock()
		return fmt.Errorf("虚拟池 %d 不存在", id)
	}
	old := m.cfg.VirtualPools[idx]
	if pool, ok := m.pools[old.Name]; ok {
		pool.Stop()
		delete(m.pools, old.Name)
	}
	m.cfg.VirtualPools = append(m.cfg.VirtualPools[:idx], m.cfg.VirtualPools[idx+1:]...)
	saveErr := m.cfg.SaveVirtualPools()
	m.mu.Unlock()
	if saveErr != nil {
		return fmt.Errorf("save config: %w", saveErr)
	}
	logger.Infof("🛑 Virtual pool %q deleted (id: %d)", old.Name, id)
	return nil
}

// nextPoolIDLocked 分配下一个虚拟池 ID（调用方持锁）。
func (m *Manager) nextPoolIDLocked() uint64 {
	var maxID uint64
	for _, p := range m.cfg.VirtualPools {
		if p.ID > maxID {
			maxID = p.ID
		}
	}
	return maxID + 1
}

// checkPoolConflictLocked 检查名称/端口冲突（ignoreID 排除自身）。
func (m *Manager) checkPoolConflictLocked(pcfg config.VirtualPoolConfig, ignoreID uint64) error {
	for _, p := range m.cfg.VirtualPools {
		if p.ID == ignoreID {
			continue
		}
		if p.Name == pcfg.Name {
			return fmt.Errorf("虚拟池名称 %s 已存在", pcfg.Name)
		}
		if pcfg.Port != 0 && p.Port == pcfg.Port {
			return fmt.Errorf("端口 %d 已被虚拟池 %s 占用", pcfg.Port, p.Name)
		}
	}
	return nil
}

// isPortAvailable 探测端口是否可绑定（TCP listen 测试）。
func isPortAvailable(address string, port uint16) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", address, port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// usedPortsLocked 收集所有已占用端口（listener + 节点 + 其他虚拟池），ignoreID 排除某虚拟池（调用方持锁）。
func (m *Manager) usedPortsLocked(ignoreID uint64) map[uint16]string {
	used := make(map[uint16]string)
	if m.cfg.Pool.Port > 0 {
		used[m.cfg.Pool.Port] = "listener"
	}
	for _, n := range m.cfg.Nodes {
		if n.Port > 0 {
			used[n.Port] = "node:" + n.Name
		}
	}
	for _, p := range m.cfg.VirtualPools {
		if p.ID == ignoreID {
			continue
		}
		if p.Port > 0 {
			used[p.Port] = "virtual_pool:" + p.Name
		}
	}
	return used
}

// allocPortLocked 分配一个可用端口：从 max(base_port, max(已用虚拟池端口)+1) 起，跳过已占用 + 实测可用（调用方持锁）。
func (m *Manager) allocPortLocked(ignoreID uint64) (uint16, error) {
	if m.cfg.VirtualPool.BasePort == 0 {
		return 0, fmt.Errorf("virtual_pool.base_port 未配置，无法自动分配端口")
	}
	used := m.usedPortsLocked(ignoreID)
	start := m.cfg.VirtualPool.BasePort
	for _, p := range m.cfg.VirtualPools {
		if p.ID == ignoreID {
			continue
		}
		if p.Port >= start {
			start = p.Port + 1
		}
	}
	for port := start; port <= 65535; port++ {
		if _, ok := used[port]; ok {
			continue
		}
		if !isPortAvailable("0.0.0.0", port) {
			continue
		}
		return port, nil
	}
	return 0, fmt.Errorf("从 %d 起无可用端口", start)
}

// NextAvailablePort 返回下一个可用端口（供 API 预览，加锁）。
func (m *Manager) NextAvailablePort() (uint16, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.allocPortLocked(0)
}

// poolIndexByIDLocked 按 ID 定位虚拟池配置索引（调用方持锁）。
func (m *Manager) poolIndexByIDLocked(id uint64) int {
	for i, p := range m.cfg.VirtualPools {
		if p.ID == id {
			return i
		}
	}
	return -1
}

// Status 获取所有虚拟池的状态
func (m *Manager) Status() []monitor.VirtualPoolStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	statuses := make([]monitor.VirtualPoolStatus, 0, len(m.pools))
	for _, pool := range m.pools {
		s := pool.Status()
		statuses = append(statuses, monitor.VirtualPoolStatus{
			Name:         s.Name,
			Regular:      s.Regular,
			Address:      s.Address,
			Port:         s.Port,
			Strategy:     s.Strategy,
			MaxLatencyMs: s.MaxLatencyMs,
			NodeCount:    s.NodeCount,
			Running:      s.Running,
			Username:     s.Username,
			Password:     s.Password,
		})
	}
	return statuses
}

// PoolStatus 虚拟池状态
type PoolStatus struct {
	Name         string `json:"name"`           // 池名称
	Regular      string `json:"regular"`        // 正则表达式
	Address      string `json:"address"`        // 监听地址
	Port         uint16 `json:"port"`           // 监听端口
	Strategy     string `json:"strategy"`       // 负载均衡策略
	MaxLatencyMs int    `json:"max_latency_ms"` // 最大延迟阈值
	NodeCount    int    `json:"node_count"`     // 匹配的节点数量
	Running      bool   `json:"running"`        // 是否运行中
	Username     string `json:"username"`       // 代理用户名
	Password     string `json:"password"`       // 代理密码
}
