package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"easy_proxies/internal/config"
)

// writeJSONStatus 写 JSON 响应并指定状态码（默认 writeJSON 固定 200，这里用于 201/202 等）。
func writeJSONStatus(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(payload)
}

// urlParamUint64 解析路径参数为 uint64，失败返回 0。
func urlParamUint64(r *http.Request, name string) uint64 {
	v, _ := strconv.ParseUint(chi.URLParam(r, name), 10, 64)
	return v
}

// filterNodes 按 query 参数过滤节点：country/protocol/available/duplicate/name_regex。
// duplicate 默认隐藏（duplicate_of 非空），duplicate=true 显式包含。
func filterNodes(nodes []Snapshot, q url.Values) []Snapshot {
	country := strings.TrimSpace(q.Get("country"))
	protocol := strings.TrimSpace(q.Get("protocol"))
	available := q.Get("available")
	duplicate := q.Get("duplicate")
	nameRegex := strings.TrimSpace(q.Get("name_regex"))

	showDup := duplicate == "true" || duplicate == "1"

	var re *regexp.Regexp
	if nameRegex != "" {
		var err error
		if re, err = regexp.Compile(nameRegex); err != nil {
			return nodes // 正则无效：不过滤
		}
	}

	out := nodes[:0]
	for _, n := range nodes {
		if country != "" && !strings.EqualFold(country, n.CountryCode) {
			continue
		}
		if protocol != "" && !strings.EqualFold(protocol, n.Mode) {
			continue
		}
		if (available == "true" || available == "1") && !(n.InitialCheckDone && n.Available) {
			continue
		}
		if !showDup && n.DuplicateOf != "" {
			continue
		}
		if re != nil {
			if !re.MatchString(n.Name) {
				continue
			}
		}
		out = append(out, n)
	}
	return out
}

// sortNodes 按 query 参数 sort 排序节点快照（原地）。格式：`field`（升序）或 `-field`（降序）。
// 支持 name / country(country_code) / exit_ip / protocol(mode) / port / latency(last_latency_ms) / availability(availability_rate)。
// 无参默认 latency 升序（与 Snapshot 默认一致）。延迟未知(<0)始终沉底、不受方向影响；同值按 name 稳定。
// 未知字段不排序（返回原序）。由 handleNodesList 在 filterNodes 后、分页前调用。
func sortNodes(nodes []Snapshot, q url.Values) []Snapshot {
	raw := strings.TrimSpace(q.Get("sort"))
	if raw == "" {
		raw = "latency"
	}
	desc := false
	field := raw
	if strings.HasPrefix(raw, "-") {
		desc = true
		field = raw[1:]
	}

	// 延迟单独处理：未知沉底，已知按方向，同值按 name。
	if field == "latency" {
		sort.SliceStable(nodes, func(i, j int) bool {
			a, b := nodes[i].LastLatencyMs, nodes[j].LastLatencyMs
			ai, bi := a < 0, b < 0
			if ai && bi {
				return nodes[i].Name < nodes[j].Name
			}
			if ai {
				return false // i 未知沉底
			}
			if bi {
				return true // j 未知沉底
			}
			if a == b {
				return nodes[i].Name < nodes[j].Name
			}
			if desc {
				return a > b
			}
			return a < b
		})
		return nodes
	}

	var cmp func(a, b Snapshot) int
	switch field {
	case "name":
		cmp = func(a, b Snapshot) int { return strings.Compare(a.Name, b.Name) }
	case "country":
		cmp = func(a, b Snapshot) int { return strings.Compare(a.CountryCode, b.CountryCode) }
	case "exit_ip":
		cmp = func(a, b Snapshot) int { return strings.Compare(a.ExitIP, b.ExitIP) }
	case "protocol":
		cmp = func(a, b Snapshot) int { return strings.Compare(a.Mode, b.Mode) }
	case "port":
		cmp = func(a, b Snapshot) int {
			if a.Port < b.Port {
				return -1
			} else if a.Port > b.Port {
				return 1
			}
			return 0
		}
	case "availability":
		cmp = func(a, b Snapshot) int {
			if a.AvailabilityRate < b.AvailabilityRate {
				return -1
			} else if a.AvailabilityRate > b.AvailabilityRate {
				return 1
			}
			return 0
		}
	default:
		return nodes // 未知字段：不排序
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		c := cmp(nodes[i], nodes[j])
		if desc {
			return c > 0
		}
		return c < 0
	})
	return nodes
}

