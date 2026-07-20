// Package alert 扫描运行时状态产生安全告警（首期：空密码入口）。
//
// 独立定义输入类型（NodeInput/PoolInput）避免 import monitor 造成循环；
// server 层负责把 Snapshot/VirtualPoolStatus 转换为本包输入。
package alert

import "fmt"

// NodeInput 告警扫描所需的节点最小字段。
type NodeInput struct {
	StableID string
	Name     string
	Password string
}

// PoolInput 告警扫描所需的虚拟池最小字段。
type PoolInput struct {
	Name     string
	Username string
	Password string
}

// Item 单条告警。
type Item struct {
	Level   string `json:"level"`             // "warning" / "critical"
	Code    string `json:"code"`              // 机器可读告警码
	Message string `json:"message"`           // 人类可读描述
	Ref     string `json:"ref,omitempty"`     // 关联标识（stable_id / pool name）
}

// 告警码常量。
const (
	CodeEmptyNodePassword  = "empty_node_password"
	CodeEmptyPoolAuth      = "empty_pool_auth"
	CodeWeakPoolAuth       = "weak_pool_auth"
)

// Checker 无状态告警扫描器。
type Checker struct{}

// New 构造 Checker。
func New() *Checker { return &Checker{} }

// Check 扫描节点与虚拟池，返回告警列表（warning 在前，critical 在后）。
func (c *Checker) Check(nodes []NodeInput, pools []PoolInput) []Item {
	var items []Item
	// 节点代理密码来自统一入口配置（multi-port/hybrid 用 multi_port；pool 用 listener），
	// 入口密码为空时所有节点密码都为空——这是同一个配置问题，故只报一条汇总，不逐节点重复。
	emptyNodePwd := 0
	for _, n := range nodes {
		if n.Password == "" {
			emptyNodePwd++
		}
	}
	if emptyNodePwd > 0 {
		items = append(items, Item{
			Level:   "warning",
			Code:    CodeEmptyNodePassword,
			Message: fmt.Sprintf("检测到 %d 个节点代理密码为空（节点密码来自统一入口 multi_port/listener 配置，请设置入口密码）", emptyNodePwd),
			Ref:     "proxy_entry",
		})
	}
	for _, p := range pools {
		switch {
		case p.Username == "" && p.Password == "":
			items = append(items, Item{
				Level:   "critical",
				Code:    CodeEmptyPoolAuth,
				Message: "虚拟池 " + p.Name + " 未设置认证，任何人可直接使用",
				Ref:     p.Name,
			})
		case p.Password == "":
			items = append(items, Item{
				Level:   "warning",
				Code:    CodeWeakPoolAuth,
				Message: "虚拟池 " + p.Name + " 用户名已设但密码为空",
				Ref:     p.Name,
			})
		}
	}
	return items
}
