package accesscontrol

import (
	"strings"
	"testing"
)

// withGeo 临时替换全局 geoLookup 注入固定判定,测试结束后由 t.Cleanup 恢复。
func withGeo(t *testing.T, fn func(string) GeoInfo) {
	t.Helper()
	orig := geoLookup
	geoLookup = fn
	t.Cleanup(func() { geoLookup = orig })
}

func TestDisabledAllowsAll(t *testing.T) {
	p, _ := Build(Options{Enabled: false, ChinaOnly: true})
	if d := p.Check("8.8.8.8"); !d.Allowed {
		t.Error("disabled 应放行")
	}
}

func TestNilPolicyAllowsAll(t *testing.T) {
	var p *Policy
	if d := p.Check("8.8.8.8"); !d.Allowed {
		t.Error("nil 策略应放行")
	}
	Set(nil) // 全局 nil
	if d := Check("8.8.8.8"); !d.Allowed {
		t.Error("全局 nil 应放行")
	}
}

func TestAllowIPsBypassAll(t *testing.T) {
	withGeo(t, func(string) GeoInfo { return GeoInfo{IsChina: false} })
	p, _ := Build(Options{Enabled: true, ChinaOnly: true, AllowIPs: []string{"8.8.8.0/24"}})
	if d := p.Check("8.8.8.8"); !d.Allowed {
		t.Errorf("IP白名单应放行(即使境外): %s", d.Reason)
	}
}

func TestChinaOnlyDeniesOverseas(t *testing.T) {
	withGeo(t, func(string) GeoInfo { return GeoInfo{IsChina: false} })
	p, _ := Build(Options{Enabled: true, ChinaOnly: true})
	if d := p.Check("8.8.8.8"); d.Allowed {
		t.Error("境外应拒绝")
	}
}

func TestProvinceWhitelist(t *testing.T) {
	withGeo(t, func(string) GeoInfo { return GeoInfo{IsChina: true, Province: "广东", ISP: "移动"} })
	p, _ := Build(Options{Enabled: true, ChinaOnly: true, AllowProvinces: []string{"广西"}})
	if d := p.Check("1.2.3.4"); d.Allowed {
		t.Errorf("非白名单省应拒绝: %s", d.Reason)
	}

	withGeo(t, func(string) GeoInfo { return GeoInfo{IsChina: true, Province: "广西", ISP: "移动"} })
	if d := p.Check("1.2.3.4"); !d.Allowed {
		t.Errorf("白名单省应放行: %s", d.Reason)
	}
}

func TestProvinceUnknownDenies(t *testing.T) {
	withGeo(t, func(string) GeoInfo { return GeoInfo{IsChina: true, Province: ""} })
	p, _ := Build(Options{Enabled: true, AllowProvinces: []string{"北京"}})
	if d := p.Check("1.2.3.4"); d.Allowed {
		t.Error("中国IP但查不到省份应拒绝(白名单模式)")
	}
}

func TestBlockIDC(t *testing.T) {
	withGeo(t, func(string) GeoInfo { return GeoInfo{IsChina: true, Province: "北京", NetType: "IDC"} })
	p, _ := Build(Options{Enabled: true, BlockIDC: true})
	if d := p.Check("1.2.3.4"); d.Allowed {
		t.Error("IDC 应拒绝")
	}
	// 非 IDC 不拦截
	withGeo(t, func(string) GeoInfo { return GeoInfo{IsChina: true, Province: "北京", NetType: "宽带"} })
	if d := p.Check("1.2.3.4"); !d.Allowed {
		t.Errorf("非IDC不应拦截: %s", d.Reason)
	}
}

func TestISPWhitelist(t *testing.T) {
	withGeo(t, func(string) GeoInfo { return GeoInfo{IsChina: true, Province: "北京", ISP: "移动"} })
	p, _ := Build(Options{Enabled: true, AllowISPs: []string{"电信", "移动"}})
	if d := p.Check("1.2.3.4"); !d.Allowed {
		t.Errorf("命中ISP应放行: %s", d.Reason)
	}
	// 已知但不命中
	withGeo(t, func(string) GeoInfo { return GeoInfo{IsChina: true, Province: "北京", ISP: "铁通"} })
	if d := p.Check("1.2.3.4"); d.Allowed {
		t.Error("非白名单ISP应拒绝")
	}
}

func TestUnknownISP(t *testing.T) {
	withGeo(t, func(string) GeoInfo { return GeoInfo{IsChina: true, Province: "北京", ISP: ""} })
	p, _ := Build(Options{Enabled: true, AllowISPs: []string{"移动"}, UnknownISP: "deny"})
	if d := p.Check("1.2.3.4"); d.Allowed {
		t.Error("未知ISP默认应拒绝")
	}
	p2, _ := Build(Options{Enabled: true, AllowISPs: []string{"移动"}, UnknownISP: "allow"})
	if d := p2.Check("1.2.3.4"); !d.Allowed {
		t.Error("未知ISP=allow应放行")
	}
}

func TestChinaOnlyAloneAllowsChineseIP(t *testing.T) {
	withGeo(t, func(string) GeoInfo { return GeoInfo{IsChina: true, Province: "北京", ISP: ""} })
	p, _ := Build(Options{Enabled: true, ChinaOnly: true}) // 无省/ISP 限制
	if d := p.Check("1.2.3.4"); !d.Allowed {
		t.Errorf("仅 china_only 时中国IP应放行: %s", d.Reason)
	}
}

func TestBuildInvalidCIDR(t *testing.T) {
	if _, err := Build(Options{Enabled: true, AllowIPs: []string{"not-a-cidr"}}); err == nil {
		t.Error("非法 CIDR 应报错")
	}
}

func TestSourceIPWithPort(t *testing.T) {
	withGeo(t, func(string) GeoInfo { return GeoInfo{IsChina: false} })
	p, _ := Build(Options{Enabled: true, ChinaOnly: true})
	if d := p.Check("8.8.8.8:12345"); d.Allowed {
		t.Error("带端口的境外IP应拒绝")
	}
}

func TestSetGetGlobal(t *testing.T) {
	p, _ := Build(Options{Enabled: true, ChinaOnly: true})
	Set(p)
	if Get() != p {
		t.Error("Set/Get 不一致")
	}
	Set(nil) // 清理,避免污染其他测试
}

// TestAllowReasonShowsProvinceAndISP 验证放行原因在省份+运营商都命中时写全
// （修复"只写运营商、漏省份"的日志不清问题）。
func TestAllowReasonShowsProvinceAndISP(t *testing.T) {
	withGeo(t, func(string) GeoInfo { return GeoInfo{IsChina: true, Province: "广西", ISP: "移动"} })
	p, _ := Build(Options{Enabled: true, ChinaOnly: true, AllowProvinces: []string{"广西"}, AllowISPs: []string{"移动"}})
	d := p.Check("1.2.3.4")
	if !d.Allowed {
		t.Fatalf("应放行: %s", d.Reason)
	}
	if !strings.Contains(d.Reason, "广西") || !strings.Contains(d.Reason, "移动") {
		t.Errorf("放行原因应同时含省份和运营商, got %q", d.Reason)
	}
}
