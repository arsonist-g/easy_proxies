package monitor

import "math/rand"

// weighted 评分的冷启动阈值：可用率统计次数低于此值时用中性分，避免新节点被饿死或过度信任。
const weightedMinAttempts int64 = 5

// WeightedScore 计算节点在 weighted 策略下的综合得分（越高越优）。
//   latencyMs     最近探测延迟（毫秒）；<=0 视为未探测，给中性低分（倾向已探测节点）。
//   availRate     可用率（0-1）。
//   totalAttempts 可用率统计总次数；< weightedMinAttempts 视为冷启动，可用率给中性 0.5。
//   wLat, wAvail  延迟/可用率权重（任意正数，内部归一化为比例）。
//
// 设计：延迟与可用率解耦（ADR-0005）——延迟是性能维度，可用率是连通性维度，
// 两者归一化后按权重加权求和。延迟分 1/(1+lat/1000)：0ms→1.0、1000ms→0.5、∞→0。
func WeightedScore(latencyMs int64, availRate float64, totalAttempts int64, wLat, wAvail float64) float64 {
	const (
		unprobedLatencyScore = 0.3 // 未探测延迟：中性偏低，优先已探测节点但不至于完全排除
		coldStartAvail       = 0.5 // 冷启动可用率：中性，不偏信也不饿死
	)

	if wLat < 0 {
		wLat = 0
	}
	if wAvail < 0 {
		wAvail = 0
	}
	sum := wLat + wAvail
	if sum <= 0 { // 兜底：未提供有效权重时退化为等权
		wLat, wAvail, sum = 1, 1, 2
	}
	nLat := wLat / sum
	nAvail := wAvail / sum

	var latScore float64
	if latencyMs > 0 {
		latScore = 1.0 / (1.0 + float64(latencyMs)/1000.0)
	} else {
		latScore = unprobedLatencyScore
	}

	var avScore float64
	if totalAttempts < weightedMinAttempts {
		avScore = coldStartAvail
	} else {
		avScore = availRate
	}

	return nLat*latScore + nAvail*avScore
}

// PickWeighted 按得分加权随机返回候选索引。得分非负参与抽取；全为 0/负时回退均匀随机。
// r 由调用方加锁保护（与 random 策略共用同一 rng）。
func PickWeighted(scores []float64, r *rand.Rand) int {
	n := len(scores)
	if n <= 1 {
		return 0
	}
	var sum float64
	allZero := true
	clamped := make([]float64, n)
	for i, s := range scores {
		if s < 0 {
			s = 0
		}
		clamped[i] = s
		sum += s
		if s > 0 {
			allZero = false
		}
	}
	if allZero || sum <= 0 {
		return r.Intn(n)
	}
	tgt := r.Float64() * sum
	var acc float64
	for i, s := range clamped {
		acc += s
		if tgt <= acc {
			return i
		}
	}
	return n - 1
}
