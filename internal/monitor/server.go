package monitor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"sync"
	"time"

	"easy_proxies/internal/config"
	"easy_proxies/internal/logger"
	"easy_proxies/internal/store"
)

//go:embed all:assets
var embeddedFS embed.FS

// NodeManager exposes config node CRUD and reload operations.
type NodeManager interface {
	ListConfigNodes(ctx context.Context) ([]config.NodeConfig, error)
	CreateNode(ctx context.Context, node config.NodeConfig) (config.NodeConfig, error)
	UpdateNode(ctx context.Context, stableID string, node config.NodeConfig) (config.NodeConfig, error)
	DeleteNode(ctx context.Context, stableID string) error
	TriggerReload(ctx context.Context) error
	GetCurrentMode() string // 获取当前运行模式（pool/multi-port/hybrid）
}

// Sentinel errors for node operations.
var (
	ErrNodeNotFound = errors.New("节点不存在")
	ErrNodeConflict = errors.New("节点名称或端口已存在")
	ErrInvalidNode  = errors.New("无效的节点配置")
)

// SubscriptionRefresher interface for subscription manager.
type SubscriptionRefresher interface {
	RefreshNow() error
	Status() SubscriptionStatus
	RefreshOne(subID uint64) error
}

// SubscriptionStatus represents subscription refresh status.
type SubscriptionStatus struct {
	LastRefresh   time.Time `json:"last_refresh"`
	NextRefresh   time.Time `json:"next_refresh"`
	NodeCount     int       `json:"node_count"`
	LastError     string    `json:"last_error,omitempty"`
	RefreshCount  int       `json:"refresh_count"`
	IsRefreshing  bool      `json:"is_refreshing"`
	NodesModified bool      `json:"nodes_modified"`
}

// VirtualPoolManager 虚拟池管理器接口
type VirtualPoolManager interface {
	Status() []VirtualPoolStatus
	GetPool(name string) VirtualPoolInstance
	ListVirtualPools() []config.VirtualPoolConfig
	CreateVirtualPool(cfg config.VirtualPoolConfig) (config.VirtualPoolConfig, error)
	UpdateVirtualPool(id uint64, cfg config.VirtualPoolConfig) (config.VirtualPoolConfig, error)
	DeleteVirtualPool(id uint64) error
	NextAvailablePort() (uint16, error)
}

// VirtualPoolStatus 虚拟池状态
type VirtualPoolStatus struct {
	Name         string `json:"name"`
	Regular      string `json:"regular"`
	Address      string `json:"address"`
	Port         uint16 `json:"port"`
	Strategy     string `json:"strategy"`
	MaxLatencyMs int    `json:"max_latency_ms"`
	NodeCount    int    `json:"node_count"`
	Running      bool   `json:"running"`
	Username     string `json:"username,omitempty"`
	Password     string `json:"password,omitempty"`
}

// VirtualPoolInstance 虚拟池实例接口
type VirtualPoolInstance interface {
	GetMatchingNodes() []Snapshot
}

// Server exposes HTTP endpoints for monitoring.
type Server struct {
	cfg          Config
	cfgMu        sync.RWMutex
	cfgSrc       *config.Config
	mgr          *Manager
	store        *store.Store // bbolt 存储（凭证/订阅/探测结果）
	srv          *http.Server
	router       http.Handler // chi 路由器
	logger       *log.Logger
	sessionToken string // 运行时随机 session token，重启失效
	subRefresher SubscriptionRefresher
	nodeMgr      NodeManager
	vpMgr        VirtualPoolManager
}

// NewServer constructs a server; it can be nil when disabled.
func NewServer(cfg Config, mgr *Manager, logger *log.Logger) *Server {
	if !cfg.Enabled || mgr == nil {
		return nil
	}
	if logger == nil {
		logger = log.Default()
	}
	s := &Server{cfg: cfg, mgr: mgr, logger: logger}

	// 生成随机 session token（重启失效）
	tokenBytes := make([]byte, 32)
	_, _ = rand.Read(tokenBytes)
	s.sessionToken = hex.EncodeToString(tokenBytes)

	s.router = newRouter(s)
	s.srv = &http.Server{Addr: cfg.Listen, Handler: s.router}
	return s
}

// SetStore 注入 bbolt 存储（凭证鉴权/订阅/探测结果）。
func (s *Server) SetStore(st *store.Store) {
	if s != nil {
		s.store = st
	}
}

// SetSubscriptionRefresher sets the subscription refresher for API endpoints.
func (s *Server) SetSubscriptionRefresher(sr SubscriptionRefresher) {
	if s != nil {
		s.subRefresher = sr
	}
}

// SetNodeManager enables config-node CRUD endpoints.
func (s *Server) SetNodeManager(nm NodeManager) {
	if s != nil {
		s.nodeMgr = nm
	}
}

// SetVirtualPoolManager sets the virtual pool manager for API endpoints.
func (s *Server) SetVirtualPoolManager(vpm VirtualPoolManager) {
	if s != nil {
		s.vpMgr = vpm
	}
}

