package monitor

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"easy_proxies/internal/store"
)

// generateToken 生成 prefix + 24 字节随机 hex 的凭证明文。
func generateToken(prefix string) string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// apiKeyView API Key 视图（列表不返回明文，仅前缀；明文经 /api-keys/{id}/plain 接口按需取）。
type apiKeyView struct {
	ID         uint64    `json:"id"`
	Name       string    `json:"name"`
	KeyPrefix  string    `json:"key_prefix"`
	Scopes     []string  `json:"scopes,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at,omitempty"`
	Revoked    bool      `json:"revoked"`
}

// subscribeTokenView 订阅 token 视图（列表不返回明文，仅前缀；明文经 plain 接口按需取）。
type subscribeTokenView struct {
	ID          uint64        `json:"id"`
	Name        string        `json:"name"`
	TokenPrefix string        `json:"token_prefix"`
	Filters     store.Filters `json:"filters,omitempty"`
	CreatedAt   time.Time     `json:"created_at"`
	LastUsedAt  time.Time     `json:"last_used_at,omitempty"`
	Revoked     bool          `json:"revoked"`
}

// handleAPIKeysList GET /api/v1/api-keys
func (s *Server) handleAPIKeysList(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		respondAPIError(w, r, errNotImplemented)
		return
	}
	keys, err := s.store.ListAPIKeys()
	if err != nil {
		respondStoreErr(w, r, err)
		return
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].ID < keys[j].ID })
	views := make([]apiKeyView, len(keys))
	for i, k := range keys {
		views[i] = apiKeyView{k.ID, k.Name, k.KeyPrefix, k.Scopes, k.CreatedAt, k.LastUsedAt, k.Revoked()}
	}
	page, pageSize, offset := parsePage(r)
	total := len(views)
	if offset > total {
		offset = total
	}
	end := offset + pageSize
	if end > total {
		end = total
	}
	writeJSON(w, newPageResponse(views[offset:end], total, page, pageSize))
}

// handleAPIKeyCreate POST /api/v1/api-keys：明文 key 仅此一次返回。
func (s *Server) handleAPIKeyCreate(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		respondAPIError(w, r, errNotImplemented)
		return
	}
	var req struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondAPIError(w, r, errBadRequest)
		return
	}
	plain := generateToken("epk_live_")
	cipher, err := encryptSecret(s.credKeyHex(), plain)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, CodeInternalError, "加密凭证失败: "+err.Error())
		return
	}
	key := &store.APIKey{
		Name:      req.Name,
		KeyHash:   store.HashSecret(plain),
		KeyPrefix: plain[:16],
		KeyCipher: cipher,
		Scopes:    req.Scopes,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.store.SaveAPIKey(key); err != nil {
		respondStoreErr(w, r, err)
		return
	}
	writeJSONStatus(w, http.StatusCreated, map[string]any{
		"id":         key.ID,
		"name":       key.Name,
		"key":        plain,
		"key_prefix": key.KeyPrefix,
		"scopes":     key.Scopes,
		"message":    "API Key 已创建，可在列表点复制随时获取明文",
	})
}

// handleAPIKeyDelete DELETE /api/v1/api-keys/{id}：硬删除（记录 + by_hash 索引）。
func (s *Server) handleAPIKeyDelete(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		respondAPIError(w, r, errNotImplemented)
		return
	}
	id := urlParamUint64(r, "id")
	if err := s.store.DeleteAPIKey(id); err != nil {
		respondStoreErr(w, r, err)
		return
	}
	writeJSON(w, map[string]any{"message": "API Key 已删除", "id": id})
}

// handleAPIKeyPlain GET /api/v1/api-keys/{id}/plain：返回明文供复制（session 全权限；列表不返回明文，点复制时按需取）。
func (s *Server) handleAPIKeyPlain(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		respondAPIError(w, r, errNotImplemented)
		return
	}
	k, err := s.store.GetAPIKey(urlParamUint64(r, "id"))
	if err != nil {
		respondStoreErr(w, r, err)
		return
	}
	if k == nil {
		respondAPIError(w, r, errNotFoundAPI)
		return
	}
	plain, derr := decryptSecret(s.credKeyHex(), k.KeyCipher)
	if derr != nil {
		respondError(w, r, http.StatusInternalServerError, CodeInternalError, "解密凭证失败: "+derr.Error())
		return
	}
	writeJSON(w, map[string]any{"plain": plain})
}

