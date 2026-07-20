package config

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"easy_proxies/internal/logger"

	"github.com/dlclark/regexp2"
	"gopkg.in/yaml.v3"
)

// Config describes the high level settings for the proxy pool server.
type Config struct {
	Mode                string                    `yaml:"mode"`
	MultiPort           MultiPortConfig           `yaml:"multi_port"`
	Pool                PoolConfig                `yaml:"pool"`
	Management          ManagementConfig          `yaml:"management"`
	SubscriptionRefresh SubscriptionRefreshConfig `yaml:"subscription_refresh"`
	VirtualPools        []VirtualPoolConfig       `yaml:"virtual_pools"`        // 虚拟池配置列表
	Nodes               []NodeConfig              `yaml:"nodes"`
	NodesFile           string                    `yaml:"nodes_file"`    // 节点文件路径，每行一个 URI
	Subscriptions       []string                  `yaml:"subscriptions"` // 订阅链接列表（迁移源，实体化后走 bbolt）
	ExternalIP          string                    `yaml:"external_ip"`      // 外部 IP 地址，用于导出时替换 0.0.0.0
	LogLevel            string                    `yaml:"log_level"`         // 应用日志级别 (debug/info/warn/error)
	SingboxLogLevel     string                    `yaml:"singbox_log_level"` // sing-box 核心日志级别，默认 warn
	SkipCertVerify      bool                      `yaml:"skip_cert_verify"` // 全局跳过 SSL 证书验证
	AlertEnabled        *bool                     `yaml:"alert_enabled"`   // 安全告警开关（空密码检测），默认 true
	GeoIP               GeoIPConfig               `yaml:"geoip"`           // 可选本地 MaxMind GeoLite2-ASN，空=禁用
	VirtualPool         VirtualPoolSettings       `yaml:"virtual_pool"`    // 虚拟池全局设置（端口起点等）
	CredentialKey       string                    `yaml:"credential_key"`  // 凭证加密密钥（AES-256 hex），解密 db 中密文用；与 db 文件分离

	filePath string `yaml:"-"` // 配置文件路径，用于保存

	saveMu *sync.Mutex `yaml:"-"` // 串行化各 Save* 的“读-改-写”，防跨 manager 写同一文件 lost update；指针避免 Config 值拷贝触发 vet
}

