package store

import "time"

// API Key scope 常量（api-contract §2.6）。
const (
	ScopeManageNodes         = "manage_nodes"
	ScopeManageSubscriptions = "manage_subscriptions"
	ScopeManagePools         = "manage_pools"
	ScopeManageCredentials   = "manage_credentials"
	ScopeReadOnly            = "read_only"
)

// 订阅刷新状态。
const (
	SubStatusSuccess = "success"
	SubStatusFailed  = "failed"
	SubStatusRunning = "running"
	SubStatusNever   = "never"
)

// Subscription 订阅实体（data-model §1.2，从 config.yaml 的 []string 实体化）。
type Subscription struct {
	ID                uint64    `json:"id"`
	Name              string    `json:"name"`
	URL               string    `json:"url"`
	Type              string    `json:"type,omitempty"` // base64/clash/plain/singbox（自动检测）
	LastRefreshAt     time.Time `json:"last_refresh_at,omitempty"`
	LastRefreshStatus string    `json:"last_refresh_status,omitempty"` // success/failed/running/never
	LastError         string    `json:"last_error,omitempty"`
	NodeCount         int       `json:"node_count,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// APIKey 管理 API 凭证（X-API-Key header）。
// KeyHash（sha256）供鉴权反查；KeyCipher（AES-256-GCM 密文）落 db，
// plain 接口用 config 的 credential_key 解密返回明文（点复制时取）。列表仅返回前缀。
type APIKey struct {
	ID         uint64    `json:"id"`
	Name       string    `json:"name"`
	KeyHash    string    `json:"key_hash"`   // sha256(明文) hex，鉴权反查用
	KeyPrefix  string    `json:"key_prefix"` // 明文前缀，列表 UI 识别
	KeyCipher  string    `json:"key_cipher"` // AES-256-GCM 密文（base64），plain 接口解密返回明文
	Scopes     []string  `json:"scopes,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at,omitempty"`
	RevokedAt  time.Time `json:"revoked_at,omitempty"`
}

// Revoked 是否已吊销。
func (k *APIKey) Revoked() bool { return !k.RevokedAt.IsZero() }

// HasScope 是否拥有指定 scope（read_only 隐含所有读）。
func (k *APIKey) HasScope(scope string) bool {
	for _, s := range k.Scopes {
		if s == scope || s == ScopeManageCredentials { // manage_credentials 视为可管理
			return true
		}
	}
	return false
}

// SubscribeToken 代理列表端点凭证（/sub/{token}）。
// TokenHash（sha256）供鉴权反查；TokenCipher（AES-256-GCM 密文）落 db，plain 接口解密返回明文。
type SubscribeToken struct {
	ID          uint64    `json:"id"`
	Name        string    `json:"name"`
	TokenHash   string    `json:"token_hash"`
	TokenPrefix string    `json:"token_prefix"`
	TokenCipher string    `json:"token_cipher"` // AES-256-GCM 密文（base64），plain 接口解密返回明文
	Filters     Filters   `json:"filters,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	LastUsedAt  time.Time `json:"last_used_at,omitempty"`
	RevokedAt   time.Time `json:"revoked_at,omitempty"`
}

// Revoked 是否已吊销。
func (t *SubscribeToken) Revoked() bool { return !t.RevokedAt.IsZero() }

// Filters 订阅 token 绑定的过滤视图（api-contract §3.5）。
type Filters struct {
	CountryCodes []string `json:"country_codes,omitempty"`
	Protocols    []string `json:"protocols,omitempty"`
	NameRegex    string   `json:"name_regex,omitempty"`
}

// NodeProbe 探测结果持久化（data-model §1.2，ADR-0004）。
type NodeProbe struct {
	NodeID             string    `json:"node_id"` // = NodeConfig.stable_id
	ExitIP             string    `json:"exit_ip"`
	CountryCode        string    `json:"country_code"`
	CountryName        string    `json:"country_name,omitempty"`
	LastProbeAt        time.Time `json:"last_probe_at"`
	LastProbeSuccess   bool      `json:"last_probe_success"`
	LastProbeLatencyMs int64     `json:"last_probe_latency_ms"`
}
