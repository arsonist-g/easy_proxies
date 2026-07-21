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

	// HTML 入口（可选路径密码保护；API 始终在 /api/v1，静态资源始终在 /assets，均不受 path_pwd 影响）
	prefix := ""
	if s.cfg.PathPwd != "" {
		prefix = "/" + strings.Trim(s.cfg.PathPwd, "/")
		// 无尾斜杠访问 → 重定向到带尾斜杠：确保前端相对 URL（location.replace('dashboard') 等）
		// 基于 /{pwd}/ 目录解析。否则 /{pwd} 被当文件、目录退回 /，相对跳转到 /dashboard（丢前缀 → 登录后不跳转）
		r.Get(prefix, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, prefix+"/", http.StatusFound)
		})
		r.Get(prefix+"/", s.serveHTML("index"))
	} else {
		r.Get("/", s.serveHTML("index"))
	}
	// 各功能页（pjax + 真 URL）：canonical 无后缀（/alerts），保留 .html 兼容旧书签/直链
	for _, pg := range []string{"dashboard", "nodes", "subs", "pools", "creds", "alerts", "access_log", "settings"} {
		r.Get(prefix+"/"+pg, s.serveHTML(pg))
		r.Get(prefix+"/"+pg+".html", s.serveHTML(pg))
	}
	// 静态资源（/assets/* 始终根，HTML 用绝对路径 /assets/... 引用）
	r.Get("/assets/*", s.serveAssets().ServeHTTP)

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
			r.Get("/probe/progress", s.handleProbeProgress)

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

			// 访问控制（热换生效，不 reload）/ 访问日志
			r.Get("/access-control", s.handleAccessControlGet)
			r.Put("/access-control", s.handleAccessControlPut)
			r.Get("/access-logs", s.handleAccessLogsList)
		})
	})

	// 兜底：/api/* 旧路径 → 404；/assets/ 缺失 → 真 404（不回 index，否则吞字体/css）；
	// path_pwd 非空时，只有 /{pwd}/... 前缀下的未知路径回 index（SPA fallback），根 / 及其他
	// 路径 → 404（隐藏入口，不暴露登录页）；path_pwd 空时所有未知路径回 index
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasPrefix(path, "/api/") {
			respondError(w, r, http.StatusNotFound, CodeNotFound, "端点不存在（已迁移到 /api/v1）")
			return
		}
		if strings.HasPrefix(path, "/assets/") {
			http.NotFound(w, r)
			return
		}
		if prefix != "" && !(path == prefix || strings.HasPrefix(path, prefix+"/")) {
			http.NotFound(w, r)
			return
		}
		s.serveHTML("index")(w, r)
	})

	return r
}
