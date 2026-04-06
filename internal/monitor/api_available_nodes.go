package monitor

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/dlclark/regexp2"
	"github.com/go-chi/render"
)

// ModeChecker 接口用于检查当前运行模式
type ModeChecker interface {
	GetCurrentMode() string
}

// AvailableNodeResponse 单个节点响应
type AvailableNodeResponse struct {
	Tag       string `json:"tag"`
	Name      string `json:"name"`
	ProxyURL  string `json:"proxy_url"`
	LatencyMs int64  `json:"latency_ms"`
}

// AvailableNodesResponse 批量节点响应
type AvailableNodesResponse struct {
	Nodes []AvailableNodeResponse `json:"nodes"`
}

// getAvailableNodesParams 获取可用节点请求参数
type getAvailableNodesParams struct {
	Count             int
	Regular           string
	Strategy          string
	LatencyWeight     string
	SuccessRateWeight string
	WeightMode        string
	WeightedRandom    bool // true: 加权随机选择, false: 选择权重最高的节点
}

// parseGetAvailableNodesParams 解析并验证请求参数
func parseGetAvailableNodesParams(r *http.Request, checker ModeChecker) (*getAvailableNodesParams, error) {
	params := &getAvailableNodesParams{}

	// 获取 count 参数（默认 1，最大 100）
	countStr := r.URL.Query().Get("count")
	if countStr != "" {
		count, err := strconv.Atoi(countStr)
		if err != nil {
			return nil, errors.New("count 参数必须是整数")
		}
		if count < 1 || count > 100 {
			return nil, errors.New("count 参数必须在 1-100 之间")
		}
		params.Count = count
	} else {
		params.Count = 1
	}

	// 获取正则表达式参数
	params.Regular = r.URL.Query().Get("regular")

	// 获取策略参数
	params.Strategy = r.URL.Query().Get("strategy")
	if params.Strategy == "" {
		params.Strategy = "sequential" // 默认策略
	}

	// 验证策略有效性
	validStrategies := map[string]bool{
		"sequential": true,
		"random":     true,
		"balance":    true,
		"weighted":   true,
	}
	if !validStrategies[params.Strategy] {
		return nil, fmt.Errorf("无效的 strategy 参数: %s (支持: sequential/random/balance/weighted)", params.Strategy)
	}

	// 获取权重参数（仅 weighted 策略使用）
	params.LatencyWeight = r.URL.Query().Get("latency_weight")
	params.SuccessRateWeight = r.URL.Query().Get("success_rate_weight")
	params.WeightMode = r.URL.Query().Get("weight_mode")

	// 获取 weighted_random 参数（默认 false）
	weightedRandomStr := r.URL.Query().Get("weighted_random")
	if weightedRandomStr != "" {
		weightedRandom, err := strconv.ParseBool(weightedRandomStr)
		if err != nil {
			return nil, errors.New("weighted_random 参数必须是布尔值")
		}
		params.WeightedRandom = weightedRandom
	} else {
		params.WeightedRandom = false // 默认为 false
	}

	// 验证模式是否支持（仅 multi-port 和 hybrid）
	if checker != nil {
		mode := checker.GetCurrentMode()
		if mode != "multi-port" && mode != "hybrid" {
			return nil, errors.New("此 API 仅在 multi-port 或 hybrid 模式下可用")
		}
	}

	return params, nil
}

// selectAvailableNodes 获取并筛选可用节点
func (s *Server) selectAvailableNodes(params *getAvailableNodesParams) ([]Snapshot, error) {
	// 获取可用节点（已过滤黑名单和离线节点）
	nodes := s.mgr.SnapshotFiltered(true)

	// 应用正则筛选
	if params.Regular != "" {
		filtered, err := s.filterByRegex(nodes, params.Regular)
		if err != nil {
			return nil, fmt.Errorf("无效的正则表达式: %w", err)
		}
		nodes = filtered
	}

	if len(nodes) == 0 {
		return nil, errors.New("无可用节点")
	}

	return nodes, nil
}

