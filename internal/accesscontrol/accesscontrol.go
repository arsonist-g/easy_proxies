// Package accesscontrol 提供代理入口的多层调用方来源过滤。
//
// 评估顺序(短路,先匹配先返回):
//  1. enabled=false → 全部放行(功能关闭)
//  2. 命中 AllowIPs(CIDR 白名单)→ 放行(最高优先级,服务器到服务器)
//  3. GeoCN 查不到记录(境外)且 ChinaOnly → 拒绝
//  4. 省份不在 AllowProvinces 白名单 → 拒绝(配了白名单时省份必须命中)
//  5. 命中机房/IDC(BlockIDC)→ 拒绝(防国内 VPS 当肉鸡探测)
//  6. 命中 AllowISPs → 放行;已知但不命中 → 拒绝;未知 ISP → 按 UnknownISP(默认 deny)
//  7. 未配运营商白名单 → 放行(省份/IDC 已检过)
//
// 热换:atomic.Pointer[Policy],读路径无锁 Load;WebUI 改策略 Set() 立即生效、不断连、不 reload。
package accesscontrol

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync/atomic"

	"easy_proxies/internal/geocn"
	"easy_proxies/internal/region"
)

// Policy 访问控制策略(不可变;热换时整体替换,字段不做并发修改)。
type Policy struct {
	Enabled        bool
	AllowIPs       []netip.Prefix  // CIDR 白名单(最高优先级)
	ChinaOnly      bool            // 仅允许中国 IP
	AllowProvinces map[string]bool // 省份白名单(标准名)
	AllowISPs      []string        // 允许的运营商(如 电信/联通/移动)
	BlockIDC       bool            // 拒绝机房/数据中心 IP(GeoCN type 含 IDC)
	UnknownISP     string          // 未知 ISP 处理:deny/allow(默认 deny)
}

// Options 构造 Policy 的原始输入(CIDR/省份名均为字符串;省份应已 Normalize 为标准名)。
type Options struct {
	Enabled        bool
	AllowIPs       []string // CIDR 字符串,如 "127.0.0.0/8"
	ChinaOnly      bool
	AllowProvinces []string // 标准省名(经 region.Normalize 归一)
	AllowISPs      []string
	BlockIDC       bool
	UnknownISP     string
}

// Build 把 Options 解析为不可变 Policy。非法 CIDR 返回错误(配置阶段应据此报 422)。
func Build(o Options) (*Policy, error) {
	p := &Policy{
		Enabled:    o.Enabled,
		ChinaOnly:  o.ChinaOnly,
		BlockIDC:   o.BlockIDC,
		UnknownISP: strings.ToLower(strings.TrimSpace(o.UnknownISP)),
	}
	for _, c := range o.AllowIPs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		pfx, err := netip.ParsePrefix(c)
		if err != nil {
			return nil, fmt.Errorf("invalid allow_ips CIDR %q: %w", c, err)
		}
		p.AllowIPs = append(p.AllowIPs, pfx)
	}
	if len(o.AllowProvinces) > 0 {
		p.AllowProvinces = make(map[string]bool, len(o.AllowProvinces))
		for _, pr := range o.AllowProvinces {
			p.AllowProvinces[pr] = true
		}
	}
	p.AllowISPs = append(p.AllowISPs[:0:0], o.AllowISPs...)
	return p, nil
}

// GeoInfo GeoCN 查询结果(供决策与日志展示)。
type GeoInfo struct {
	IsChina  bool
	Province string // 标准省名(归一化后)
	ISP      string
	NetType  string
}

// Decision Check 结果。
type Decision struct {
	Allowed bool
	Reason  string
	Info    GeoInfo
}

