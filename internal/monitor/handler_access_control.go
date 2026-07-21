package monitor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"strings"

	"easy_proxies/internal/accesscontrol"
	"easy_proxies/internal/config"
	"easy_proxies/internal/geocn"
	"easy_proxies/internal/logger"
	"easy_proxies/internal/region"
)

// handleAccessControlGet GET /api/v1/access-control：返回当前访问控制配置 + GeoCN 就绪状态 + 省份下拉选项。
func (s *Server) handleAccessControlGet(w http.ResponseWriter, r *http.Request) {
	s.cfgMu.RLock()
	var ac config.AccessControlConfig
	geocnConfigured := false
	if s.cfgSrc != nil {
		ac = s.cfgSrc.AccessControl
		geocnConfigured = s.cfgSrc.GeoCN.DatabasePath != ""
	}
	s.cfgMu.RUnlock()

	// 空数组归一为 []（前端展示与拼接需要）
	if ac.AllowIPs == nil {
		ac.AllowIPs = []string{}
	}
	if ac.AllowProvinces == nil {
		ac.AllowProvinces = []string{}
	}
	if ac.AllowISPs == nil {
		ac.AllowISPs = []string{}
	}
	if ac.UnknownISP == "" {
		ac.UnknownISP = "deny"
	}

	writeJSON(w, map[string]any{
		"enabled":           ac.Enabled,
		"allow_ips":         ac.AllowIPs,
		"china_only":        ac.ChinaOnly,
		"allow_provinces":   ac.AllowProvinces,
		"allow_isps":        ac.AllowISPs,
		"block_idc":         ac.BlockIDC,
		"unknown_isp":       ac.UnknownISP,
		"geocn_configured":  geocnConfigured, // 是否配置了 geocn.database_path
		"geocn_loaded":      geocn.Loaded(),  // 库是否就绪（省份/ISP 判定是否可用）
		"provinces_options": region.StandardNames(),
	})
}

// handleAccessControlPut PUT /api/v1/access-control：更新配置→持久化→热换策略（不 reload、不断连）。
// nil 字段=保留原值；数组 nil=保留、空数组=清空；省份/CIDR/unknown_isp 校验失败返回 422。
func (s *Server) handleAccessControlPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled        *bool    `json:"enabled"`
		AllowIPs       []string `json:"allow_ips"`
		ChinaOnly      *bool    `json:"china_only"`
		AllowProvinces []string `json:"allow_provinces"`
		AllowISPs      []string `json:"allow_isps"`
		BlockIDC       *bool    `json:"block_idc"`
		UnknownISP     *string  `json:"unknown_isp"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondAPIError(w, r, errBadRequest)
		return
	}

	s.cfgMu.Lock()
	if s.cfgSrc == nil {
		s.cfgMu.Unlock()
		respondError(w, r, http.StatusInternalServerError, CodeInternalError, "配置存储未初始化")
		return
	}
	ac := s.cfgSrc.AccessControl // 拷贝当前值
	if req.Enabled != nil {
		ac.Enabled = *req.Enabled
	}
	if req.AllowIPs != nil {
		ac.AllowIPs = req.AllowIPs
	}
	if req.ChinaOnly != nil {
		ac.ChinaOnly = *req.ChinaOnly
	}
	if req.AllowProvinces != nil {
		ac.AllowProvinces = req.AllowProvinces
	}
	if req.AllowISPs != nil {
		ac.AllowISPs = req.AllowISPs
	}
	if req.BlockIDC != nil {
		ac.BlockIDC = *req.BlockIDC
	}
	if req.UnknownISP != nil {
		ac.UnknownISP = *req.UnknownISP
	}

	// 校验 unknown_isp（空默认 deny）
	switch strings.ToLower(strings.TrimSpace(ac.UnknownISP)) {
	case "", "deny":
		ac.UnknownISP = "deny"
	case "allow":
		ac.UnknownISP = "allow"
	default:
		s.cfgMu.Unlock()
		respondError(w, r, http.StatusUnprocessableEntity, CodeValidationError,
			fmt.Sprintf("unknown_isp %q 无效（用 deny 或 allow）", ac.UnknownISP))
		return
	}

	// 校验+归一省份（标准名/全称/简称/码均可；无法识别 422，不静默失败）
	if ac.AllowProvinces != nil {
		normalized := make([]string, 0, len(ac.AllowProvinces))
		seen := make(map[string]bool, len(ac.AllowProvinces))
		for _, p := range ac.AllowProvinces {
			std, ok := region.Normalize(p)
			if !ok {
				s.cfgMu.Unlock()
				respondError(w, r, http.StatusUnprocessableEntity, CodeValidationError,
					fmt.Sprintf("省份 %q 无法识别（用标准省名/简称/行政区划码，如 北京/京/110000）", p))
				return
			}
			if !seen[std] {
				seen[std] = true
				normalized = append(normalized, std)
			}
		}
		ac.AllowProvinces = normalized
	}

	// 校验 CIDR（提前校验，避免 Build 失败时已写 cfg）
	for _, c := range ac.AllowIPs {
		if _, err := netip.ParsePrefix(strings.TrimSpace(c)); err != nil {
			s.cfgMu.Unlock()
			respondError(w, r, http.StatusUnprocessableEntity, CodeValidationError,
				fmt.Sprintf("allow_ips %q 不是有效 CIDR", c))
			return
		}
	}

	// 构造策略（最终校验 + 构建）
	policy, err := accesscontrol.Build(accesscontrol.Options{
		Enabled:        ac.Enabled,
		AllowIPs:       ac.AllowIPs,
		ChinaOnly:      ac.ChinaOnly,
		AllowProvinces: ac.AllowProvinces,
		AllowISPs:      ac.AllowISPs,
		BlockIDC:       ac.BlockIDC,
		UnknownISP:     ac.UnknownISP,
	})
	if err != nil {
		s.cfgMu.Unlock()
		respondError(w, r, http.StatusUnprocessableEntity, CodeValidationError, err.Error())
		return
	}

	s.cfgSrc.AccessControl = ac
	saveErr := s.cfgSrc.SaveAccessControl()
	s.cfgMu.Unlock()
	if saveErr != nil {
		respondError(w, r, http.StatusInternalServerError, CodeInternalError, "保存配置失败: "+saveErr.Error())
		return
	}

	// 热换策略：atomic.Store，立即生效、不断连、不 reload
	accesscontrol.Set(policy)
	logger.Infof("🛡️ 访问控制已更新并热换生效（china_only=%v, provinces=%v, isps=%v）",
		ac.ChinaOnly, ac.AllowProvinces, ac.AllowISPs)
	writeJSON(w, map[string]any{"message": "访问控制已更新，立即生效"})
}
