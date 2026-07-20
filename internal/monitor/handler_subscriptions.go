package monitor

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	"easy_proxies/internal/store"
)

// respondStoreErr 把 bbolt store 错误映射为统一 API 错误体。
func respondStoreErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		respondAPIError(w, r, errNotFoundAPI)
	case errors.Is(err, store.ErrConflict):
		respondAPIError(w, r, errConflictAPI)
	default:
		respondError(w, r, http.StatusInternalServerError, CodeInternalError, err.Error())
	}
}

// handleSubscriptionsList GET /api/v1/subscriptions
func (s *Server) handleSubscriptionsList(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		respondAPIError(w, r, errNotImplemented)
		return
	}
	subs, err := s.store.ListSubscriptions()
	if err != nil {
		respondStoreErr(w, r, err)
		return
	}
	sort.Slice(subs, func(i, j int) bool { return subs[i].ID < subs[j].ID })
	page, pageSize, offset := parsePage(r)
	total := len(subs)
	if offset > total {
		offset = total
	}
	end := offset + pageSize
	if end > total {
		end = total
	}
	writeJSON(w, newPageResponse(subs[offset:end], total, page, pageSize))
}

// handleSubscriptionCreate POST /api/v1/subscriptions
func (s *Server) handleSubscriptionCreate(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		respondAPIError(w, r, errNotImplemented)
		return
	}
	var req struct {
		Name string `json:"name"`
		URL  string `json:"url"`
		Type string `json:"type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondAPIError(w, r, errBadRequest)
		return
	}
	if req.URL == "" {
		respondError(w, r, http.StatusBadRequest, CodeValidationError, "url 不能为空")
		return
	}
	// yaml-first：先落 config.yaml（权威源），再写 bbolt。bbolt 失败/崩溃时 yaml 领先，
	// 下次启动 SyncSubscriptions 会按 yaml 补建 bbolt（崩溃安全，无数据丢失）。
	if s.cfgSrc != nil {
		if err := s.cfgSrc.AddSubscription(req.Name, req.URL); err != nil {
			respondError(w, r, http.StatusInternalServerError, CodeInternalError, err.Error())
			return
		}
	}
	sub, err := s.store.CreateSubscription(req.Name, req.URL, req.Type)
	if err != nil {
		respondStoreErr(w, r, err)
		return
	}
	// 异步触发刷新，让新订阅节点尽快生效（不阻塞 201 响应）。
	// 刷新会重启 sing-box 内核、短暂中断现有连接（订阅刷新固有特性）。
	if s.subRefresher != nil {
		go func() { _ = s.subRefresher.RefreshNow() }()
	}
	writeJSONStatus(w, http.StatusCreated, sub)
}

// handleSubscriptionGet GET /api/v1/subscriptions/{id}
func (s *Server) handleSubscriptionGet(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		respondAPIError(w, r, errNotImplemented)
		return
	}
	sub, err := s.store.GetSubscription(urlParamUint64(r, "id"))
	if err != nil {
		respondStoreErr(w, r, err)
		return
	}
	if sub == nil {
		respondAPIError(w, r, errNotFoundAPI)
		return
	}
	writeJSON(w, sub)
}

// handleSubscriptionUpdate PATCH /api/v1/subscriptions/{id}
func (s *Server) handleSubscriptionUpdate(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		respondAPIError(w, r, errNotImplemented)
		return
	}
	id := urlParamUint64(r, "id")
	sub, err := s.store.GetSubscription(id)
	if err != nil {
		respondStoreErr(w, r, err)
		return
	}
	if sub == nil {
		respondAPIError(w, r, errNotFoundAPI)
		return
	}
	var req struct {
		Name string `json:"name"`
		URL  string `json:"url"`
		Type string `json:"type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondAPIError(w, r, errBadRequest)
		return
	}
	oldURL := sub.URL // 记录旧 url，供 yaml 条目定位改写
	if req.Name != "" {
		sub.Name = req.Name
	}
	if req.URL != "" {
		sub.URL = req.URL
	}
	if req.Type != "" {
		sub.Type = req.Type
	}
	// yaml-first：按旧 url 定位条目改写为新 name:url，再写 bbolt。
	if s.cfgSrc != nil {
		if err := s.cfgSrc.UpdateSubscriptionEntry(oldURL, sub.Name, sub.URL); err != nil {
			respondError(w, r, http.StatusInternalServerError, CodeInternalError, err.Error())
			return
		}
	}
	if err := s.store.UpdateSubscription(sub); err != nil {
		respondStoreErr(w, r, err)
		return
	}
	writeJSON(w, sub)
}

// handleSubscriptionDelete DELETE /api/v1/subscriptions/{id}
func (s *Server) handleSubscriptionDelete(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		respondAPIError(w, r, errNotImplemented)
		return
	}
	id := urlParamUint64(r, "id")
	sub, err := s.store.GetSubscription(id)
	if err != nil {
		respondStoreErr(w, r, err)
		return
	}
	if sub == nil {
		respondAPIError(w, r, errNotFoundAPI)
		return
	}
	// yaml-first：先从 yaml 移除（按 url），再删 bbolt。
	if s.cfgSrc != nil {
		if err := s.cfgSrc.RemoveSubscription(sub.URL); err != nil {
			respondError(w, r, http.StatusInternalServerError, CodeInternalError, err.Error())
			return
		}
	}
	if err := s.store.DeleteSubscription(id); err != nil {
		respondStoreErr(w, r, err)
		return
	}
	writeJSON(w, map[string]any{"message": "订阅已删除"})
}

// handleSubscriptionsRefresh POST /api/v1/subscriptions/refresh：全量刷新。
func (s *Server) handleSubscriptionsRefresh(w http.ResponseWriter, r *http.Request) {
	if s.subRefresher == nil {
		respondAPIError(w, r, errNotImplemented)
		return
	}
	if err := s.subRefresher.RefreshNow(); err != nil {
		respondError(w, r, http.StatusInternalServerError, CodeInternalError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"message": "订阅刷新完成", "status": s.subRefresher.Status()})
}

// handleSubscriptionRefresh POST /api/v1/subscriptions/{id}/refresh：单订阅刷新。
func (s *Server) handleSubscriptionRefresh(w http.ResponseWriter, r *http.Request) {
	if s.subRefresher == nil {
		respondAPIError(w, r, errNotImplemented)
		return
	}
	id := urlParamUint64(r, "id")
	if err := s.subRefresher.RefreshOne(id); err != nil {
		respondError(w, r, http.StatusInternalServerError, CodeInternalError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"message": "订阅刷新完成", "subscription_id": id})
}