// handleSubscribeTokenPlain GET /api/v1/subscribe-tokens/{id}/plain：返回明文供复制。
func (s *Server) handleSubscribeTokenPlain(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		respondAPIError(w, r, errNotImplemented)
		return
	}
	t, err := s.store.GetSubscribeToken(urlParamUint64(r, "id"))
	if err != nil {
		respondStoreErr(w, r, err)
		return
	}
	if t == nil {
		respondAPIError(w, r, errNotFoundAPI)
		return
	}
	plain, derr := decryptSecret(s.credKeyHex(), t.TokenCipher)
	if derr != nil {
		respondError(w, r, http.StatusInternalServerError, CodeInternalError, "解密凭证失败: "+derr.Error())
		return
	}
	writeJSON(w, map[string]any{"plain": plain})
}

// handleSubscribeTokensList GET /api/v1/subscribe-tokens
func (s *Server) handleSubscribeTokensList(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		respondAPIError(w, r, errNotImplemented)
		return
	}
	tokens, err := s.store.ListSubscribeTokens()
	if err != nil {
		respondStoreErr(w, r, err)
		return
	}
	sort.Slice(tokens, func(i, j int) bool { return tokens[i].ID < tokens[j].ID })
	views := make([]subscribeTokenView, len(tokens))
	for i, t := range tokens {
		views[i] = subscribeTokenView{t.ID, t.Name, t.TokenPrefix, t.Filters, t.CreatedAt, t.LastUsedAt, t.Revoked()}
	}
	page, pageSize, offset := parsePage(r)
	total := len(views)
	if offset > total {
		offset = total
	}
	end := offset + pageSize
	if end > total {
		end = total
	}
	writeJSON(w, newPageResponse(views[offset:end], total, page, pageSize))
}

// handleSubscribeTokenCreate POST /api/v1/subscribe-tokens：明文 token 仅此一次返回。
func (s *Server) handleSubscribeTokenCreate(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		respondAPIError(w, r, errNotImplemented)
		return
	}
	var req struct {
		Name    string          `json:"name"`
		Filters store.Filters   `json:"filters"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondAPIError(w, r, errBadRequest)
		return
	}
	plain := generateToken("eps_")
	cipher, err := encryptSecret(s.credKeyHex(), plain)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, CodeInternalError, "加密凭证失败: "+err.Error())
		return
	}
	tok := &store.SubscribeToken{
		Name:        req.Name,
		TokenHash:   store.HashSecret(plain),
		TokenPrefix: plain[:12],
		TokenCipher: cipher,
		Filters:     req.Filters,
		CreatedAt:   time.Now().UTC(),
	}
	if err := s.store.SaveSubscribeToken(tok); err != nil {
		respondStoreErr(w, r, err)
		return
	}
	writeJSONStatus(w, http.StatusCreated, map[string]any{
		"id":           tok.ID,
		"name":         tok.Name,
		"token":        plain,
		"token_prefix": tok.TokenPrefix,
		"filters":      tok.Filters,
		"message":      "订阅 Token 已创建，可在列表点复制随时获取订阅链接",
	})
}

// handleSubscribeTokenDelete DELETE /api/v1/subscribe-tokens/{id}：硬删除（记录 + by_hash 索引）。
func (s *Server) handleSubscribeTokenDelete(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		respondAPIError(w, r, errNotImplemented)
		return
	}
	id := urlParamUint64(r, "id")
	if err := s.store.DeleteSubscribeToken(id); err != nil {
		respondStoreErr(w, r, err)
		return
	}
	writeJSON(w, map[string]any{"message": "订阅 Token 已删除", "id": id})
}