// SetConfig binds the persistable config object for settings API.
func (s *Server) SetConfig(cfg *config.Config) {
	if s == nil {
		return
	}
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	s.cfgSrc = cfg
	if cfg != nil {
		s.cfg.ExternalIP = cfg.ExternalIP
		s.cfg.ProbeTarget = cfg.Management.ProbeTarget
		s.cfg.SkipCertVerify = cfg.SkipCertVerify
	}
}

// getSettings returns current dynamic settings (thread-safe).
func (s *Server) getSettings() (externalIP, probeTarget string, skipCertVerify bool) {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg.ExternalIP, s.cfg.ProbeTarget, s.cfg.SkipCertVerify
}

// credKeyHex 返回凭证加密密钥（hex），供凭证密文加解密；密钥来自 config，纯服务端使用。
func (s *Server) credKeyHex() string {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	if s.cfgSrc != nil {
		return s.cfgSrc.CredentialKey
	}
	return ""
}

// updateSettings updates dynamic settings and persists to config file.
func (s *Server) updateSettings(externalIP, probeTarget string, skipCertVerify bool) error {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()

	s.cfg.ExternalIP = externalIP
	s.cfg.ProbeTarget = probeTarget
	s.cfg.SkipCertVerify = skipCertVerify

	if s.cfgSrc == nil {
		return errors.New("配置存储未初始化")
	}

	s.cfgSrc.ExternalIP = externalIP
	s.cfgSrc.Management.ProbeTarget = probeTarget
	s.cfgSrc.SkipCertVerify = skipCertVerify

	if err := s.cfgSrc.SaveSettings(); err != nil {
		return err
	}
	return nil
}

// Start launches the HTTP server.
func (s *Server) Start(ctx context.Context) {
	if s == nil || s.srv == nil {
		return
	}
	logger.Infof("Starting monitor server on %s", s.cfg.Listen)
	go func() {
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Errorf("Monitor server error: %v", err)
		}
	}()
	go func() {
		<-ctx.Done()
		s.Shutdown(context.Background())
	}()
}

// Shutdown stops the server gracefully.
func (s *Server) Shutdown(ctx context.Context) {
	if s == nil || s.srv == nil {
		return
	}
	_ = s.srv.Shutdown(ctx)
}

// serveHTML 服务 assets/{page}.html：no-cache + ETag（内容 sha256 前 8 字节），支持 If-None-Match→304。
// F5 命中 304 不重新下载，但每次都发条件请求校验最新（解决 P4 必须强刷的问题）。
func (s *Server) serveHTML(page string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := embeddedFS.ReadFile("assets/" + page + ".html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		sum := sha256.Sum256(data)
		etag := `"` + page + "-" + hex.EncodeToString(sum[:8]) + `"`
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		w.Header().Set("ETag", etag)
		if match := r.Header.Get("If-None-Match"); match == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write(data)
	}
}

// serveAssets 服务 /assets/{css,js,fonts}/*：immutable 长缓存（路径固定，内容随构建变更）。
// 静态资源始终挂在根 /assets/，不受 path_pwd 影响（HTML 用绝对路径 /assets/... 引用）。
func (s *Server) serveAssets() http.Handler {
	sub, err := fs.Sub(embeddedFS, "assets")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	}
	fileServer := http.FileServer(http.FS(sub))
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		fileServer.ServeHTTP(w, r)
	})
	return http.StripPrefix("/assets/", inner)
}

// handleLogin POST /api/v1/auth/login：密码换 session cookie。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "方法不允许")
		return
	}
	s.cfgMu.RLock()
	password := s.cfg.Password
	s.cfgMu.RUnlock()

	if password == "" {
		writeJSON(w, map[string]any{"message": "无需密码", "no_password": true})
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondAPIError(w, r, errBadRequest)
		return
	}
	if req.Password != password {
		respondError(w, r, http.StatusUnauthorized, CodeUnauthorized, "密码错误")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    s.sessionToken,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   86400 * 7,
	})
	writeJSON(w, map[string]any{"message": "登录成功", "token": s.sessionToken})
}

// handleLogout POST /api/v1/auth/logout：清除 session cookie。
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "方法不允许")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1,
	})
	writeJSON(w, map[string]any{"message": "已登出"})
}

// handleAuthStatus GET /api/v1/auth/status：当前凭证信息。
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	info := authFromContext(r)
	method := "unknown"
	if info != nil {
		method = info.Method
	}
	s.cfgMu.RLock()
	hasPassword := s.cfg.Password != ""
	s.cfgMu.RUnlock()
	writeJSON(w, map[string]any{
		"auth_method":  method,
		"has_password": hasPassword,
	})
}

// handleNodesList GET /api/v1/nodes：节点运行时状态（含 stable_id），支持过滤 + 分页。
func (s *Server) handleNodesList(w http.ResponseWriter, r *http.Request) {
	snapshots := filterNodes(s.mgr.Snapshot(), r.URL.Query())
	snapshots = sortNodes(snapshots, r.URL.Query())
	page, pageSize, offset := parsePage(r)
	total := len(snapshots)
	if offset > total {
		offset = total
	}
	end := offset + pageSize
	if end > total {
		end = total
	}
	writeJSON(w, newPageResponse(snapshots[offset:end], total, page, pageSize))
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(payload)
}
