package virtualpool

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"easy_proxies/internal/config"
	"easy_proxies/internal/logger"
	"easy_proxies/internal/monitor"

	"github.com/dlclark/regexp2"
)

// VirtualPool 虚拟池实例
// 每个虚拟池有独立的监听端口，通过正则表达式筛选节点
type VirtualPool struct {
	cfg        config.VirtualPoolConfig // 虚拟池配置
	globalCfg  *config.Config           // 全局配置
	monitorMgr *monitor.Manager         // 节点监控管理器
	regex      *regexp2.Regexp         // 编译后的正则表达式（支持零宽断言）
	listener   net.Listener             // TCP 监听器
	ctx        context.Context
	cancel     context.CancelFunc
	running    atomic.Bool
	rrCounter  atomic.Uint32 // 轮询计数器
	rng        *rand.Rand    // 随机数生成器
	rngMu      sync.Mutex

	// 节点缓存
	nodeCache     []monitor.Snapshot
	nodeCacheMu   sync.RWMutex
	lastCacheTime time.Time
	cacheTTL      time.Duration
}

// NewVirtualPool 创建虚拟池实例
func NewVirtualPool(ctx context.Context, cfg config.VirtualPoolConfig, monitorMgr *monitor.Manager, globalCfg *config.Config) (*VirtualPool, error) {
	// 编译正则表达式（使用 regexp2 支持零宽断言）
	regex, err := regexp2.Compile(cfg.Regular, regexp2.RE2)
	if err != nil {
		return nil, fmt.Errorf("compile regex: %w", err)
	}

	poolCtx, cancel := context.WithCancel(ctx)

	return &VirtualPool{
		cfg:        cfg,
		globalCfg:  globalCfg,
		monitorMgr: monitorMgr,
		regex:      regex,
		ctx:        poolCtx,
		cancel:     cancel,
		rng:        rand.New(rand.NewSource(time.Now().UnixNano())),
		cacheTTL:   30 * time.Second, // 缓存 30 秒
	}, nil
}

// Start 启动虚拟池监听
func (p *VirtualPool) Start() error {
	addr := fmt.Sprintf("%s:%d", p.cfg.Address, p.cfg.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	p.listener = listener
	p.running.Store(true)

	// 启动连接处理协程
	go p.acceptLoop()

	// 启动缓存更新协程
	go p.cacheUpdateLoop()

	return nil
}

// Stop 停止虚拟池
func (p *VirtualPool) Stop() {
	p.running.Store(false)
	p.cancel()
	if p.listener != nil {
		p.listener.Close()
	}
}

// Status 获取虚拟池状态
func (p *VirtualPool) Status() PoolStatus {
	nodes := p.getMatchingNodes()
	return PoolStatus{
		Name:         p.cfg.Name,
		Regular:      p.cfg.Regular,
		Address:      p.cfg.Address,
		Port:         p.cfg.Port,
		Strategy:     p.cfg.Strategy,
		MaxLatencyMs: p.cfg.MaxLatencyMs,
		NodeCount:    len(nodes),
		Running:      p.running.Load(),
		Username:     p.cfg.Username,
		Password:     p.cfg.Password,
	}
}

// GetMatchingNodes 获取匹配的节点列表（公开方法，用于 API）
func (p *VirtualPool) GetMatchingNodes() []monitor.Snapshot {
	return p.getMatchingNodes()
}

// acceptLoop 接受连接循环
func (p *VirtualPool) acceptLoop() {
	for {
		conn, err := p.listener.Accept()
		if err != nil {
			if p.running.Load() {
				logger.Warnf("Virtual pool %q accept error: %v", p.cfg.Name, err)
			}
			return
		}

		go p.handleConnection(conn)
	}
}

// cacheUpdateLoop 缓存更新循环
func (p *VirtualPool) cacheUpdateLoop() {
	ticker := time.NewTicker(p.cacheTTL)
	defer ticker.Stop()

	// 立即更新一次缓存
	p.updateNodeCache()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.updateNodeCache()
		}
	}
}

