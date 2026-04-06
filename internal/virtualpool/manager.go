// Package virtualpool 实现虚拟池功能
// 虚拟池允许用户通过正则表达式筛选节点，创建独立的负载均衡入口
package virtualpool

import (
	"context"
	"fmt"
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