// respondNodeErr 把 NodeManager 错误映射为统一 API 错误体。
func (s *Server) respondNodeErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNodeNotFound):
		respondAPIError(w, r, errNotFoundAPI)
	case errors.Is(err, ErrNodeConflict):
		respondAPIError(w, r, errConflictAPI)
	case errors.Is(err, ErrInvalidNode):
		// 透出具体校验原因（如 unsupported scheme / 缺 uuid），便于用户定位 URI 问题。
		respondError(w, r, http.StatusUnprocessableEntity, CodeValidationError, err.Error())
	default:
		respondError(w, r, http.StatusInternalServerError, CodeInternalError, err.Error())
	}
}

// handleNodeGet GET /api/v1/nodes/{stable_id}
func (s *Server) handleNodeGet(w http.ResponseWriter, r *http.Request) {
	stableID := chi.URLParam(r, "stable_id")
	snap, ok := s.mgr.SnapshotByStableID(stableID)
	if !ok {
		respondAPIError(w, r, errNotFoundAPI)
		return
	}
	writeJSON(w, snap)
}

// handleNodeCreate POST /api/v1/nodes
func (s *Server) handleNodeCreate(w http.ResponseWriter, r *http.Request) {
	if s.nodeMgr == nil {
		respondAPIError(w, r, errNotImplemented)
		return
	}
	var node config.NodeConfig
	if err := json.NewDecoder(r.Body).Decode(&node); err != nil {
		respondAPIError(w, r, errBadRequest)
		return
	}
	created, err := s.nodeMgr.CreateNode(r.Context(), node)
	if err != nil {
		s.respondNodeErr(w, r, err)
		return
	}
	writeJSONStatus(w, http.StatusCreated, created)
}

// handleNodeUpdate PATCH /api/v1/nodes/{stable_id}
func (s *Server) handleNodeUpdate(w http.ResponseWriter, r *http.Request) {
	if s.nodeMgr == nil {
		respondAPIError(w, r, errNotImplemented)
		return
	}
	stableID := chi.URLParam(r, "stable_id")
	var node config.NodeConfig
	if err := json.NewDecoder(r.Body).Decode(&node); err != nil {
		respondAPIError(w, r, errBadRequest)
		return
	}
	updated, err := s.nodeMgr.UpdateNode(r.Context(), stableID, node)
	if err != nil {
		s.respondNodeErr(w, r, err)
		return
	}
	writeJSON(w, updated)
}

// handleNodeDelete DELETE /api/v1/nodes/{stable_id}
func (s *Server) handleNodeDelete(w http.ResponseWriter, r *http.Request) {
	if s.nodeMgr == nil {
		respondAPIError(w, r, errNotImplemented)
		return
	}
	stableID := chi.URLParam(r, "stable_id")
	if err := s.nodeMgr.DeleteNode(r.Context(), stableID); err != nil {
		s.respondNodeErr(w, r, err)
		return
	}
	writeJSON(w, map[string]any{"message": "节点已删除", "stable_id": stableID})
}

// handleNodeProbe POST /api/v1/nodes/{stable_id}/probe：同步探测单节点。
func (s *Server) handleNodeProbe(w http.ResponseWriter, r *http.Request) {
	stableID := chi.URLParam(r, "stable_id")
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	latency, err := s.mgr.ProbeByStableID(ctx, stableID)
	if err != nil {
		respondAPIError(w, r, errNotFoundAPI)
		return
	}
	// 回带完整快照供前端就地刷新整行（延迟/可用率/状态/国家/ASN），
	// 避免 loadNodes() 全量重建导致探测后节点跳位。
	resp := map[string]any{
		"stable_id":  stableID,
		"latency_ms": latency.Milliseconds(),
		"success":    true,
	}
	if snap, ok := s.mgr.SnapshotByStableID(stableID); ok {
		resp["node"] = snap
	}
	writeJSON(w, resp)
}

// handleProbeAll POST /api/v1/probe/all：异步触发全节点探测，立即返回 202。
func (s *Server) handleProbeAll(w http.ResponseWriter, r *http.Request) {
	go s.mgr.ProbeAll(15 * time.Second)
	writeJSONStatus(w, http.StatusAccepted, map[string]any{
		"message": "全节点探测已触发",
		"status":  "running",
	})
}
