package monitor

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"easy_proxies/internal/store"
)

// newRouter 构建 chi 路由器：HTML 入口 + /sub/{token} + /api/v1/*。
// 旧 /api/* 不保留（一次性迁移到 /api/v1）。
func newRouter(s *Server) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)

	// HTML 入口（可选路径密码保护；API 始终在 /api/v1 下，不受影响）
	if s.cfg.PathPwd != "" {
		custom := "/" + strings.Trim(s.cfg.PathPwd, "/")
		r.Get(custom, s.handleIndex)
		r.Get(custom+"/", s.handleIndex)
	} else {
		r.Get("/", s.handleIndex)
	}

	// 订阅 token 代理列表（独立鉴权路径，阶段3 实装）
	r.Group(func(r chi.Router) {
		r.Use(s.subTokenAuth)
		r.Get("/sub/{token}", s.handleProxyList)
	})

	// /api/v1
	r.Route("/api/v1", func(r chi.Router) {
		// 公开：login（无鉴权）
		r.Post("/auth/login", s.handleLogin)

		// 受三层凭证保护（session / X-API-Key）
		r.Group(func(r chi.Router) {
			r.Use(s.sessionOrAPIKeyAuth)

			r.Post("/auth/logout", s.handleLogout)
			r.Get("/auth/status", s.handleAuthStatus)

			// Stats
			r.Get("/stats", s.handleStats)

			// 节点
			r.Get("/nodes", s.handleNodesList)
			r.Get("/nodes/{stable_id}", s.handleNodeGet)
			r.Post("/nodes", s.handleNodeCreate)
			r.Patch("/nodes/{stable_id}", s.handleNodeUpdate)
			r.Delete("/nodes/{stable_id}", s.handleNodeDelete)
			r.Post("/nodes/{stable_id}/probe", s.handleNodeProbe)
			r.Post("/probe/all", s.handleProbeAll)

			// 订阅
			r.Get("/subscriptions", s.handleSubscriptionsList)
			r.Post("/subscriptions", s.handleSubscriptionCreate)
			r.Post("/subscriptions/refresh", s.handleSubscriptionsRefresh)
			r.Get("/subscriptions/{id}", s.handleSubscriptionGet)
			r.Patch("/subscriptions/{id}", s.handleSubscriptionUpdate)
			r.Delete("/subscriptions/{id}", s.handleSubscriptionDelete)
			r.Post("/subscriptions/{id}/refresh", s.handleSubscriptionRefresh)

			// 虚拟池
			r.Get("/virtual-pools", s.handleVirtualPoolsList)
			r.Post("/virtual-pools", s.handleVirtualPoolCreate)
			r.Get("/virtual-pools/next-port", s.handleVirtualPoolNextPort)
			r.Get("/virtual-pools/{id}", s.handleVirtualPoolGet)
			r.Patch("/virtual-pools/{id}", s.handleVirtualPoolUpdate)
			r.Delete("/virtual-pools/{id}", s.handleVirtualPoolDelete)
			r.Get("/virtual-pools/{id}/nodes", s.handleVirtualPoolNodes)

			// 凭证（session 全权限；apikey 需 manage_credentials scope）
			r.Group(func(r chi.Router) {
				r.Use(requireScope(store.ScopeManageCredentials))
				r.Get("/api-keys", s.handleAPIKeysList)
				r.Post("/api-keys", s.handleAPIKeyCreate)
				r.Get("/api-keys/{id}/plain", s.handleAPIKeyPlain)
				r.Delete("/api-keys/{id}", s.handleAPIKeyDelete)
				r.Get("/subscribe-tokens", s.handleSubscribeTokensList)
				r.Post("/subscribe-tokens", s.handleSubscribeTokenCreate)
				r.Get("/subscribe-tokens/{id}/plain", s.handleSubscribeTokenPlain)
				r.Delete("/subscribe-tokens/{id}", s.handleSubscribeTokenDelete)
			})

			// 告警 / 导出 / 设置
			r.Get("/alerts", s.handleAlertsList)
			r.Get("/export", s.handleExport)
			r.Get("/settings", s.handleSettingsGet)
			r.Put("/settings", s.handleSettingsPut)
		})
	})

	// 兜底：未匹配的路径回退到首页（SPA 友好），但 /api/* 旧路径返回 404
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			respondError(w, r, http.StatusNotFound, CodeNotFound, "端点不存在（已迁移到 /api/v1）")
			return
		}
		s.handleIndex(w, r)
	})

	return r
}