// ListenerConfig defines how the HTTP proxy should listen for clients.
type ListenerConfig struct {
	Address  string `yaml:"address"`
	Port     uint16 `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// PoolConfig 配置节点池的调度策略与代理入口（pool/hybrid 模式的统一入口）。
// ListenerConfig 以 inline 内嵌：address/port/username/password 直接出现在 pool 段下，
// 与 multi_port 的扁平结构保持一致（原顶层 listener 段已废弃）。
type PoolConfig struct {
	Mode               string        `yaml:"mode"`
	FailureThreshold   int           `yaml:"failure_threshold"`
	BlacklistDuration  time.Duration `yaml:"blacklist_duration"`
	LatencyWeight      float64       `yaml:"latency_weight,omitempty"`      // Mode=weighted：延迟权重（>0，内部归一化）
	AvailabilityWeight float64       `yaml:"availability_weight,omitempty"` // Mode=weighted：可用率权重（>0）
	ListenerConfig     `yaml:",inline"` // 节点池代理入口（address/port/username/password）
}

// validatePoolMode 校验主出站池调度模式合法性；weighted 模式要求两个权重均 >0。
func validatePoolMode(p PoolConfig) error {
	switch p.Mode {
	case "sequential", "random", "balance":
		// 有效模式
	case "weighted":
		if p.LatencyWeight <= 0 || p.AvailabilityWeight <= 0 {
			return fmt.Errorf("pool.mode 'weighted' requires latency_weight>0 and availability_weight>0")
		}
	default:
		return fmt.Errorf("unsupported pool.mode %q (use 'sequential', 'random', 'balance', or 'weighted')", p.Mode)
	}
	return nil
}

// ValidationError 配置业务校验失败。API 层据此映射 422（区别于 500 内部错误）。
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

// ValidateVirtualPoolConfig 校验单个虚拟池配置并填充默认值（strategy/address）。
// weighted 策略要求 latency_weight>0 且 availability_weight>0。供配置加载与 API CRUD 共用。
func ValidateVirtualPoolConfig(p *VirtualPoolConfig) error {
	if p.Strategy == "" {
		p.Strategy = "sequential"
	}
	switch p.Strategy {
	case "sequential", "random", "balance", "weighted":
	default:
		return &ValidationError{Msg: fmt.Sprintf("invalid strategy %q (use 'sequential', 'random', 'balance', or 'weighted')", p.Strategy)}
	}
	if p.Strategy == "weighted" {
		if p.LatencyWeight <= 0 || p.AvailabilityWeight <= 0 {
			return &ValidationError{Msg: "strategy 'weighted' requires latency_weight>0 and availability_weight>0"}
		}
	}
	if p.Address == "" {
		p.Address = "0.0.0.0"
	}
	if p.MaxLatencyMs < 0 {
		return &ValidationError{Msg: "max_latency_ms cannot be negative"}
	}
	return nil
}

// MultiPortConfig defines address/credential defaults for multi-port mode.
type MultiPortConfig struct {
	Address  string `yaml:"address"`
	BasePort uint16 `yaml:"base_port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// VirtualPoolSettings 虚拟池子系统全局设置。
type VirtualPoolSettings struct {
	BasePort uint16 `yaml:"base_port"` // 虚拟池端口分配起点（必填，无默认；新建池端口留空时从此起点按 max(已用)+1 分配）
}

// ManagementConfig controls the monitoring HTTP endpoint.
type ManagementConfig struct {
	Enabled     *bool  `yaml:"enabled"`
	Listen      string `yaml:"listen"`
	ProbeTarget string `yaml:"probe_target"`
	Password    string `yaml:"password"` // WebUI 访问密码，为空则不需要密码
	PathPwd     string `yaml:"path_pwd"` // 路径密码，访问管理页面需要通过 /路径密码 访问，为空则使用默认路径 /
}

// SubscriptionRefreshConfig controls subscription auto-refresh and reload settings.
type SubscriptionRefreshConfig struct {
	Enabled            bool          `yaml:"enabled"`              // 是否启用定时刷新
	Interval           time.Duration `yaml:"interval"`             // 刷新间隔，默认 1 小时
	Timeout            time.Duration `yaml:"timeout"`              // 获取订阅的超时时间
	HealthCheckTimeout time.Duration `yaml:"health_check_timeout"` // 新节点健康检查超时
	DrainTimeout       time.Duration `yaml:"drain_timeout"`        // 旧实例排空超时时间
	MinAvailableNodes  int           `yaml:"min_available_nodes"`  // 最少可用节点数，低于此值不切换
}

// VirtualPoolConfig 定义虚拟池配置
// 虚拟池允许用户通过正则表达式筛选节点，创建独立的负载均衡入口
type VirtualPoolConfig struct {
	ID           uint64   `yaml:"id" json:"id"`                                               // 稳定标识（bbolt sequence，CRUD 路由主键；name 可改）
	Name         string   `yaml:"name" json:"name"`                                           // 虚拟池名称（唯一标识）
	Regular      string   `yaml:"regular" json:"regular"`                                     // 正则表达式，用于匹配节点名称
	Address      string   `yaml:"address" json:"address"`                                     // 监听地址
	Port         uint16   `yaml:"port" json:"port"`                                           // 监听端口
	Username     string   `yaml:"username,omitempty" json:"username,omitempty"`               // 认证用户名（可选）
	Password     string   `yaml:"password,omitempty" json:"password,omitempty"`               // 认证密码（可选）
	Strategy     string   `yaml:"strategy,omitempty" json:"strategy,omitempty"`               // 负载均衡策略：sequential/random/balance/weighted，默认 sequential
	MaxLatencyMs int      `yaml:"max_latency_ms,omitempty" json:"max_latency_ms,omitempty"`   // 最大延迟阈值（毫秒），可选的额外过滤条件
	LatencyWeight      float64 `yaml:"latency_weight,omitempty" json:"latency_weight,omitempty"`           // weighted 策略：延迟权重（>0，与 availability_weight 相对比例，内部归一化）
	AvailabilityWeight float64 `yaml:"availability_weight,omitempty" json:"availability_weight,omitempty"` // weighted 策略：可用率权重（>0）
	CountryCodes         []string `yaml:"country_codes,omitempty" json:"country_codes,omitempty"`                 // 国家码包含过滤（可选，与 regular 并存）
	ExcludedCountryCodes []string `yaml:"excluded_country_codes,omitempty" json:"excluded_country_codes,omitempty"` // 国家码排除过滤（可选，命中则跳过该节点）
}

// GeoIPConfig 本地 MaxMind GeoLite2-ASN 查询配置（可选）。
// 配置后，节点探测到出口 IP 会回填 ASN 与组织名；不配置则相关字段留空。
// 配置 license_key + account_id 后启用自动更新：缺库下载、有库按 If-Modified-Since 周期更新。
type GeoIPConfig struct {
	ASNDatabase    string        `yaml:"asn_database"`    // GeoLite2-ASN.mmdb 路径，空=禁用 ASN 查询
	AccountID      string        `yaml:"account_id"`      // MaxMind Account ID（自动更新用，Basic Auth 用户名）
	LicenseKey     string        `yaml:"license_key"`     // MaxMind License Key（自动更新用，Basic Auth 密码）
	EditionID      string        `yaml:"edition_id"`      // 数据库 edition，默认 GeoLite2-ASN
	UpdateInterval time.Duration `yaml:"update_interval"` // 自动更新检查间隔，默认 24h（MaxMind 每周二更新）
}

// NodeSource indicates where a node configuration originated from.
type NodeSource string

const (
	NodeSourceInline       NodeSource = "inline"       // Defined directly in config.yaml nodes array
	NodeSourceFile         NodeSource = "nodes_file"   // Loaded from external nodes file
	NodeSourceSubscription NodeSource = "subscription" // Fetched from subscription URL
	NodeSourceManual       NodeSource = "manual"       // Added via WebUI/API
)

// NodeConfig describes a single upstream proxy endpoint expressed as URI.
type NodeConfig struct {
	Name           string     `yaml:"name" json:"name"`
	URI            string     `yaml:"uri" json:"uri"`
	Port           uint16     `yaml:"port,omitempty" json:"port,omitempty"`
	Username       string     `yaml:"username,omitempty" json:"username,omitempty"`
	Password       string     `yaml:"password,omitempty" json:"password,omitempty"`
	Source         NodeSource `yaml:"source,omitempty" json:"source,omitempty"`                 // 持久化来源：inline/nodes_file/subscription/manual
	StableID       string     `yaml:"stable_id,omitempty" json:"stable_id,omitempty"`          // 跨刷新稳定主键（data-model §1.4）
	SubscriptionID uint64     `yaml:"subscription_id,omitempty" json:"subscription_id,omitempty"` // source=subscription 时关联 Subscription.id
	DuplicateOf    string     `yaml:"duplicate_of,omitempty" json:"duplicate_of,omitempty"`    // 去重标记，指向留存节点 stable_id
	CountryCode    string     `yaml:"country_code,omitempty" json:"country_code,omitempty"`    // 冗余缓存（NodeProbe 派生），列表快速展示
}

// NodeKey returns a unique identifier for the node based on its URI.
// This is used to preserve port assignments across reloads.
func (n *NodeConfig) NodeKey() string {
	return n.URI
}

// StableID 计算节点的跨刷新稳定主键（data-model §1.4，ADR-0010）。
//   非订阅节点：hex(sha256(source + ":" + uri))
//   订阅节点：  hex(sha256("subscription:" + sub_id + ":" + uri))
// 同订阅刷新 URI 不变 → stable_id 不变，关联保留；URI 变视为新节点；跨订阅同节点靠 exit_ip 去重。
func StableID(source NodeSource, subID uint64, uri string) string {
	h := sha256.New()
	if source == NodeSourceSubscription {
		io.WriteString(h, "subscription:")
		io.WriteString(h, strconv.FormatUint(subID, 10))
		io.WriteString(h, ":")
		io.WriteString(h, uri)
	} else {
		io.WriteString(h, string(source))
		io.WriteString(h, ":")
		io.WriteString(h, uri)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Load reads YAML config from disk and applies defaults/validation.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	cfg.filePath = path
	cfg.saveMu = &sync.Mutex{}

	// Resolve nodes_file path relative to config file directory
	if cfg.NodesFile != "" && !filepath.IsAbs(cfg.NodesFile) {
		configDir := filepath.Dir(path)
		cfg.NodesFile = filepath.Join(configDir, cfg.NodesFile)
	}
	// Resolve geoip.asn_database path relative to config file directory
	if cfg.GeoIP.ASNDatabase != "" && !filepath.IsAbs(cfg.GeoIP.ASNDatabase) {
		cfg.GeoIP.ASNDatabase = filepath.Join(filepath.Dir(path), cfg.GeoIP.ASNDatabase)
	}

	if err := cfg.normalize(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) normalize() error {
	if c.Mode == "" {
		c.Mode = "pool"
	}
	// Normalize mode name: support both multi-port and multi_port
	if c.Mode == "multi_port" {
		c.Mode = "multi-port"
	}
	switch c.Mode {
	case "pool", "multi-port", "hybrid":
	default:
		return fmt.Errorf("unsupported mode %q (use 'pool', 'multi-port', or 'hybrid')", c.Mode)
	}
	if c.Pool.Address == "" {
		c.Pool.Address = "0.0.0.0"
	}
	if c.Pool.Port == 0 {
		c.Pool.Port = 2323
	}
	if c.Pool.Mode == "" {
		c.Pool.Mode = "sequential"
	}
	if err := validatePoolMode(c.Pool); err != nil {
		return err
	}
	if c.Pool.FailureThreshold <= 0 {
		c.Pool.FailureThreshold = 3
	}
	if c.Pool.BlacklistDuration <= 0 {
		c.Pool.BlacklistDuration = 24 * time.Hour
	}
	if c.MultiPort.Address == "" {
		c.MultiPort.Address = "0.0.0.0"
	}
	if c.MultiPort.BasePort == 0 {
		c.MultiPort.BasePort = 28000
	}
	if c.Management.Listen == "" {
		c.Management.Listen = "127.0.0.1:9090"
	}
	if c.Management.ProbeTarget == "" {
		c.Management.ProbeTarget = "www.apple.com:80"
	}
	if c.Management.Enabled == nil {
		defaultEnabled := true
		c.Management.Enabled = &defaultEnabled
	}
	if c.AlertEnabled == nil {
		defaultAlert := true
		c.AlertEnabled = &defaultAlert
	}

	// Subscription refresh defaults
	if c.SubscriptionRefresh.Interval <= 0 {
		c.SubscriptionRefresh.Interval = 1 * time.Hour
	}
	if c.SubscriptionRefresh.Timeout <= 0 {
		c.SubscriptionRefresh.Timeout = 30 * time.Second
	}
	if c.SubscriptionRefresh.HealthCheckTimeout <= 0 {
		c.SubscriptionRefresh.HealthCheckTimeout = 60 * time.Second
	}
	if c.SubscriptionRefresh.DrainTimeout <= 0 {
		c.SubscriptionRefresh.DrainTimeout = 30 * time.Second
	}
	if c.SubscriptionRefresh.MinAvailableNodes <= 0 {
		c.SubscriptionRefresh.MinAvailableNodes = 1
	}

	// Mark inline nodes with source
	for idx := range c.Nodes {
		c.Nodes[idx].Source = NodeSourceInline
	}

	// Load nodes from file if specified (but NOT if subscriptions exist - subscription takes priority)
	if c.NodesFile != "" && len(c.Subscriptions) == 0 {
		fileNodes, err := loadNodesFromFile(c.NodesFile)
		if err != nil {
			return fmt.Errorf("load nodes from file %q: %w", c.NodesFile, err)
		}
		for idx := range fileNodes {
			fileNodes[idx].Source = NodeSourceFile
		}
		c.Nodes = append(c.Nodes, fileNodes...)
	}

	// Load nodes from subscriptions (highest priority - writes to nodes.txt)
	if len(c.Subscriptions) > 0 {
		var subNodes []NodeConfig
		subTimeout := c.SubscriptionRefresh.Timeout
		for _, subEntry := range c.Subscriptions {
			// 解析订阅名字和URL：格式为 "订阅名字:URL" 或 "URL"
			subName, subURL := parseSubscriptionEntry(subEntry)

			nodes, err := loadNodesFromSubscription(subURL, subTimeout)
			if err != nil {
				logger.Warnf("Failed to load subscription %q: %v (skipping)", subURL, err)
				continue
			}
			logger.Infof("✅ Loaded %d nodes from subscription", len(nodes))

			// 如果有订阅名字，添加到节点名称后
			if subName != "" {
				for idx := range nodes {
					// 先从 URI 的 fragment 中提取节点名称（如果还没有提取）
					if nodes[idx].Name == "" {
						if parsed, err := url.Parse(nodes[idx].URI); err == nil && parsed.Fragment != "" {
							if decoded, err := url.QueryUnescape(parsed.Fragment); err == nil {
								nodes[idx].Name = decoded
							} else {
								nodes[idx].Name = parsed.Fragment
							}
						}
					}
					// 添加订阅名字后缀
					if nodes[idx].Name != "" {
						nodes[idx].Name = nodes[idx].Name + "|" + subName
					}
				}
			}

			subNodes = append(subNodes, nodes...)
		}
		// Mark subscription nodes and write to nodes.txt
		for idx := range subNodes {
			subNodes[idx].Source = NodeSourceSubscription
		}
		if len(subNodes) > 0 {
			// Determine nodes.txt path
			nodesFilePath := c.NodesFile
			if nodesFilePath == "" {
				nodesFilePath = filepath.Join(filepath.Dir(c.filePath), "nodes.txt")
				c.NodesFile = nodesFilePath
			}
			// Write subscription nodes to nodes.txt
			if err := writeNodesToFile(nodesFilePath, subNodes); err != nil {
				logger.Warnf("Failed to write nodes to %q: %v", nodesFilePath, err)
			} else {
				logger.Infof("✅ Written %d subscription nodes to %s", len(subNodes), nodesFilePath)
			}
		}
		c.Nodes = append(c.Nodes, subNodes...)
	}

	if len(c.Nodes) == 0 {
		return errors.New("config.nodes cannot be empty (configure nodes in config or use nodes_file)")
	}
	portCursor := c.MultiPort.BasePort
	for idx := range c.Nodes {
		c.Nodes[idx].Name = strings.TrimSpace(c.Nodes[idx].Name)
		c.Nodes[idx].URI = strings.TrimSpace(c.Nodes[idx].URI)

		if c.Nodes[idx].URI == "" {
			return fmt.Errorf("node %d is missing uri", idx)
		}

		// 回填稳定 ID（source 已在加载阶段确定；订阅节点 subscription_id 阶段2 接 bbolt 后修正）
		if c.Nodes[idx].StableID == "" {
			c.Nodes[idx].StableID = StableID(c.Nodes[idx].Source, c.Nodes[idx].SubscriptionID, c.Nodes[idx].URI)
		}

		// Auto-extract name from URI fragment (#name) if not provided
		if c.Nodes[idx].Name == "" {
			if parsed, err := url.Parse(c.Nodes[idx].URI); err == nil && parsed.Fragment != "" {
				// URL decode the fragment to handle encoded characters
				if decoded, err := url.QueryUnescape(parsed.Fragment); err == nil {
					c.Nodes[idx].Name = decoded
				} else {
					c.Nodes[idx].Name = parsed.Fragment
				}
			}
		}

		// Fallback to default name if still empty
		if c.Nodes[idx].Name == "" {
			c.Nodes[idx].Name = fmt.Sprintf("node-%d", idx)
		}

		// Auto-assign port in multi-port/hybrid mode, skip occupied ports
		if c.Nodes[idx].Port == 0 && (c.Mode == "multi-port" || c.Mode == "hybrid") {
			for !isPortAvailable(c.MultiPort.Address, portCursor) {
				logger.Warnf("Port %d is in use, trying next port", portCursor)
				portCursor++
				if portCursor > 65535 {
					return fmt.Errorf("no available ports found starting from %d", c.MultiPort.BasePort)
				}
			}
			c.Nodes[idx].Port = portCursor
			portCursor++
		} else if c.Nodes[idx].Port == 0 {
			c.Nodes[idx].Port = portCursor
			portCursor++
		}

		if c.Mode == "multi-port" || c.Mode == "hybrid" {
			if c.Nodes[idx].Username == "" {
				c.Nodes[idx].Username = c.MultiPort.Username
				c.Nodes[idx].Password = c.MultiPort.Password
			}
		}
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if c.SingboxLogLevel == "" {
		c.SingboxLogLevel = "warn" // 默认抑制 sing-box 的 info 级别日志
	}

	// Auto-fix port conflicts in hybrid mode (pool port vs multi-port)
	if c.Mode == "hybrid" {
		poolPort := c.Pool.Port
		usedPorts := make(map[uint16]bool)
		usedPorts[poolPort] = true
		for idx := range c.Nodes {
			usedPorts[c.Nodes[idx].Port] = true
		}
		for idx := range c.Nodes {
			if c.Nodes[idx].Port == poolPort {
				// Find next available port
				newPort := c.Nodes[idx].Port + 1
				for usedPorts[newPort] || !isPortAvailable(c.MultiPort.Address, newPort) {
					newPort++
					if newPort > 65535 {
						return fmt.Errorf("no available port for node %q after conflict with pool port %d", c.Nodes[idx].Name, poolPort)
					}
				}
				logger.Warnf("Node %q port %d conflicts with pool port, reassigned to %d", c.Nodes[idx].Name, poolPort, newPort)
				usedPorts[newPort] = true
				c.Nodes[idx].Port = newPort
			}
		}
	}

	// 验证虚拟池配置
	if err := c.validateVirtualPools(); err != nil {
		return err
	}

	return nil
}

// BuildPortMap creates a mapping from node URI to port for existing nodes.
// This is used to preserve port assignments when reloading configuration.
func (c *Config) BuildPortMap() map[string]uint16 {
	portMap := make(map[string]uint16)
	for _, node := range c.Nodes {
		if node.Port > 0 {
			portMap[node.NodeKey()] = node.Port
		}
	}
	return portMap
}

// NormalizeWithPortMap applies defaults and validation, preserving port assignments
// for nodes that exist in the provided port map.
func (c *Config) NormalizeWithPortMap(portMap map[string]uint16) error {
	if c.Mode == "" {
		c.Mode = "pool"
	}
	if c.Mode == "multi_port" {
		c.Mode = "multi-port"
	}
	switch c.Mode {
	case "pool", "multi-port", "hybrid":
	default:
		return fmt.Errorf("unsupported mode %q (use 'pool', 'multi-port', or 'hybrid')", c.Mode)
	}
	if c.Pool.Address == "" {
		c.Pool.Address = "0.0.0.0"
	}
	if c.Pool.Port == 0 {
		c.Pool.Port = 2323
	}
	if c.Pool.Mode == "" {
		c.Pool.Mode = "sequential"
	}
	if err := validatePoolMode(c.Pool); err != nil {
		return err
	}
	if c.Pool.FailureThreshold <= 0 {
		c.Pool.FailureThreshold = 3
	}
	if c.Pool.BlacklistDuration <= 0 {
		c.Pool.BlacklistDuration = 24 * time.Hour
	}
	if c.MultiPort.Address == "" {
		c.MultiPort.Address = "0.0.0.0"
	}
	if c.MultiPort.BasePort == 0 {
		c.MultiPort.BasePort = 28000
	}
	if c.Management.Listen == "" {
		c.Management.Listen = "127.0.0.1:9090"
	}
	if c.Management.ProbeTarget == "" {
		c.Management.ProbeTarget = "www.apple.com:80"
	}
	if c.Management.Enabled == nil {
		defaultEnabled := true
		c.Management.Enabled = &defaultEnabled
	}
	if c.AlertEnabled == nil {
		defaultAlert := true
		c.AlertEnabled = &defaultAlert
	}
	if c.SubscriptionRefresh.Interval <= 0 {
		c.SubscriptionRefresh.Interval = 1 * time.Hour
	}
	if c.SubscriptionRefresh.Timeout <= 0 {
		c.SubscriptionRefresh.Timeout = 30 * time.Second
	}
	if c.SubscriptionRefresh.HealthCheckTimeout <= 0 {
		c.SubscriptionRefresh.HealthCheckTimeout = 60 * time.Second
	}
	if c.SubscriptionRefresh.DrainTimeout <= 0 {
		c.SubscriptionRefresh.DrainTimeout = 30 * time.Second
	}
	if c.SubscriptionRefresh.MinAvailableNodes <= 0 {
		c.SubscriptionRefresh.MinAvailableNodes = 1
	}

	if len(c.Nodes) == 0 {
		return errors.New("config.nodes cannot be empty")
	}

	// Build set of ports already assigned from portMap
	usedPorts := make(map[uint16]bool)
	if c.Mode == "hybrid" {
		usedPorts[c.Pool.Port] = true
	}

	// First pass: assign ports from portMap for existing nodes
	for idx := range c.Nodes {
		c.Nodes[idx].Name = strings.TrimSpace(c.Nodes[idx].Name)
		c.Nodes[idx].URI = strings.TrimSpace(c.Nodes[idx].URI)
		if c.Nodes[idx].URI == "" {
			return fmt.Errorf("node %d is missing uri", idx)
		}

		// 回填稳定 ID（source 已确定）
		if c.Nodes[idx].StableID == "" {
			c.Nodes[idx].StableID = StableID(c.Nodes[idx].Source, c.Nodes[idx].SubscriptionID, c.Nodes[idx].URI)
		}

		// Extract name from URI fragment if not provided
		if c.Nodes[idx].Name == "" {
			if parsed, err := url.Parse(c.Nodes[idx].URI); err == nil && parsed.Fragment != "" {
				if decoded, err := url.QueryUnescape(parsed.Fragment); err == nil {
					c.Nodes[idx].Name = decoded
				} else {
					c.Nodes[idx].Name = parsed.Fragment
				}
			}
		}
		if c.Nodes[idx].Name == "" {
			c.Nodes[idx].Name = fmt.Sprintf("node-%d", idx)
		}

		// Check if this node has a preserved port from portMap
		if c.Mode == "multi-port" || c.Mode == "hybrid" {
			nodeKey := c.Nodes[idx].NodeKey()
			if existingPort, ok := portMap[nodeKey]; ok && existingPort > 0 {
				c.Nodes[idx].Port = existingPort
				usedPorts[existingPort] = true
				logger.Debugf("Preserved port %d for node %q", existingPort, c.Nodes[idx].Name)
			}
		}
	}

	// Second pass: assign new ports for nodes without preserved ports
	portCursor := c.MultiPort.BasePort
	for idx := range c.Nodes {
		if c.Nodes[idx].Port == 0 && (c.Mode == "multi-port" || c.Mode == "hybrid") {
			// Find next available port that's not used
			for usedPorts[portCursor] || !isPortAvailable(c.MultiPort.Address, portCursor) {
				portCursor++
				if portCursor > 65535 {
					return fmt.Errorf("no available ports found starting from %d", c.MultiPort.BasePort)
				}
			}
			c.Nodes[idx].Port = portCursor
			usedPorts[portCursor] = true
			logger.Debugf("Assigned new port %d for node %q", portCursor, c.Nodes[idx].Name)
			portCursor++
		} else if c.Nodes[idx].Port == 0 {
			c.Nodes[idx].Port = portCursor
			portCursor++
		}

		// Apply default credentials
		if c.Mode == "multi-port" || c.Mode == "hybrid" {
			if c.Nodes[idx].Username == "" {
				c.Nodes[idx].Username = c.MultiPort.Username
				c.Nodes[idx].Password = c.MultiPort.Password
			}
		}
	}

	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if c.SingboxLogLevel == "" {
		c.SingboxLogLevel = "warn" // 默认抑制 sing-box 的 info 级别日志
	}

	return nil
}

// ManagementEnabled reports whether the monitoring endpoint should run.
func (c *Config) ManagementEnabled() bool {
	if c.Management.Enabled == nil {
		return true
	}
	return *c.Management.Enabled
}

// AlertsEnabled reports whether the security alert (empty-password) check is active. Defaults to true.
func (c *Config) AlertsEnabled() bool {
	if c.AlertEnabled == nil {
		return true
	}
	return *c.AlertEnabled
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

// loadNodesFromFile reads a nodes file where each line is a proxy URI
// Lines starting with # are comments, empty lines are ignored
func loadNodesFromFile(path string) ([]NodeConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseNodesFromContent(string(data))
}

// loadNodesFromSubscription fetches and parses nodes from a subscription URL
// Supports multiple formats: base64 encoded, plain text, clash yaml, etc.
func loadNodesFromSubscription(subURL string, timeout time.Duration) ([]NodeConfig, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	client := &http.Client{
		Timeout: timeout,
	}

	req, err := http.NewRequest("GET", subURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	// Set common headers to avoid being blocked
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "*/*")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch subscription: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("subscription returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	content := string(body)

	// Try to detect and parse different formats
	return ParseSubscriptionContent(content)
}

// ParseSubscriptionContent tries to parse subscription content in various formats
// (base64 / plain text / Clash YAML). Exported so the subscription refresher reuses
// the full parser (incl. Clash YAML) instead of a plain-text/base64-only fallback.
func ParseSubscriptionContent(content string) ([]NodeConfig, error) {
	content = strings.TrimSpace(content)

	// Check if it's base64 encoded (common for v2ray subscriptions)
	if isBase64(content) {
		decoded, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			// Try URL-safe base64
			decoded, err = base64.RawStdEncoding.DecodeString(content)
			if err != nil {
				// Not base64, try as plain text
				return parseNodesFromContent(content)
			}
		}
		content = string(decoded)
	}

	// Check if it's YAML (Clash format)
	if strings.Contains(content, "proxies:") {
		return parseClashYAML(content)
	}

	// Parse as plain text (one URI per line)
	return parseNodesFromContent(content)
}

// parseNodesFromContent parses nodes from plain text content (one URI per line)
func parseNodesFromContent(content string) ([]NodeConfig, error) {
	var nodes []NodeConfig
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Check if it's a valid proxy URI
		if isProxyURI(line) {
			nodes = append(nodes, NodeConfig{
				URI: line,
			})
		}
	}

	return nodes, nil
}

// isBase64 checks if a string looks like base64 encoded content
func isBase64(s string) bool {
	// Remove whitespace
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return false
	}

	// Base64 should not contain newlines in the middle (unless it's multi-line base64)
	// and should only contain valid base64 characters
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")

	// Check if it contains proxy URI schemes (then it's not base64)
	if strings.Contains(s, "://") {
		return false
	}

	// Try to decode
	_, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		_, err = base64.RawStdEncoding.DecodeString(s)
	}
	return err == nil
}

// isProxyURI checks if a string is a valid proxy URI
func isProxyURI(s string) bool {
	schemes := []string{"vmess://", "vless://", "trojan://", "ss://", "ssr://", "hysteria://", "hysteria2://", "hy2://"}
	for _, scheme := range schemes {
		if strings.HasPrefix(strings.ToLower(s), scheme) {
			return true
		}
	}
	return false
}

// clashConfig represents a minimal Clash configuration for parsing proxies
type clashConfig struct {
	Proxies []clashProxy `yaml:"proxies"`
}

type clashProxy struct {
	Name           string                 `yaml:"name"`
	Type           string                 `yaml:"type"`
	Server         string                 `yaml:"server"`
	Port           int                    `yaml:"port"`
	UUID           string                 `yaml:"uuid"`
	Password       string                 `yaml:"password"`
	Cipher         string                 `yaml:"cipher"`
	AlterId        int                    `yaml:"alterId"`
	Network        string                 `yaml:"network"`
	TLS            bool                   `yaml:"tls"`
	SkipCertVerify bool                   `yaml:"skip-cert-verify"`
	ServerName     string                 `yaml:"servername"`
	SNI            string                 `yaml:"sni"`
	Flow           string                 `yaml:"flow"`
	UDP            bool                   `yaml:"udp"`
	WSOpts         *clashWSOptions        `yaml:"ws-opts"`
	GrpcOpts       *clashGrpcOptions      `yaml:"grpc-opts"`
	RealityOpts    *clashRealityOptions   `yaml:"reality-opts"`
	ClientFingerprint string              `yaml:"client-fingerprint"`
	Plugin         string                 `yaml:"plugin"`
	PluginOpts     map[string]interface{} `yaml:"plugin-opts"`
}

type clashWSOptions struct {
	Path    string            `yaml:"path"`
	Headers map[string]string `yaml:"headers"`
}

type clashGrpcOptions struct {
	GrpcServiceName string `yaml:"grpc-service-name"`
}

type clashRealityOptions struct {
	PublicKey string `yaml:"public-key"`
	ShortID   string `yaml:"short-id"`
}

// parseClashYAML parses Clash YAML format and converts to NodeConfig
func parseClashYAML(content string) ([]NodeConfig, error) {
	var clash clashConfig
	if err := yaml.Unmarshal([]byte(content), &clash); err != nil {
		return nil, fmt.Errorf("parse clash yaml: %w", err)
	}

	var nodes []NodeConfig
	for _, proxy := range clash.Proxies {
		uri := convertClashProxyToURI(proxy)
		if uri != "" {
			nodes = append(nodes, NodeConfig{
				Name: proxy.Name,
				URI:  uri,
			})
		}
	}

	return nodes, nil
}

// convertClashProxyToURI converts a Clash proxy config to a standard URI
func convertClashProxyToURI(p clashProxy) string {
	switch strings.ToLower(p.Type) {
	case "vmess":
		return buildVMessURI(p)
	case "vless":
		return buildVLESSURI(p)
	case "trojan":
		return buildTrojanURI(p)
	case "ss", "shadowsocks":
		return buildShadowsocksURI(p)
	case "hysteria2", "hy2":
		return buildHysteria2URI(p)
	default:
		return ""
	}
}

func buildVMessURI(p clashProxy) string {
	params := url.Values{}
	if p.Network != "" && p.Network != "tcp" {
		params.Set("type", p.Network)
	}
	if p.TLS {
		params.Set("security", "tls")
		if p.ServerName != "" {
			params.Set("sni", p.ServerName)
		} else if p.SNI != "" {
			params.Set("sni", p.SNI)
		}
	}
	if p.WSOpts != nil {
		if p.WSOpts.Path != "" {
			params.Set("path", p.WSOpts.Path)
		}
		if host, ok := p.WSOpts.Headers["Host"]; ok {
			params.Set("host", host)
		}
	}
	if p.ClientFingerprint != "" {
		params.Set("fp", p.ClientFingerprint)
	}

	query := ""
	if len(params) > 0 {
		query = "?" + params.Encode()
	}

	return fmt.Sprintf("vmess://%s@%s:%d%s#%s", p.UUID, p.Server, p.Port, query, url.QueryEscape(p.Name))
}

func buildVLESSURI(p clashProxy) string {
	params := url.Values{}
	params.Set("encryption", "none")

	if p.Network != "" && p.Network != "tcp" {
		params.Set("type", p.Network)
	}
	if p.Flow != "" {
		params.Set("flow", p.Flow)
	}
	if p.TLS {
		params.Set("security", "tls")
		if p.ServerName != "" {
			params.Set("sni", p.ServerName)
		} else if p.SNI != "" {
			params.Set("sni", p.SNI)
		}
	}
	if p.RealityOpts != nil {
		params.Set("security", "reality")
		if p.RealityOpts.PublicKey != "" {
			params.Set("pbk", p.RealityOpts.PublicKey)
		}
		if p.RealityOpts.ShortID != "" {
			params.Set("sid", p.RealityOpts.ShortID)
		}
		if p.ServerName != "" {
			params.Set("sni", p.ServerName)
		}
	}
	if p.WSOpts != nil {
		if p.WSOpts.Path != "" {
			params.Set("path", p.WSOpts.Path)
		}
		if host, ok := p.WSOpts.Headers["Host"]; ok {
			params.Set("host", host)
		}
	}
	if p.GrpcOpts != nil && p.GrpcOpts.GrpcServiceName != "" {
		params.Set("serviceName", p.GrpcOpts.GrpcServiceName)
	}
	if p.ClientFingerprint != "" {
		params.Set("fp", p.ClientFingerprint)
	}

	return fmt.Sprintf("vless://%s@%s:%d?%s#%s", p.UUID, p.Server, p.Port, params.Encode(), url.QueryEscape(p.Name))
}

func buildTrojanURI(p clashProxy) string {
	params := url.Values{}
	if p.Network != "" && p.Network != "tcp" {
		params.Set("type", p.Network)
	}
	if p.ServerName != "" {
		params.Set("sni", p.ServerName)
	} else if p.SNI != "" {
		params.Set("sni", p.SNI)
	}
	if p.SkipCertVerify {
		params.Set("allowInsecure", "1")
	}
	if p.WSOpts != nil {
		if p.WSOpts.Path != "" {
			params.Set("path", p.WSOpts.Path)
		}
		if host, ok := p.WSOpts.Headers["Host"]; ok {
			params.Set("host", host)
		}
	}
	if p.ClientFingerprint != "" {
		params.Set("fp", p.ClientFingerprint)
	}

	query := ""
	if len(params) > 0 {
		query = "?" + params.Encode()
	}

	return fmt.Sprintf("trojan://%s@%s:%d%s#%s", p.Password, p.Server, p.Port, query, url.QueryEscape(p.Name))
}

func buildShadowsocksURI(p clashProxy) string {
	// Encode method:password in base64
	userInfo := base64.StdEncoding.EncodeToString([]byte(p.Cipher + ":" + p.Password))
	return fmt.Sprintf("ss://%s@%s:%d#%s", userInfo, p.Server, p.Port, url.QueryEscape(p.Name))
}

func buildHysteria2URI(p clashProxy) string {
	params := url.Values{}
	if p.ServerName != "" {
		params.Set("sni", p.ServerName)
	} else if p.SNI != "" {
		params.Set("sni", p.SNI)
	}
	if p.SkipCertVerify {
		params.Set("insecure", "1")
	}

	query := ""
	if len(params) > 0 {
		query = "?" + params.Encode()
	}

	return fmt.Sprintf("hysteria2://%s@%s:%d%s#%s", p.Password, p.Server, p.Port, query, url.QueryEscape(p.Name))
}

// FilePath returns the config file path.
func (c *Config) FilePath() string {
	if c == nil {
		return ""
	}
	return c.filePath
}

// SetFilePath sets the config file path (used when creating config programmatically).
func (c *Config) SetFilePath(path string) {
	if c != nil {
		c.filePath = path
	}
}

// writeNodesToFile writes nodes to a file (one URI per line).
// If node has a name, it will be encoded in the URI fragment.
func writeNodesToFile(path string, nodes []NodeConfig) error {
	var lines []string
	for _, node := range nodes {
		uri := node.URI
		// 如果节点有名称，更新URI的fragment部分
		if node.Name != "" {
			if parsed, err := url.Parse(uri); err == nil {
				parsed.Fragment = node.Name
				uri = parsed.String()
			}
		}
		lines = append(lines, uri)
	}
	content := strings.Join(lines, "\n")
	if len(lines) > 0 {
		content += "\n"
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// SaveNodes persists inline/manual nodes to config.yaml.
// nodes.txt 不再整体覆写（修复 D1：避免订阅刷新吞掉手动节点）。
//   inline/manual → config.yaml nodes[]
//   subscription  → bbolt（阶段2 实体化），运行时由订阅刷新产生，不入文件
//   nodes_file    → 一次性导入源，不回写
func (c *Config) SaveNodes() error {
	if c == nil {
		return errors.New("config is nil")
	}
	if c.filePath == "" {
		return errors.New("config file path is unknown")
	}

	c.saveMu.Lock()
	defer c.saveMu.Unlock()

	var persistNodes []NodeConfig
	for _, node := range c.Nodes {
		switch node.Source {
		case NodeSourceInline, NodeSourceManual:
			persistNodes = append(persistNodes, NodeConfig{
				Name:           node.Name,
				URI:            node.URI,
				Port:           node.Port,
				Username:       node.Username,
				Password:       node.Password,
				Source:         node.Source,
				StableID:       node.StableID,
				SubscriptionID: node.SubscriptionID,
				DuplicateOf:    node.DuplicateOf,
				CountryCode:    node.CountryCode,
			})
		}
	}

	// 读原 config 保留结构，只更新 nodes[]
	data, err := os.ReadFile(c.filePath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	var saveCfg Config
	if err := yaml.Unmarshal(data, &saveCfg); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}
	if len(persistNodes) == 0 && len(saveCfg.Nodes) == 0 {
		return nil // 无 inline/manual 节点且原配置也无，无需写
	}
	saveCfg.Nodes = persistNodes

	newData, err := yaml.Marshal(&saveCfg)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := os.WriteFile(c.filePath, newData, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// Save is deprecated, use SaveNodes instead.
// This method is kept for backward compatibility but now delegates to SaveNodes.
func (c *Config) Save() error {
	return c.SaveNodes()
}

// EnsureCredentialKey 确保凭证加密密钥存在；为空则生成 32 字节随机密钥并持久化到 config.yaml。
// 密钥存配置文件、与 db 文件分离（db 只存 AES 密文），故 db 单独泄露时无法解密凭证。
// 密钥纯服务端使用，前端/浏览器永不接触。
func (c *Config) EnsureCredentialKey() error {
	if c.CredentialKey != "" {
		return nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("generate credential key: %w", err)
	}
	c.CredentialKey = hex.EncodeToString(key)
	return c.SaveSettings()
}

// SaveSettings persists only config settings (external_ip, probe_target, skip_cert_verify, credential_key)
// without touching nodes.txt. Use this for settings API updates.
func (c *Config) SaveSettings() error {
	if c == nil {
		return errors.New("config is nil")
	}
	if c.filePath == "" {
		return errors.New("config file path is unknown")
	}

	c.saveMu.Lock()
	defer c.saveMu.Unlock()

	data, err := os.ReadFile(c.filePath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	var saveCfg Config
	if err := yaml.Unmarshal(data, &saveCfg); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}

	saveCfg.ExternalIP = c.ExternalIP
	saveCfg.Management.ProbeTarget = c.Management.ProbeTarget
	saveCfg.SkipCertVerify = c.SkipCertVerify
	saveCfg.AlertEnabled = c.AlertEnabled
	saveCfg.CredentialKey = c.CredentialKey

	newData, err := yaml.Marshal(&saveCfg)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := os.WriteFile(c.filePath, newData, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// SaveVirtualPools 持久化 virtual_pools 段到 config.yaml（读原文件保留其他段，仅替换 virtual_pools）。
// 供虚拟池 API CRUD 回写，使页面创建/修改的虚拟池落盘可被用户二次编辑。
func (c *Config) SaveVirtualPools() error {
	if c == nil {
		return errors.New("config is nil")
	}
	if c.filePath == "" {
		return errors.New("config file path is unknown")
	}

	c.saveMu.Lock()
	defer c.saveMu.Unlock()

	data, err := os.ReadFile(c.filePath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	var saveCfg Config
	if err := yaml.Unmarshal(data, &saveCfg); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}
	saveCfg.VirtualPools = c.VirtualPools

	newData, err := yaml.Marshal(&saveCfg)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := os.WriteFile(c.filePath, newData, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// SaveSubscriptions 持久化 subscriptions 段（[]string "name:url"）到 config.yaml。
// 订阅运行时状态（刷新状态/节点数/时间戳）留在 bbolt，不入 yaml；此方法只写用户可编辑的订阅链接清单。
func (c *Config) SaveSubscriptions() error {
	if c == nil {
		return errors.New("config is nil")
	}
	if c.filePath == "" {
		return errors.New("config file path is unknown")
	}
	c.saveMu.Lock()
	defer c.saveMu.Unlock()
	return c.saveSubscriptionsLocked()
}

// saveSubscriptionsLocked 持 saveMu 时写 subscriptions 段（调用方持锁）。
func (c *Config) saveSubscriptionsLocked() error {
	data, err := os.ReadFile(c.filePath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	var saveCfg Config
	if err := yaml.Unmarshal(data, &saveCfg); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}
	saveCfg.Subscriptions = c.Subscriptions

	newData, err := yaml.Marshal(&saveCfg)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := os.WriteFile(c.filePath, newData, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// SubscriptionsList 返回订阅列表的快照拷贝（持锁，供并发安全读取）。
func (c *Config) SubscriptionsList() []string {
	if c == nil {
		return nil
	}
	c.saveMu.Lock()
	defer c.saveMu.Unlock()
	out := make([]string, len(c.Subscriptions))
	copy(out, c.Subscriptions)
	return out
}

// AddSubscription 追加订阅到 yaml（name:url），按 url 幂等（已存在则更新 name）。持 saveMu 原子写。
// 供订阅 CRUD 的 yaml-first 提交：先落 yaml（权威源），再写 bbolt；bbolt 失败时 yaml 领先，
// 下次启动 SyncSubscriptions 会按 yaml 补建 bbolt，崩溃安全。
func (c *Config) AddSubscription(name, url string) error {
	if c == nil {
		return errors.New("config is nil")
	}
	if c.filePath == "" {
		return errors.New("config file path is unknown")
	}
	entry := formatSubscriptionEntry(name, url)
	c.saveMu.Lock()
	defer c.saveMu.Unlock()
	if idx := c.findSubscriptionIndexLocked(url); idx >= 0 {
		c.Subscriptions[idx] = entry // 幂等：同 url 更新 name
	} else {
		c.Subscriptions = append(c.Subscriptions, entry)
	}
	return c.saveSubscriptionsLocked()
}

// RemoveSubscription 按 url 移除订阅条目（未找到视为已删除，幂等）。持 saveMu 原子写。
func (c *Config) RemoveSubscription(url string) error {
	if c == nil {
		return errors.New("config is nil")
	}
	if c.filePath == "" {
		return errors.New("config file path is unknown")
	}
	c.saveMu.Lock()
	defer c.saveMu.Unlock()
	idx := c.findSubscriptionIndexLocked(url)
	if idx < 0 {
		return nil
	}
	c.Subscriptions = append(c.Subscriptions[:idx], c.Subscriptions[idx+1:]...)
	return c.saveSubscriptionsLocked()
}

// UpdateSubscriptionEntry 把 oldURL 对应条目改为新 name:url；oldURL 不存在则追加。持 saveMu 原子写。
func (c *Config) UpdateSubscriptionEntry(oldURL, name, url string) error {
	if c == nil {
		return errors.New("config is nil")
	}
	if c.filePath == "" {
		return errors.New("config file path is unknown")
	}
	entry := formatSubscriptionEntry(name, url)
	c.saveMu.Lock()
	defer c.saveMu.Unlock()
	if idx := c.findSubscriptionIndexLocked(oldURL); idx >= 0 {
		c.Subscriptions[idx] = entry
	} else {
		c.Subscriptions = append(c.Subscriptions, entry)
	}
	return c.saveSubscriptionsLocked()
}

// findSubscriptionIndexLocked 按 url 查找订阅条目索引（调用方持 saveMu）。
func (c *Config) findSubscriptionIndexLocked(targetURL string) int {
	for i, e := range c.Subscriptions {
		_, u := parseSubscriptionEntry(e)
		if u != "" && u == targetURL {
			return i
		}
	}
	return -1
}

// formatSubscriptionEntry 编码 "name:url"（无 name 则裸 url），与 parseSubscriptionEntry 互逆。
func formatSubscriptionEntry(name, url string) string {
	name = strings.TrimSpace(name)
	url = strings.TrimSpace(url)
	if name == "" {
		return url
	}
	return name + ":" + url
}

// isPortAvailable checks if a port is available for binding.
func isPortAvailable(address string, port uint16) bool {
	addr := fmt.Sprintf("%s:%d", address, port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// validateVirtualPools 验证虚拟池配置
// 检查名称唯一性、端口冲突、正则表达式语法、策略有效性
func (c *Config) validateVirtualPools() error {
	if len(c.VirtualPools) == 0 {
		return nil
	}

	// 收集已使用的端口
	usedPorts := make(map[uint16]string) // port -> owner name

	// 添加 listener 端口（pool/hybrid 模式）
	if c.Mode == "pool" || c.Mode == "hybrid" {
		usedPorts[c.Pool.Port] = "listener"
	}

	// 添加 management 端口
	if c.ManagementEnabled() {
		_, portStr, err := net.SplitHostPort(c.Management.Listen)
		if err == nil {
			var mgmtPort int
			fmt.Sscanf(portStr, "%d", &mgmtPort)
			if mgmtPort > 0 && mgmtPort <= 65535 {
				usedPorts[uint16(mgmtPort)] = "management"
			}
		}
	}

	// 添加节点端口（multi-port/hybrid 模式）
	if c.Mode == "multi-port" || c.Mode == "hybrid" {
		for _, node := range c.Nodes {
			if node.Port > 0 {
				usedPorts[node.Port] = fmt.Sprintf("node:%s", node.Name)
			}
		}
	}

	// 虚拟池端口自动分配起点：有留空端口的池时必填 virtual_pool.base_port
	if c.VirtualPool.BasePort == 0 {
		for _, p := range c.VirtualPools {
			if p.Port == 0 {
				return fmt.Errorf("virtual_pool.base_port is required (virtual pool %q has no explicit port)", p.Name)
			}
		}
	}

	// 收集虚拟池名称用于唯一性检查
	poolNames := make(map[string]bool)

	for idx, pool := range c.VirtualPools {
		// 验证名称非空
		if pool.Name == "" {
			return fmt.Errorf("virtual_pools[%d]: name is required", idx)
		}

		// 验证名称唯一性
		if poolNames[pool.Name] {
			return fmt.Errorf("virtual_pools[%d]: duplicate pool name %q", idx, pool.Name)
		}
		poolNames[pool.Name] = true

		// 正则可选：为空表示不按节点名过滤（仅按国家码/延迟）；非空时校验语法
		if pool.Regular != "" {
			if _, err := regexp2.Compile(pool.Regular, regexp2.RE2); err != nil {
				return fmt.Errorf("virtual_pools[%d] %q: invalid regular expression %q: %w", idx, pool.Name, pool.Regular, err)
			}
		}

		// 验证地址非空
		if pool.Address == "" {
			return fmt.Errorf("virtual_pools[%d] %q: address is required", idx, pool.Name)
		}

		// 端口：留空(0) 表示运行时自动分配（需 virtual_pool.base_port）；非空时校验范围+冲突
		if pool.Port > 65535 {
			return fmt.Errorf("virtual_pools[%d] %q: port %d is out of range (1-65535)", idx, pool.Name, pool.Port)
		}
		if pool.Port != 0 {
			if owner, exists := usedPorts[pool.Port]; exists {
				return fmt.Errorf("virtual_pools[%d] %q: port %d conflicts with %s", idx, pool.Name, pool.Port, owner)
			}
			usedPorts[pool.Port] = fmt.Sprintf("virtual_pool:%s", pool.Name)
		}

		// 验证负载均衡策略
		if pool.Strategy != "" {
			switch pool.Strategy {
			case "sequential", "random", "balance", "weighted":
				// 有效策略
			default:
				return fmt.Errorf("virtual_pools[%d] %q: invalid strategy %q (use 'sequential', 'random', 'balance', or 'weighted')", idx, pool.Name, pool.Strategy)
			}
		}

		// weighted 策略必须显式提供两个权重（均 >0）；其余策略忽略权重
		if c.VirtualPools[idx].Strategy == "weighted" {
			if pool.LatencyWeight <= 0 || pool.AvailabilityWeight <= 0 {
				return fmt.Errorf("virtual_pools[%d] %q: strategy 'weighted' requires latency_weight>0 and availability_weight>0", idx, pool.Name)
			}
		}

		// 设置默认策略
		if c.VirtualPools[idx].Strategy == "" {
			c.VirtualPools[idx].Strategy = "sequential"
		}

		// 设置默认地址
		if c.VirtualPools[idx].Address == "" {
			c.VirtualPools[idx].Address = "0.0.0.0"
		}

		// 验证延迟阈值
		if pool.MaxLatencyMs < 0 {
			return fmt.Errorf("virtual_pools[%d] %q: max_latency_ms cannot be negative", idx, pool.Name)
		}
	}

	return nil
}
