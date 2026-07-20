package monitor

import (
	"encoding/json"
	"net/http"

	"easy_proxies/internal/alert"
)

// handleAlertsList GET /api/v1/alerts：扫描空密码等安全告警。
func (s *Server) handleAlertsList(w http.ResponseWriter, r *http.Request) {
	// alert_enabled 关闭时返回空列表 + enabled:false
	s.cfgMu.RLock()
	alertEnabled := true
	if s.cfgSrc != nil && s.cfgSrc.AlertEnabled != nil {
		alertEnabled = *s.cfgSrc.AlertEnabled
	}
	s.cfgMu.RUnlock()

	if !alertEnabled {
		writeJSON(w, map[string]any{"alerts": []alert.Item{}, "count": 0, "enabled": false})
		return
	}

	checker := alert.New()
	snapshots := s.mgr.Snapshot()
	nodes := make([]alert.NodeInput, 0, len(snapshots))
	for _, sn := range snapshots {
		nodes = append(nodes, alert.NodeInput{StableID: sn.StableID, Name: sn.Name, Password: sn.Password})
	}
	var pools []alert.PoolInput
	if s.vpMgr != nil {
		for _, st := range s.vpMgr.Status() {
			pools = append(pools, alert.PoolInput{Name: st.Name, Username: st.Username, Password: st.Password})
		}
	}
	items := checker.Check(nodes, pools)
	if items == nil {
		items = []alert.Item{}
	}
	writeJSON(w, map[string]any{"alerts": items, "count": len(items), "enabled": true})
}

// handleSettingsGet GET /api/v1/settings
func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	ip, _, skip := s.getSettings()
	s.cfgMu.RLock()
	alertEnabled := true
	if s.cfgSrc != nil && s.cfgSrc.AlertEnabled != nil {
		alertEnabled = *s.cfgSrc.AlertEnabled
	}
	// 节点端口代理凭证：hybrid/multi-port 用 multi_port 凭证，pool 用 listener 凭证。
	// 供前端拼"复制节点代理链接"（http://user:pwd@external_ip:port）。
	proxyUser, proxyPwd := "", ""
	mode, listenerPort := "", 0
	var poolEntry, mpEntry map[string]any
	if s.cfgSrc != nil {
		mode = s.cfgSrc.Mode
		listenerPort = int(s.cfgSrc.Pool.Port)
		if s.cfgSrc.Mode == "multi-port" || s.cfgSrc.Mode == "hybrid" {
			proxyUser, proxyPwd = s.cfgSrc.MultiPort.Username, s.cfgSrc.MultiPort.Password
		} else {
			proxyUser, proxyPwd = s.cfgSrc.Pool.Username, s.cfgSrc.Pool.Password
		}
		// 两组入口凭证（设置页入口密码编辑用）：pool 入口（listener）+ 多端口入口（multi_port）。
		// enabled 标记当前模式是否在用该入口，供前端决定是否渲染对应编辑块。
		poolEntry = map[string]any{
			"address":  s.cfgSrc.Pool.Address,
			"port":     int(s.cfgSrc.Pool.Port),
			"username": s.cfgSrc.Pool.Username,
			"password": s.cfgSrc.Pool.Password,
			"enabled":  s.cfgSrc.Mode == "pool" || s.cfgSrc.Mode == "hybrid",
		}
		mpEntry = map[string]any{
			"address":   s.cfgSrc.MultiPort.Address,
			"base_port": int(s.cfgSrc.MultiPort.BasePort),
			"username":  s.cfgSrc.MultiPort.Username,
			"password":  s.cfgSrc.MultiPort.Password,
			"enabled":   s.cfgSrc.Mode == "multi-port" || s.cfgSrc.Mode == "hybrid",
		}
	}
	s.cfgMu.RUnlock()
	writeJSON(w, map[string]any{
		"external_ip":      ip,
		"skip_cert_verify": skip,
		"alert_enabled":    alertEnabled,
		"proxy_username":   proxyUser,
		"proxy_password":   proxyPwd,
		"mode":             mode, // 运行模式 pool/hybrid/multi-port：pool 模式节点复制链接用 listener_port
		"listener_port":    listenerPort,
		"pool":             poolEntry,
		"multi_port":       mpEntry,
	})
}