// updateNodeCache 更新节点缓存
func (p *VirtualPool) updateNodeCache() {
	// 获取所有可用节点
	allNodes := p.monitorMgr.SnapshotFiltered(true)

	// 筛选匹配正则的节点
	var matchedNodes []monitor.Snapshot
	for _, node := range allNodes {
		matched, err := p.regex.MatchString(node.Name)
		if err != nil {
			logger.Warnf("Virtual pool %q regex match error: %v", p.cfg.Name, err)
			continue
		}
		if matched {
			// 检查延迟阈值
			if p.cfg.MaxLatencyMs > 0 && node.LastLatencyMs > 0 {
				if node.LastLatencyMs > int64(p.cfg.MaxLatencyMs) {
					continue
				}
			}
			matchedNodes = append(matchedNodes, node)
		}
	}

	p.nodeCacheMu.Lock()
	p.nodeCache = matchedNodes
	p.lastCacheTime = time.Now()
	p.nodeCacheMu.Unlock()
}

// getMatchingNodes 获取匹配的节点（带缓存）
func (p *VirtualPool) getMatchingNodes() []monitor.Snapshot {
	p.nodeCacheMu.RLock()
	cache := p.nodeCache
	cacheTime := p.lastCacheTime
	p.nodeCacheMu.RUnlock()

	// 如果缓存有效，直接返回
	if time.Since(cacheTime) < p.cacheTTL && len(cache) > 0 {
		return cache
	}

	// 缓存过期，重新获取
	p.updateNodeCache()

	p.nodeCacheMu.RLock()
	defer p.nodeCacheMu.RUnlock()
	return p.nodeCache
}

// selectNode 根据策略选择节点
func (p *VirtualPool) selectNode(nodes []monitor.Snapshot) *monitor.Snapshot {
	if len(nodes) == 0 {
		return nil
	}

	var idx int
	switch p.cfg.Strategy {
	case "random":
		p.rngMu.Lock()
		idx = p.rng.Intn(len(nodes))
		p.rngMu.Unlock()
	case "balance":
		// 选择活跃连接数最少的节点
		minActive := int32(1<<31 - 1)
		for i, node := range nodes {
			if node.ActiveConnections < minActive {
				minActive = node.ActiveConnections
				idx = i
			}
		}
	default: // sequential
		idx = int(p.rrCounter.Add(1)-1) % len(nodes)
	}

	return &nodes[idx]
}

