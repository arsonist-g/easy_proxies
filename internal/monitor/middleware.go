package monitor

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// session cookie 名（api-contract §2.6）。
const sessionCookieName = "ep_session"

type ctxKey string

const (
	ctxKeyAuthInfo ctxKey = "auth_info"
	ctxKeySubToken ctxKey = "sub_token"
)

// authInfo 鉴权结果（放入 context 供 handler 用）。
type authInfo struct {
	Method   string   // "session" / "apikey" / "subtoken" / "none"
	APIKeyID uint64
	Scopes   []string
}

func authFromContext(r *http.Request) *authInfo {
	v, _ := r.Context().Value(ctxKeyAuthInfo).(*authInfo)
	return v
}

// subTokenFromContext 取出订阅 token 记录（/sub/{token} handler 用）。
func subTokenFromContext(r *http.Request) any {
	return r.Context().Value(ctxKeySubToken)
}

// validSession 校验 session token 与服务端持有的随机 token。
func (s *Server) validSession(token string) bool {
	if token == "" {
		return false
	}
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return token == s.sessionToken
}

// sessionOrAPIKeyAuth 三层凭证中间件（/api/v1/*）：
// 无密码放行；否则 session cookie / Authorization Bearer / X-API-Key 任一通过。
func (s *Server) sessionOrAPIKeyAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.cfgMu.RLock()
		noPwd := s.cfg.Password == ""
		s.cfgMu.RUnlock()
		if noPwd {
			ctx := context.WithValue(r.Context(), ctxKeyAuthInfo, &authInfo{Method: "none"})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		// 1. session cookie
		if c, err := r.Cookie(sessionCookieName); err == nil && s.validSession(c.Value) {
			ctx := context.WithValue(r.Context(), ctxKeyAuthInfo, &authInfo{Method: "session"})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		// 2. Authorization: Bearer <session-token>
		if ah := r.Header.Get("Authorization"); strings.HasPrefix(ah, "Bearer ") {
			if s.validSession(strings.TrimPrefix(ah, "Bearer ")) {
				ctx := context.WithValue(r.Context(), ctxKeyAuthInfo, &authInfo{Method: "session"})
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}
		// 3. X-API-Key
		if key := r.Header.Get("X-API-Key"); key != "" {
			info, ok := s.validateAPIKey(key)
			if !ok {
				respondAPIError(w, r, errUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), ctxKeyAuthInfo, info)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		respondAPIError(w, r, errUnauthorized)
	})
}

// validateAPIKey 反查 API Key，返回鉴权信息。吊销/不存在/存储未就绪均判失败。
func (s *Server) validateAPIKey(plain string) (*authInfo, bool) {
	if s.store == nil {
		return nil, false
	}
	k, err := s.store.FindAPIKeyByHash(plain)
	if err != nil || k == nil || k.Revoked() {
		return nil, false
	}
	go s.touchAPIKeyUsed(k.ID)
	return &authInfo{Method: "apikey", APIKeyID: k.ID, Scopes: k.Scopes}, true
}

// subTokenAuth 订阅 token 中间件（/sub/{token}）。
func (s *Server) subTokenAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := chi.URLParam(r, "token")
		if token == "" || s.store == nil {
			respondAPIError(w, r, errUnauthorized)
			return
		}
		t, err := s.store.FindSubscribeTokenByHash(token)
		if err != nil || t == nil || t.Revoked() {
			respondAPIError(w, r, errUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), ctxKeyAuthInfo, &authInfo{Method: "subtoken"})
		ctx = context.WithValue(ctx, ctxKeySubToken, t)
		go s.touchSubTokenUsed(t.ID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireScope 要求 API Key 拥有指定 scope；session / 无密码模式全权限放行。
func requireScope(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			info := authFromContext(r)
			if info == nil || info.Method == "none" || info.Method == "session" {
				next.ServeHTTP(w, r)
				return
			}
			if info.Method == "apikey" {
				for _, sc := range info.Scopes {
					if sc == scope {
						next.ServeHTTP(w, r)
						return
					}
				}
				respondAPIError(w, r, errForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// touchAPIKeyUsed 更新 API Key 最近使用时间（best-effort）。
func (s *Server) touchAPIKeyUsed(id uint64) {
	if s.store == nil {
		return
	}
	if k, err := s.store.GetAPIKey(id); err == nil && k != nil {
		k.LastUsedAt = time.Now().UTC()
		_ = s.store.UpdateAPIKey(k)
	}
}

// touchSubTokenUsed 更新订阅 token 最近使用时间（best-effort）。
func (s *Server) touchSubTokenUsed(id uint64) {
	if s.store == nil {
		return
	}
	if t, err := s.store.GetSubscribeToken(id); err == nil && t != nil {
		t.LastUsedAt = time.Now().UTC()
		_ = s.store.UpdateSubscribeToken(t)
	}
}
