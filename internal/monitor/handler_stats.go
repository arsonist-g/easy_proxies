package monitor

import (
	"net/http"
	"sort"

	"easy_proxies/internal/alert"
	"easy_proxies/internal/countryprobe"
	"easy_proxies/internal/store"
)

// countryCount 国家分布条目。
type countryCount struct {
	CountryCode string `json:"country_code"`
	CountryName string `json:"country_name"`
	Count       int    `json:"count"`
}

// topCalledNode 被调用次数 Top 节点（dashboard 排名）。
type topCalledNode struct {
	StableID    string `json:"stable_id"`
	Name        string `json:"name"`
	CallTotal   int64  `json:"call_total"`
	CountryCode string `json:"country_code,omitempty"`
}

// handleStats GET /api/v1/stats：总览统计 + dashboard 聚合（api-contract §3.7 delta）。
// 全部基于内存 Snapshot + bbolt 订阅状态实时聚合，无额外存储。
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	snapshots := s.mgr.Snapshot()
	total := len(snapshots)

	availDist := map[string]int{"high": 0, "medium": 0, "low": 0}
	latDist := map[string]int{"lt100": 0, "100_300": 0, "300_500": 0, "gt500": 0, "unknown": 0}
	countryMap := map[string]*countryCount{}
	topNodes := make([]topCalledNode, 0, total)
	available, duplicate := 0, 0

	for _, sn := range snapshots {
		if sn.InitialCheckDone && sn.Available {
			available++
		}
		if sn.DuplicateOf != "" {
			duplicate++
		}

		// 可用率分布：high >=0.9 / medium 0.5~0.9 / low <0.5
		switch {
		case sn.AvailabilityRate >= 0.9:
			availDist["high"]++
		case sn.AvailabilityRate >= 0.5:
			availDist["medium"]++
		default:
			availDist["low"]++
		}

		// 延迟分布：-1 视为未测
		switch {
		case sn.LastLatencyMs < 0:
			latDist["unknown"]++
		case sn.LastLatencyMs < 100:
			latDist["lt100"]++
		case sn.LastLatencyMs < 300:
			latDist["100_300"]++
		case sn.LastLatencyMs < 500:
			latDist["300_500"]++
		default:
			latDist["gt500"]++
		}

		// 国家分布
		if sn.CountryCode != "" {
			cc := countryMap[sn.CountryCode]
			if cc == nil {
				name := sn.CountryName
				if name == "" {
					name = countryprobe.CountryName(sn.CountryCode)
				}
				cc = &countryCount{CountryCode: sn.CountryCode, CountryName: name}
				countryMap[sn.CountryCode] = cc
			}
			cc.Count++
		}

		topNodes = append(topNodes, topCalledNode{
			StableID: sn.StableID, Name: sn.Name, CallTotal: sn.CallTotal, CountryCode: sn.CountryCode,
		})
	}

	// top_called_nodes：按 call_total 降序取前 10
	sort.Slice(topNodes, func(i, j int) bool { return topNodes[i].CallTotal > topNodes[j].CallTotal })
	if len(topNodes) > 10 {
		topNodes = topNodes[:10]
	}

	// country_distribution：按 count 降序、country_code 升序兜底
	countryDist := make([]countryCount, 0, len(countryMap))
	for _, cc := range countryMap {
		countryDist = append(countryDist, *cc)
	}
	sort.Slice(countryDist, func(i, j int) bool {
		if countryDist[i].Count != countryDist[j].Count {
			return countryDist[i].Count > countryDist[j].Count
		}
		return countryDist[i].CountryCode < countryDist[j].CountryCode
	})

	// 订阅健康 + 活跃订阅数
	subHealth := map[string]any{"total": 0, "failed": 0, "failed_names": []string{}}
	activeSubs := 0
	if s.store != nil {
		if subs, err := s.store.ListSubscriptions(); err == nil {
			activeSubs = len(subs)
			failedNames := []string{}
			for _, sub := range subs {
				if sub.LastRefreshStatus == store.SubStatusFailed {
					failedNames = append(failedNames, sub.Name)
				}
			}
			subHealth = map[string]any{
				"total":        len(subs),
				"failed":       len(failedNames),
				"failed_names": failedNames,
			}
		}
	}

	// 告警摘要：与 /alerts 同口径（节点空密码 + 虚拟池认证），保证 dashboard 告警数与告警页一致。
	// alert_enabled 关闭时不扫描（与 /alerts 行为一致）。
	alertEnabled := true
	s.cfgMu.RLock()
	if s.cfgSrc != nil && s.cfgSrc.AlertEnabled != nil {
		alertEnabled = *s.cfgSrc.AlertEnabled
	}
	s.cfgMu.RUnlock()
	alertTotal, alertCritical, emptyPwdCount := 0, 0, 0
	if alertEnabled {
		checker := alert.New()
		nodes := make([]alert.NodeInput, 0, total)
		for _, sn := range snapshots {
			nodes = append(nodes, alert.NodeInput{StableID: sn.StableID, Name: sn.Name, Password: sn.Password})
		}
		var pools []alert.PoolInput
		if s.vpMgr != nil {
			for _, st := range s.vpMgr.Status() {
				pools = append(pools, alert.PoolInput{Name: st.Name, Username: st.Username, Password: st.Password})
			}
		}
		for _, it := range checker.Check(nodes, pools) {
			alertTotal++
			if it.Level == "critical" {
				alertCritical++
			}
			if it.Code == alert.CodeEmptyNodePassword {
				emptyPwdCount++
			}
		}
	}

	writeJSON(w, map[string]any{
		"total_nodes":               total,
		"available_nodes":           available,
		"duplicate_nodes":           duplicate,
		"active_subscriptions":      activeSubs,
		"availability_distribution": availDist,
		"country_distribution":      countryDist,
		"top_called_nodes":          topNodes,
		"subscription_health":       subHealth,
		"alert_summary": map[string]int{
			"alert_count":          alertTotal,    // 告警总数（同 /alerts 口径：节点空密码 + 虚拟池认证）
			"alert_critical":       alertCritical, // 其中严重数
			"empty_password_count": emptyPwdCount, // 节点空密码细分（兼容保留）
		},
		"latency_distribution":      latDist,
	})
}