// Check 评估源 IP 是否放行。srcIP 可为 "ip" 或 "ip:port"。
func (p *Policy) Check(srcIP string) Decision {
	if p == nil || !p.Enabled {
		return Decision{Allowed: true, Reason: "访问控制未启用"}
	}
	host := srcIP
	if h, _, err := net.SplitHostPort(srcIP); err == nil {
		host = h
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		// 无法解析的源 IP:保守拒绝,避免绕过(GeoCN 也查不了)
		return Decision{Allowed: false, Reason: "源IP无法解析: " + srcIP}
	}

	// 1. IP 白名单(最高优先级,优先于一切来源判定)
	for _, pfx := range p.AllowIPs {
		if pfx.Contains(addr) {
			return Decision{Allowed: true, Reason: "命中IP白名单"}
		}
	}

	info := geoLookup(host)

	// 2. 仅中国(境外拒绝)
	if p.ChinaOnly && !info.IsChina {
		return Decision{Allowed: false, Reason: "境外IP拒绝", Info: info}
	}

	// 3. 省份白名单:配了就必须命中(查不到省份视为不命中,保守拒绝)
	if len(p.AllowProvinces) > 0 {
		if info.Province == "" || !p.AllowProvinces[info.Province] {
			reason := "省份不在白名单"
			if info.Province != "" {
				reason = "省份不在白名单: " + info.Province
			}
			return Decision{Allowed: false, Reason: reason, Info: info}
		}
	}

	// 4. 机房/IDC 拦截
	if p.BlockIDC && isIDC(info.NetType) {
		return Decision{Allowed: false, Reason: "机房/数据中心IP拒绝", Info: info}
	}

	// 5. 运营商白名单
	if len(p.AllowISPs) > 0 {
		if info.ISP != "" {
			if containsString(p.AllowISPs, info.ISP) {
				return Decision{Allowed: true, Reason: "命中运营商白名单: " + info.ISP, Info: info}
			}
			return Decision{Allowed: false, Reason: "运营商不在白名单: " + info.ISP, Info: info}
		}
		// ISP 未知 → 按 UnknownISP(默认 deny)
		if p.UnknownISP == "allow" {
			return Decision{Allowed: true, Reason: "未知运营商放行", Info: info}
		}
		return Decision{Allowed: false, Reason: "未知运营商拒绝", Info: info}
	}

	// 6. 未配运营商白名单:省份/IDC 已检过,放行
	return Decision{Allowed: true, Reason: "通过", Info: info}
}

// geoLookup 把 IP 查询为 GeoInfo。默认走 GeoCN;测试可替换以注入固定判定。
var geoLookup = defaultGeoLookup

// defaultGeoLookup 查 GeoCN 并把行政区划码归一为标准省名。查不到(GeoCN 无记录)→ 境外。
func defaultGeoLookup(ip string) GeoInfo {
	code, isp, netType, ok := geocn.Lookup(ip)
	if !ok {
		return GeoInfo{IsChina: false}
	}
	province, _ := region.ProvinceByCode(code)
	return GeoInfo{
		IsChina:  true,
		Province: province,
		ISP:      isp,
		NetType:  netType,
	}
}

// isIDC 判定网络类型是否为机房/数据中心(GeoCN IPv6 type 值含 IDC;IPv4 通常无 type)。
func isIDC(netType string) bool {
	if netType == "" {
		return false
	}
	t := strings.ToLower(netType)
	return strings.Contains(t, "idc") || strings.Contains(t, "机房") || strings.Contains(t, "data")
}

// containsString 大小写不敏感包含判定(运营商名匹配容错)。
func containsString(list []string, s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, item := range list {
		if strings.ToLower(strings.TrimSpace(item)) == s {
			return true
		}
	}
	return false
}

// --- 进程级全局策略(热换) ---

var current atomic.Pointer[Policy]

// Set 设置当前策略(热换,原子替换)。
func Set(p *Policy) {
	current.Store(p)
}

// Get 返回当前策略(nil 表示未配置=不限制)。
func Get() *Policy {
	return current.Load()
}

// CheckPackage 全局策略的包级 Check(转发到当前策略;未配置时放行)。
// 代理入口热路径调用本函数。
func Check(srcIP string) Decision {
	return current.Load().Check(srcIP)
}
