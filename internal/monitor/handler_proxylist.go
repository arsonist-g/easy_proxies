package monitor

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"easy_proxies/internal/store"
)

// proxyListItem 单条代理（api-contract §3.2）。
type proxyListItem struct {
	Name             string  `json:"name"`
	CountryCode      string  `json:"country_code,omitempty"`
	CountryName      string  `json:"country_name,omitempty"`
	Host             string  `json:"host"`
	Port             uint16  `json:"port"`
	Username         string  `json:"username,omitempty"`
	Password         string  `json:"password,omitempty"`
	Protocol         string  `json:"protocol"`
	LatencyMs        int64   `json:"latency_ms,omitempty"`
	AvailabilityRate float64 `json:"availability_rate,omitempty"`
	ExitIP           string  `json:"exit_ip,omitempty"`
}

// handleProxyList GET /sub/{token}：JSON 代理列表（订阅 token 已由 subTokenAuth 校验）。
// 过滤由 token.filters 决定；默认排除 duplicate_of 非空与 available=false 的节点。
func (s *Server) handleProxyList(w http.ResponseWriter, r *http.Request) {
	raw := subTokenFromContext(r)
	tok, ok := raw.(*store.SubscribeToken)
	if !ok || tok == nil {
		respondAPIError(w, r, errUnauthorized)
		return
	}

	externalIP, _, _ := s.getSettings()
	snapshots := applyTokenFilters(s.mgr.Snapshot(), tok.Filters)

	items := make([]proxyListItem, 0, len(snapshots))
	for _, sn := range snapshots {
		// 默认排除：重复节点 / 已检查但不可用
		if sn.DuplicateOf != "" {
			continue
		}
		if sn.InitialCheckDone && !sn.Available {
			continue
		}
		host := externalIP
		if host == "" {
			host = hostFromListen(sn.ListenAddress)
		}
		items = append(items, proxyListItem{
			Name:             sn.Name,
			CountryCode:      sn.CountryCode,
			CountryName:      sn.CountryName,
			Host:             host,
			Port:             sn.Port,
			Username:         sn.Username,
			Password:         sn.Password,
			Protocol:         "http",
			LatencyMs:        sn.LastLatencyMs,
			AvailabilityRate: sn.AvailabilityRate,
			ExitIP:           sn.ExitIP,
		})
	}

	writeJSON(w, map[string]any{
		"updated_at": time.Now().UTC(),
		"count":      len(items),
		"proxies":    items,
	})
}

// applyTokenFilters 应用订阅 token 绑定的过滤视图（country_codes/protocols/name_regex）。
// 与 /nodes 的 URL 过滤分离：/sub 不暴露过滤逻辑，全部由 token 决定。
func applyTokenFilters(nodes []Snapshot, f store.Filters) []Snapshot {
	var re *regexp.Regexp
	if f.NameRegex != "" {
		if compiled, err := regexp.Compile(f.NameRegex); err == nil {
			re = compiled
		}
	}
	out := make([]Snapshot, 0, len(nodes))
	for _, n := range nodes {
		if len(f.CountryCodes) > 0 && !containsFold(f.CountryCodes, n.CountryCode) {
			continue
		}
		if len(f.Protocols) > 0 && !containsFold(f.Protocols, n.Mode) {
			continue
		}
		if re != nil && !re.MatchString(n.Name) {
			continue
		}
		out = append(out, n)
	}
	return out
}

// containsFold 大小写不敏感的集合包含判断。
func containsFold(set []string, v string) bool {
	for _, s := range set {
		if strings.EqualFold(s, v) {
			return true
		}
	}
	return false
}

// hostFromListen 从 listen 地址（host:port）取 host，失败返回原串。
func hostFromListen(addr string) string {
	if i := strings.LastIndex(addr, ":"); i > 0 {
		return addr[:i]
	}
	return addr
}