// handleConnection 处理客户端连接
func (p *VirtualPool) handleConnection(clientConn net.Conn) {
	defer clientConn.Close()

	// 设置读取超时
	clientConn.SetReadDeadline(time.Now().Add(30 * time.Second))

	// 读取 HTTP CONNECT 请求
	reader := bufio.NewReader(clientConn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		logger.Warnf("Virtual pool %q read request error: %v", p.cfg.Name, err)
		return
	}

	// 验证认证（如果配置了）
	if p.cfg.Username != "" || p.cfg.Password != "" {
		if !p.authenticate(req) {
			p.sendResponse(clientConn, "407 Proxy Authentication Required", map[string]string{
				"Proxy-Authenticate": "Basic realm=\"Virtual Pool\"",
			})
			return
		}
	}

	// 获取目标地址
	targetHost := req.Host
	if targetHost == "" {
		targetHost = req.URL.Host
	}
	if targetHost == "" {
		p.sendResponse(clientConn, "400 Bad Request", nil)
		return
	}

	// 确保有端口
	if !strings.Contains(targetHost, ":") {
		if req.URL.Scheme == "https" || req.Method == "CONNECT" {
			targetHost += ":443"
		} else {
			targetHost += ":80"
		}
	}

	// 选择一个节点
	nodes := p.getMatchingNodes()
	if len(nodes) == 0 {
		logger.Warnf("Virtual pool %q has no available nodes", p.cfg.Name)
		p.sendResponse(clientConn, "503 Service Unavailable", nil)
		return
	}

	selectedNode := p.selectNode(nodes)
	if selectedNode == nil {
		p.sendResponse(clientConn, "503 Service Unavailable", nil)
		return
	}

	// 调试日志：显示选择的节点（归为 debug 级别）
	logger.Debugf("Virtual pool %q selected node: %s (port: %d, strategy: %s, total nodes: %d)",
		p.cfg.Name, selectedNode.Name, selectedNode.Port, p.cfg.Strategy, len(nodes))

	// 清除读取超时
	clientConn.SetReadDeadline(time.Time{})

	// 连接到选中的节点代理
	proxyAddr := fmt.Sprintf("%s:%d", p.getProxyHost(selectedNode), selectedNode.Port)
	proxyConn, err := net.DialTimeout("tcp", proxyAddr, 10*time.Second)
	if err != nil {
		logger.Warnf("Virtual pool %q connect to proxy %s error: %v", p.cfg.Name, proxyAddr, err)
		p.sendResponse(clientConn, "502 Bad Gateway", nil)
		return
	}
	defer proxyConn.Close()

	// 构建发送给上游代理的 CONNECT 请求
	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", targetHost, targetHost)

	// 添加上游代理认证
	if selectedNode.Mode == "multi-port" || selectedNode.Mode == "hybrid" {
		// 从全局配置获取认证信息
		username := p.globalCfg.MultiPort.Username
		password := p.globalCfg.MultiPort.Password
		if username != "" {
			auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
			connectReq += fmt.Sprintf("Proxy-Authorization: Basic %s\r\n", auth)
		}
	}
	connectReq += "\r\n"

	// 发送 CONNECT 请求到上游代理
	_, err = proxyConn.Write([]byte(connectReq))
	if err != nil {
		logger.Warnf("Virtual pool %q send CONNECT error: %v", p.cfg.Name, err)
		p.sendResponse(clientConn, "502 Bad Gateway", nil)
		return
	}

	// 读取上游代理响应
	proxyReader := bufio.NewReader(proxyConn)
	resp, err := http.ReadResponse(proxyReader, nil)
	if err != nil {
		logger.Warnf("Virtual pool %q read proxy response error: %v", p.cfg.Name, err)
		p.sendResponse(clientConn, "502 Bad Gateway", nil)
		return
	}
	resp.Body.Close()

	if resp.StatusCode != 200 {
		logger.Warnf("Virtual pool %q proxy returned %d", p.cfg.Name, resp.StatusCode)
		p.sendResponse(clientConn, fmt.Sprintf("%d %s", resp.StatusCode, resp.Status), nil)
		return
	}

	// 向客户端发送成功响应
	p.sendResponse(clientConn, "200 Connection Established", nil)

	// 开始双向转发
	p.relay(clientConn, proxyConn)
}

// getProxyHost 获取代理主机地址
func (p *VirtualPool) getProxyHost(node *monitor.Snapshot) string {
	// 优先使用节点的监听地址
	if node.ListenAddress != "" && node.ListenAddress != "0.0.0.0" {
		return node.ListenAddress
	}
	// 使用 multi_port 配置的地址
	if p.globalCfg.MultiPort.Address != "" && p.globalCfg.MultiPort.Address != "0.0.0.0" {
		return p.globalCfg.MultiPort.Address
	}
	// 回退到 localhost
	return "127.0.0.1"
}

// authenticate 验证认证信息
func (p *VirtualPool) authenticate(req *http.Request) bool {
	auth := req.Header.Get("Proxy-Authorization")
	if auth == "" {
		return false
	}

	// 解析 Basic 认证
	if !strings.HasPrefix(auth, "Basic ") {
		return false
	}

	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(auth, "Basic "))
	if err != nil {
		return false
	}

	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return false
	}

	return parts[0] == p.cfg.Username && parts[1] == p.cfg.Password
}

// sendResponse 发送 HTTP 响应
func (p *VirtualPool) sendResponse(conn net.Conn, status string, headers map[string]string) {
	response := fmt.Sprintf("HTTP/1.1 %s\r\n", status)
	for k, v := range headers {
		response += fmt.Sprintf("%s: %s\r\n", k, v)
	}
	response += "\r\n"
	conn.Write([]byte(response))
}

// relay 双向转发数据
func (p *VirtualPool) relay(client, server net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	// 客户端 -> 服务器
	go func() {
		defer wg.Done()
		io.Copy(server, client)
		// 关闭写入方向
		if tcpConn, ok := server.(*net.TCPConn); ok {
			tcpConn.CloseWrite()
		}
	}()

	// 服务器 -> 客户端
	go func() {
		defer wg.Done()
		io.Copy(client, server)
		// 关闭写入方向
		if tcpConn, ok := client.(*net.TCPConn); ok {
			tcpConn.CloseWrite()
		}
	}()

	wg.Wait()
}