// handleSettingsPut PUT /api/v1/settings（external_ip/skip_cert_verify/alert_enabled；不含 probe_target）
func (s *Server) handleSettingsPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ExternalIP        string  `json:"external_ip"`
		SkipCertVerify    *bool   `json:"skip_cert_verify"` // 指针：nil = 保留现有值（修复未提供时被重置为 false）
		AlertEnabled      *bool   `json:"alert_enabled"`
		PoolUsername      *string `json:"pool_username"`       // 代理池入口（pool/hybrid 的 listener）用户名，nil=保留
		PoolPassword      *string `json:"pool_password"`       // 代理池入口密码，nil=保留
		MultiPortUsername *string `json:"multi_port_username"` // 多端口入口（multi-port/hybrid）用户名，nil=保留
		MultiPortPassword *string `json:"multi_port_password"` // 多端口入口密码，nil=保留
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondAPIError(w, r, errBadRequest)
		return
	}
	// probe_target 不在 settings 暴露：保留现有值（运行时国家探测固定走 cdn-cgi/trace）
	// skip_cert_verify 未提供时同样保留现有值（避免 bool 零值 false 覆盖）
	_, probe, curSkip := s.getSettings()
	newSkip := curSkip
	if req.SkipCertVerify != nil {
		newSkip = *req.SkipCertVerify
	}
	if err := s.updateSettings(req.ExternalIP, probe, newSkip); err != nil {
		respondError(w, r, http.StatusInternalServerError, CodeInternalError, err.Error())
		return
	}
	// alert_enabled 与入口密码单独持久化（入口密码改后需重建内核生效）
	needReload := false
	s.cfgMu.Lock()
	if s.cfgSrc != nil {
		if req.AlertEnabled != nil {
			s.cfgSrc.AlertEnabled = req.AlertEnabled
		}
		// 入口凭证：两组入口各自独立写入（前端按模式只发对应组）。
		// 代理池入口（pool/hybrid 的 listener）；多端口入口（multi-port/hybrid，节点端口共享）。
		if req.PoolUsername != nil {
			s.cfgSrc.Pool.Username = *req.PoolUsername
			needReload = true
		}
		if req.PoolPassword != nil {
			s.cfgSrc.Pool.Password = *req.PoolPassword
			needReload = true
		}
		if req.MultiPortUsername != nil {
			s.cfgSrc.MultiPort.Username = *req.MultiPortUsername
			needReload = true
		}
		if req.MultiPortPassword != nil {
			s.cfgSrc.MultiPort.Password = *req.MultiPortPassword
			needReload = true
		}
		_ = s.cfgSrc.SaveSettings()
	}
	s.cfgMu.Unlock()
	// 入口密码变更需重建 sing-box 内核才能生效（与节点增删改同等，期间短暂中断连接）
	if needReload && s.nodeMgr != nil {
		if err := s.nodeMgr.TriggerReload(r.Context()); err != nil {
			// 密码已持久化，但热重载失败：下次重启仍会生效
			respondError(w, r, http.StatusInternalServerError, CodeInternalError, "入口密码已保存，但热重载失败（"+err.Error()+"），重启后生效")
			return
		}
	}
	writeJSON(w, map[string]any{"message": "设置已更新"})
}

// handleExport GET /api/v1/export：导出节点 URI 列表（支持与 /nodes 相同的过滤参数）。
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	snapshots := filterNodes(s.mgr.Snapshot(), r.URL.Query())
	uris := make([]string, 0, len(snapshots))
	for _, sn := range snapshots {
		if sn.URI != "" {
			uris = append(uris, sn.URI)
		}
	}
	writeJSON(w, map[string]any{"uris": uris, "count": len(uris)})
}