// filterByRegex 使用正则表达式筛选节点
func (s *Server) filterByRegex(nodes []Snapshot, pattern string) ([]Snapshot, error) {
	re, err := compileRegex(pattern)
	if err != nil {
		return nil, err
	}

	var result []Snapshot
	for _, node := range nodes {
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

// compileRegex 编译正则表达式，支持零宽断言和中文字符
func compileRegex(pattern string) (*regexp2.Regexp, error) {
	return regexp2.Compile(pattern, regexp2.RE2)
}

// selectNode 根据策略选择单个节点
func (s *Server) selectNode(nodes []Snapshot, params *getAvailableNodesParams) *Snapshot {
	switch params.Strategy {
	case "random":
		return s.selectRandom(nodes)
	case "balance":
		return s.selectLeastActive(nodes)
	case "weighted":
		return s.selectWeighted(nodes, params)
	default: // sequential
		return s.selectSequential(nodes)
	}
}

// selectNodes 批量选择节点
func (s *Server) selectNodes(nodes []Snapshot, count int, params *getAvailableNodesParams) []Snapshot {
	if count <= 1 {
		// 单个节点选择
		selected := s.selectNode(nodes, params)
		if selected != nil {
			return []Snapshot{*selected}
		}
		return nil
	}

	// 批量选择（不重复）
	return s.selectMultiple(nodes, count, params)
}

// selectSequential 顺序轮询选择
func (s *Server) selectSequential(nodes []Snapshot) *Snapshot {
	if len(nodes) == 0 {
		return nil
	}
	// 使用时间戳的秒数作为索引，实现轮询效果
	index := int(time.Now().Unix()) % len(nodes)
	return &nodes[index]
}

// selectRandom 随机选择
func (s *Server) selectRandom(nodes []Snapshot) *Snapshot {
	if len(nodes) == 0 {
		return nil
	}
	// 使用池管理器的随机数生成器
	index := int(time.Now().UnixNano()) % len(nodes)
	return &nodes[index]
}

// selectLeastActive 选择活跃连接数最少的节点
func (s *Server) selectLeastActive(nodes []Snapshot) *Snapshot {
	if len(nodes) == 0 {
		return nil
	}

	var selected *Snapshot
	var minActive int32 = -1

	for i := range nodes {
		active := nodes[i].ActiveConnections
		if minActive < 0 || active < minActive {
			minActive = active
			selected = &nodes[i]
		}
	}

	return selected
}

// selectWeighted 加权选择
func (s *Server) selectWeighted(nodes []Snapshot, params *getAvailableNodesParams) *Snapshot {
	if len(nodes) == 0 {
		return nil
	}

	// 解析权重参数
	weightParams, err := ParseWeightParams(
		params.WeightMode,
		params.LatencyWeight,
		params.SuccessRateWeight,
	)
	if err != nil {
		// 解析失败，使用默认权重
		weightParams, _ = ParseWeightParams("", "", "")
	}

	selector := NewWeightedSelector()
	if params.WeightedRandom {
		// 加权随机选择
		return selector.Select(nodes, weightParams)
	} else {
		// 选择权重最高的节点
		return selector.SelectBest(nodes, weightParams)
	}
}

// selectMultiple 批量选择多个不重复的节点
func (s *Server) selectMultiple(nodes []Snapshot, count int, params *getAvailableNodesParams) []Snapshot {
	if len(nodes) == 0 {
		return nil
	}

	// 如果请求数量大于等于可用节点数量，返回全部节点
	if count >= len(nodes) {
		result := make([]Snapshot, len(nodes))
		copy(result, nodes)
		return result
	}

	// 根据策略选择
	if params.Strategy == "weighted" {
		// 加权批量选择
		weightParams, err := ParseWeightParams(
			params.WeightMode,
			params.LatencyWeight,
			params.SuccessRateWeight,
		)
		if err != nil {
			weightParams, _ = ParseWeightParams("", "", "")
		}

		selector := NewWeightedSelector()
		if params.WeightedRandom {
			// 加权随机批量选择
			selectedSnapshots := selector.SelectMultiple(nodes, count, weightParams)
			return selectedSnapshots
		} else {
			// 选择权重最高的节点
			selectedSnapshots := selector.SelectMultipleBest(nodes, count, weightParams)
			return selectedSnapshots
		}
	}

	// 其他策略：随机选择多个不重复的节点
	selected := make([]Snapshot, 0, count)
	used := make(map[int]bool)
	for len(selected) < count {
		var index int
		switch params.Strategy {
		case "random":
			index = int(time.Now().UnixNano()) % len(nodes)
		case "balance":
			index = s.selectLeastActiveIndex(nodes)
		default: // sequential
			offset := len(selected)
			index = (int(time.Now().Unix()) + offset) % len(nodes)
		}

		if !used[index] {
			selected = append(selected, nodes[index])
			used[index] = true
		}
	}

	return selected
}

// selectLeastActiveIndex 选择活跃连接数最少的节点的索引
func (s *Server) selectLeastActiveIndex(nodes []Snapshot) int {
	if len(nodes) == 0 {
		return 0
	}

	minIndex := 0
	minActive := nodes[0].ActiveConnections

	for i := 1; i < len(nodes); i++ {
		if nodes[i].ActiveConnections < minActive {
			minActive = nodes[i].ActiveConnections
			minIndex = i
		}
	}

	return minIndex
}

// buildProxyURL 构建代理 URL
func (s *Server) buildProxyURL(node Snapshot) string {
	extIP, _, _ := s.getSettings()

	// 使用 external_ip 或 listener.address
	addr := node.ListenAddress
	if (addr == "0.0.0.0" || addr == "") && extIP != "" {
		addr = extIP
	}

	// 构建代理 URL
	var proxyURL string
	if s.cfg.ProxyUsername != "" && s.cfg.ProxyPassword != "" {
		proxyURL = fmt.Sprintf("http://%s:%s@%s:%d",
			s.cfg.ProxyUsername, s.cfg.ProxyPassword,
			addr, node.Port)
	} else {
		proxyURL = fmt.Sprintf("http://%s:%d", addr, node.Port)
	}

	return proxyURL
}

// snapshotToResponse 将 Snapshot 转换为 AvailableNodeResponse
func (s *Server) snapshotToResponse(node Snapshot) AvailableNodeResponse {
	return AvailableNodeResponse{
		Tag:       node.Tag,
		Name:      node.Name,
		ProxyURL:  s.buildProxyURL(node),
		LatencyMs: node.LastLatencyMs,
	}
}

// handleGetAvailableNode 处理获取单个可用节点请求
// GET /api/nodes/get_available_node?regular=<regex>&strategy=<strategy>
func (s *Server) handleGetAvailableNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// 解析参数
	params, err := parseGetAvailableNodesParams(r, s.nodeMgr)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		render.JSON(w, r, map[string]any{"error": err.Error()})
		return
	}

	// 获取并筛选节点
	nodes, err := s.selectAvailableNodes(params)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		render.JSON(w, r, map[string]any{"error": err.Error()})
		return
	}

	// 选择单个节点
	selected := s.selectNode(nodes, params)
	if selected == nil {
		w.WriteHeader(http.StatusInternalServerError)
		render.JSON(w, r, map[string]any{"error": "选择节点失败"})
		return
	}

	// 转换并返回响应
	response := s.snapshotToResponse(*selected)
	render.JSON(w, r, response)
}

// handleGetAvailableNodes 处理批量获取可用节点请求
// GET /api/nodes/get_available_nodes?count=<n>&regular=<regex>&strategy=<strategy>
func (s *Server) handleGetAvailableNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// 解析参数
	params, err := parseGetAvailableNodesParams(r, s.nodeMgr)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		render.JSON(w, r, map[string]any{"error": err.Error()})
		return
	}

	// 获取并筛选节点
	nodes, err := s.selectAvailableNodes(params)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		render.JSON(w, r, map[string]any{"error": err.Error()})
		return
	}

	// 批量选择节点
	selected := s.selectNodes(nodes, params.Count, params)
	if len(selected) == 0 {
		w.WriteHeader(http.StatusInternalServerError)
		render.JSON(w, r, map[string]any{"error": "选择节点失败"})
		return
	}

	// 转换并返回响应
	nodesResponse := make([]AvailableNodeResponse, len(selected))
	for i, node := range selected {
		nodesResponse[i] = s.snapshotToResponse(node)
	}

	render.JSON(w, r, AvailableNodesResponse{Nodes: nodesResponse})
}
