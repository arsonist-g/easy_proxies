package monitor

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"easy_proxies/internal/config"
)

// poolView 虚拟池视图：配置 + 运行时（节点数/运行状态）。
type poolView struct {
	config.VirtualPoolConfig
	NodeCount int  `json:"node_count"`
	Running   bool `json:"running"`
}

// respondPoolErr 把虚拟池 CRUD 错误映射：
//   - 配置校验失败（*config.ValidationError）→ 422，保留具体校验信息
//   - 名称/端口冲突 → 409
//   - 其余 → 500
func respondPoolErr(w http.ResponseWriter, r *http.Request, err error) {
	var ve *config.ValidationError
	if errors.As(err, &ve) {
		respondError(w, r, http.StatusUnprocessableEntity, CodeValidationError, ve.Msg)
		return
	}
	msg := err.Error()
	if strings.Contains(msg, "已存在") || strings.Contains(msg, "占用") {
		respondAPIError(w, r, errConflictAPI)
		return
	}
	respondError(w, r, http.StatusInternalServerError, CodeInternalError, msg)
}

// poolViewByID 按 ID 构建虚拟池视图。
func (s *Server) poolViewByID(id uint64) (poolView, bool) {
	cfgs := s.vpMgr.ListVirtualPools()
	var cfg config.VirtualPoolConfig
	found := false
	for _, c := range cfgs {
		if c.ID == id {
			cfg = c
			found = true
			break
		}
	}
	if !found {
		return poolView{}, false
	}
	for _, st := range s.vpMgr.Status() {
		if st.Name == cfg.Name {
			return poolView{VirtualPoolConfig: cfg, NodeCount: st.NodeCount, Running: st.Running}, true
		}
	}
	return poolView{VirtualPoolConfig: cfg}, true
}

// handleVirtualPoolsList GET /api/v1/virtual-pools
func (s *Server) handleVirtualPoolsList(w http.ResponseWriter, r *http.Request) {
	if s.vpMgr == nil {
		writeJSON(w, []poolView{})
		return
	}
	cfgs := s.vpMgr.ListVirtualPools()
	statusMap := map[string]VirtualPoolStatus{}
	for _, st := range s.vpMgr.Status() {
		statusMap[st.Name] = st
	}
	out := make([]poolView, 0, len(cfgs))
	for _, c := range cfgs {
		st := statusMap[c.Name]
		out = append(out, poolView{VirtualPoolConfig: c, NodeCount: st.NodeCount, Running: st.Running})
	}
	writeJSON(w, out)
}

// handleVirtualPoolCreate POST /api/v1/virtual-pools
func (s *Server) handleVirtualPoolCreate(w http.ResponseWriter, r *http.Request) {
	if s.vpMgr == nil {
		respondAPIError(w, r, errNotImplemented)
		return
	}
	var pcfg config.VirtualPoolConfig
	if err := json.NewDecoder(r.Body).Decode(&pcfg); err != nil {
		respondAPIError(w, r, errBadRequest)
		return
	}
	created, err := s.vpMgr.CreateVirtualPool(pcfg)
	if err != nil {
		respondPoolErr(w, r, err)
		return
	}
	writeJSONStatus(w, http.StatusCreated, created)
}

// handleVirtualPoolGet GET /api/v1/virtual-pools/{id}
func (s *Server) handleVirtualPoolGet(w http.ResponseWriter, r *http.Request) {
	if s.vpMgr == nil {
		respondAPIError(w, r, errNotImplemented)
		return
	}
	view, ok := s.poolViewByID(urlParamUint64(r, "id"))
	if !ok {
		respondAPIError(w, r, errNotFoundAPI)
		return
	}
	writeJSON(w, view)
}

// handleVirtualPoolUpdate PATCH /api/v1/virtual-pools/{id}（部分更新：仅覆盖请求中提供的字段）
func (s *Server) handleVirtualPoolUpdate(w http.ResponseWriter, r *http.Request) {
	if s.vpMgr == nil {
		respondAPIError(w, r, errNotImplemented)
		return
	}
	id := urlParamUint64(r, "id")
	// 取现有配置，在其上解码——json.Unmarshal 不覆盖未提供的字段，实现 PATCH 合并语义
	cfgs := s.vpMgr.ListVirtualPools()
	var existing *config.VirtualPoolConfig
	for i := range cfgs {
		if cfgs[i].ID == id {
			existing = &cfgs[i]
			break
		}
	}
	if existing == nil {
		respondAPIError(w, r, errNotFoundAPI)
		return
	}
	if err := json.NewDecoder(r.Body).Decode(existing); err != nil {
		respondAPIError(w, r, errBadRequest)
		return
	}
	updated, err := s.vpMgr.UpdateVirtualPool(id, *existing)
	if err != nil {
		respondPoolErr(w, r, err)
		return
	}
	writeJSON(w, updated)
}

// handleVirtualPoolDelete DELETE /api/v1/virtual-pools/{id}
func (s *Server) handleVirtualPoolDelete(w http.ResponseWriter, r *http.Request) {
	if s.vpMgr == nil {
		respondAPIError(w, r, errNotImplemented)
		return
	}
	if err := s.vpMgr.DeleteVirtualPool(urlParamUint64(r, "id")); err != nil {
		respondPoolErr(w, r, err)
		return
	}
	writeJSON(w, map[string]any{"message": "虚拟池已删除"})
}

// handleVirtualPoolNodes GET /api/v1/virtual-pools/{id}/nodes
func (s *Server) handleVirtualPoolNodes(w http.ResponseWriter, r *http.Request) {
	if s.vpMgr == nil {
		respondAPIError(w, r, errNotImplemented)
		return
	}
	id := urlParamUint64(r, "id")
	cfgs := s.vpMgr.ListVirtualPools()
	var name string
	for _, c := range cfgs {
		if c.ID == id {
			name = c.Name
			break
		}
	}
	if name == "" {
		respondAPIError(w, r, errNotFoundAPI)
		return
	}
	pool := s.vpMgr.GetPool(name)
	if pool == nil {
		respondAPIError(w, r, errNotFoundAPI)
		return
	}
	writeJSON(w, pool.GetMatchingNodes())
}

// handleVirtualPoolNextPort GET /api/v1/virtual-pools/next-port：预览下一个可用端口。
func (s *Server) handleVirtualPoolNextPort(w http.ResponseWriter, r *http.Request) {
	if s.vpMgr == nil {
		respondAPIError(w, r, errNotImplemented)
		return
	}
	port, err := s.vpMgr.NextAvailablePort()
	if err != nil {
		respondPoolErr(w, r, err)
		return
	}
	writeJSON(w, map[string]any{"port": port})
}
