package monitor

import (
	"math/rand"
	"strconv"
	"time"
)

// WeightedSelector 实现基于权重的随机选择算法
type WeightedSelector struct {
	rng *rand.Rand
}

// NewWeightedSelector 创建新的加权选择器
func NewWeightedSelector() *WeightedSelector {
	return &WeightedSelector{
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// WeightParams 权重参数
type WeightParams struct {
	LatencyFactor float64 // 延迟权重系数 (0.0-1.0)
	SuccessFactor float64 // 成功率权重系数 (0.0-1.0)
}

// ParseWeightParams 解析权重参数
// weightMode: 预设模式（latency_first/stable_first/balanced）
// latencyWeightStr: 手动指定的延迟权重
// successRateWeightStr: 手动指定的成功率权重
func ParseWeightParams(weightMode, latencyWeightStr, successRateWeightStr string) (WeightParams, error) {
	params := WeightParams{
		LatencyFactor: 0.6, // 默认：延迟权重 0.6
		SuccessFactor: 0.4, // 默认：成功率权重 0.4
	}

	// 优先级 1：使用预设模式
	if weightMode != "" {
		switch weightMode {
		case "latency_first":
			params.LatencyFactor = 0.8
			params.SuccessFactor = 0.2
		case "stable_first":
			params.LatencyFactor = 0.3
			params.SuccessFactor = 0.7
		case "balanced":
			params.LatencyFactor = 0.6
			params.SuccessFactor = 0.4
		default:
			// 无效模式，使用默认值
		}
		return params, nil
	}

	// 优先级 2：手动指定的权重参数
	if latencyWeightStr != "" || successRateWeightStr != "" {
		var latencyWeight, successRateWeight float64
		var err error

		if latencyWeightStr != "" {
			latencyWeight, err = strconv.ParseFloat(latencyWeightStr, 64)
			if err != nil {
				return params, err
			}
		}

		if successRateWeightStr != "" {
			successRateWeight, err = strconv.ParseFloat(successRateWeightStr, 64)
			if err != nil {
				return params, err
			}
		}

		// 规范化权重：确保总和为 1.0
		total := latencyWeight + successRateWeight
		if total > 0 {
			params.LatencyFactor = latencyWeight / total
			params.SuccessFactor = successRateWeight / total
		}
	}

	return params, nil
}

// Select 根据权重选择一个节点
// nodes: 候选节点列表
// params: 权重参数
func (s *WeightedSelector) Select(nodes []Snapshot, params WeightParams) *Snapshot {
	if len(nodes) == 0 {
		return nil
	}
	if len(nodes) == 1 {
		return &nodes[0]
	}

	// 计算每个节点的权重
	weights := make([]float64, len(nodes))
	for i, node := range nodes {
		weights[i] = s.calculateWeight(node, params)
	}

	// 计算总权重
	totalWeight := 0.0
	for _, w := range weights {
		totalWeight += w
	}

	if totalWeight <= 0 {
		// 所有权重都为 0，随机选择
		return &nodes[s.rng.Intn(len(nodes))]
	}

	// 基于权重的随机选择
	r := s.rng.Float64() * totalWeight
	cumulative := 0.0
	for i, w := range weights {
		cumulative += w
		if r <= cumulative {
			return &nodes[i]
		}
	}

	// 浮点数精度问题，返回最后一个
	return &nodes[len(nodes)-1]
}

// SelectBest 选择权重最高的节点
// nodes: 候选节点列表
// params: 权重参数
func (s *WeightedSelector) SelectBest(nodes []Snapshot, params WeightParams) *Snapshot {
	if len(nodes) == 0 {
		return nil
	}
	if len(nodes) == 1 {
		return &nodes[0]
	}

	// 计算每个节点的权重
	weights := make([]float64, len(nodes))
	for i, node := range nodes {
		weights[i] = s.calculateWeight(node, params)
	}

	// 找出权重最高的节点索引
	bestIndex := 0
	bestWeight := weights[0]
	for i := 1; i < len(nodes); i++ {
		if weights[i] > bestWeight {
			bestWeight = weights[i]
			bestIndex = i
		}
	}

	return &nodes[bestIndex]
}

// calculateWeight 计算节点的综合权重
// 延迟归一化：1000 / (latency_ms + 1)，延迟越低权重越高
// 成功率估算：如果节点可用且未失败，给予较高得分；否则给予较低得分
func (s *WeightedSelector) calculateWeight(node Snapshot, params WeightParams) float64 {
	// 计算延迟权重（延迟越低权重越高）
	var latencyScore float64
	if node.LastLatencyMs < 0 {
		// 未测试延迟，给予中等得分
		latencyScore = 50.0
	} else {
		// 使用公式：1000 / (latency_ms + 1)
		// 延迟 10ms → ~91，延迟 100ms → ~9.9，延迟 500ms → ~2
		latencyScore = 1000.0 / (float64(node.LastLatencyMs) + 1.0)
	}

	// 估算成功率得分
	var successScore float64
	if !node.InitialCheckDone {
		// 未完成初始检查，给予中等得分
		successScore = 50.0
	} else if !node.Available {
		// 已检查但不可用，给予极低得分
		successScore = 1.0
	} else if node.FailureCount > 0 {
		// 可用但有失败记录，根据失败次数降低得分
		// 失败越多，得分越低（失败 3 次得分约 25）
		successScore = 100.0 / (float64(node.FailureCount) + 1.0)
	} else {
		// 可用且无失败记录，给予最高得分
		successScore = 100.0
	}

	// 综合权重
	weight := latencyScore*params.LatencyFactor + successScore*params.SuccessFactor
	return weight
}

// SelectMultiple 批量选择多个不重复的节点
func (s *WeightedSelector) SelectMultiple(nodes []Snapshot, count int, params WeightParams) []Snapshot {
	if len(nodes) == 0 {
		return nil
	}

	// 请求数量超过可用节点数量，返回全部节点
	if count >= len(nodes) {
		result := make([]Snapshot, len(nodes))
		copy(result, nodes)
		return result
	}

	selected := make([]Snapshot, 0, count)
	remaining := make([]Snapshot, len(nodes))
	copy(remaining, nodes)

	for i := 0; i < count && len(remaining) > 0; i++ {
		// 从剩余节点中选择一个
		node := s.Select(remaining, params)
		if node == nil {
			break
		}

		// 添加到结果
		selected = append(selected, *node)

		// 从剩余节点中移除已选择的节点
		for j, n := range remaining {
			if n.Tag == node.Tag {
				remaining = append(remaining[:j], remaining[j+1:]...)
				break
			}
		}
	}

	return selected
}

// SelectMultipleBest 批量选择权重最高的节点
func (s *WeightedSelector) SelectMultipleBest(nodes []Snapshot, count int, params WeightParams) []Snapshot {
	if len(nodes) == 0 {
		return nil
	}

	// 请求数量超过可用节点数量，返回全部节点
	if count >= len(nodes) {
		result := make([]Snapshot, len(nodes))
		copy(result, nodes)
		return result
	}

	// 计算每个节点的权重
	weights := make([]float64, len(nodes))
	for i, node := range nodes {
		weights[i] = s.calculateWeight(node, params)
	}

	// 创建带权重的节点信息
	type weightNode struct {
		node   Snapshot
		weight float64
	}
	weightedNodes := make([]weightNode, len(nodes))
	for i, node := range nodes {
		weightedNodes[i] = weightNode{node: node, weight: weights[i]}
	}

	// 按权重降序排序
	for i := 0; i < len(weightedNodes); i++ {
		for j := i + 1; j < len(weightedNodes); j++ {
			if weightedNodes[j].weight > weightedNodes[i].weight {
				weightedNodes[i], weightedNodes[j] = weightedNodes[j], weightedNodes[i]
			}
		}
	}

	// 选择前 count 个节点
	result := make([]Snapshot, count)
	for i := 0; i < count; i++ {
		result[i] = weightedNodes[i].node
	}

	return result
}
