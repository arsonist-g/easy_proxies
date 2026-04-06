package monitor

import (
	"sync"
	"time"

	"github.com/dlclark/regexp2"
)

// NodeCache 缓存可用节点列表，避免每次请求都遍历所有节点
type NodeCache struct {
	mu           sync.RWMutex
	nodes        []Snapshot       // 可用节点列表
	nameIndex    map[string]int // 按名称索引，加速正则筛选
	lastUpdate   time.Time     // 最后更新时间
	ttl          time.Duration // 缓存有效期
}

// NewNodeCache 创建新的节点缓存
func NewNodeCache(ttl time.Duration) *NodeCache {
	return &NodeCache{
		nameIndex: make(map[string]int),
		ttl:       ttl,
	}
}

// Get 获取缓存的节点列表，如果缓存过期返回 nil
func (c *NodeCache) Get() []Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// 检查缓存是否过期
	if time.Since(c.lastUpdate) > c.ttl {
		return nil
	}

	// 返回副本，避免外部修改
	nodes := make([]Snapshot, len(c.nodes))
	copy(nodes, c.nodes)
	return nodes
}

// Update 更新缓存
func (c *NodeCache) Update(nodes []Snapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.nodes = make([]Snapshot, len(nodes))
	copy(c.nodes, nodes)

	// 构建名称索引
	c.nameIndex = make(map[string]int, len(nodes))
	for i, node := range nodes {
		c.nameIndex[node.Name] = i
	}

	c.lastUpdate = time.Now()
}

// FilterByRegex 使用正则表达式筛选节点名称
func (c *NodeCache) FilterByRegex(pattern string) ([]Snapshot, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// 检查缓存是否过期
	if time.Since(c.lastUpdate) > c.ttl {
		return nil, nil // 缓存过期，返回 nil 由调用方重新获取
	}

	// 编译正则表达式（使用 regexp2 支持零宽断言）
	re, err := regexp2.Compile(pattern, regexp2.RE2)
	if err != nil {
		return nil, err
	}

	// 筛选匹配的节点
	var result []Snapshot
	for _, node := range c.nodes {
		matched, err := re.MatchString(node.Name)
		if err != nil {
			continue
		}
		if matched {
			result = append(result, node)
		}
	}

	return result, nil
}

// Clear 清空缓存
func (c *NodeCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.nodes = nil
	c.nameIndex = nil
	c.lastUpdate = time.Time{}
}
