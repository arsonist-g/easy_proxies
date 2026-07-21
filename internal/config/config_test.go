package config

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestExampleYAMLParses 验证 config.example.yaml 能被解析，且访问控制三段字段符合预期。
// 同时守住安全约束：示例 allow_provinces 只允许 北京/上海（不暴露用户所在地）。
func TestExampleYAMLParses(t *testing.T) {
	data, err := os.ReadFile("../../config.example.yaml")
	if err != nil {
		t.Skip("config.example.yaml 不存在（非项目根运行），跳过")
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("config.example.yaml 解析失败: %v", err)
	}
	if cfg.GeoCN.DatabasePath != "GeoCN.mmdb" {
		t.Errorf("geocn.database_path = %q, want GeoCN.mmdb", cfg.GeoCN.DatabasePath)
	}
	if cfg.GeoCN.AutoDownload == nil || !*cfg.GeoCN.AutoDownload {
		t.Error("geocn.auto_download 示例应为 true")
	}
	if cfg.AccessControl.Enabled != false {
		t.Error("access_control.enabled 示例应为 false（默认关闭）")
	}
	if !cfg.AccessControl.ChinaOnly {
		t.Error("access_control.china_only 示例应为 true")
	}
	if cfg.AccessControl.UnknownISP != "deny" {
		t.Errorf("access_control.unknown_isp = %q, want deny", cfg.AccessControl.UnknownISP)
	}
	// 安全约束：示例省份只能是 北京/上海，不得包含其他（避免暴露所在地）
	prov := cfg.AccessControl.AllowProvinces
	if len(prov) != 2 {
		t.Fatalf("access_control.allow_provinces 示例应有 2 项（北京/上海），实际 %d 项: %v", len(prov), prov)
	}
	for _, p := range prov {
		if p != "北京" && p != "上海" {
			t.Errorf("access_control.allow_provinces 示例含非北京/上海省份 %q（暴露所在地风险）", p)
		}
	}
	if !cfg.AccessLog.Enabled {
		t.Error("access_log.enabled 示例应为 true")
	}
	if cfg.AccessLog.Capacity != 10000 {
		t.Errorf("access_log.capacity = %d, want 10000", cfg.AccessLog.Capacity)
	}
}

// TestValidateAccessControlProvince 校验省份归一 + 非法省份报 ValidationError（不静默失败）。
func TestValidateAccessControlProvince(t *testing.T) {
	c := &Config{AccessControl: AccessControlConfig{AllowProvinces: []string{"广西壮族自治区", "桂", "450000", "北京"}}}
	if err := c.validateAccessControl(); err != nil {
		t.Fatalf("合法省份归一失败: %v", err)
	}
	want := []string{"广西", "北京"}
	if len(c.AccessControl.AllowProvinces) != len(want) {
		t.Fatalf("归一后省份 = %v, want %v", c.AccessControl.AllowProvinces, want)
	}
	seen := map[string]bool{}
	for _, p := range c.AccessControl.AllowProvinces {
		seen[p] = true
	}
	for _, w := range want {
		if !seen[w] {
			t.Errorf("归一后缺少 %q（得到 %v）", w, c.AccessControl.AllowProvinces)
		}
	}

	// 非法省份必须报错
	bad := &Config{AccessControl: AccessControlConfig{AllowProvinces: []string{"广夕"}}}
	if err := bad.validateAccessControl(); err == nil {
		t.Error("非法省份应返回错误，实际 nil")
	} else if _, ok := err.(*ValidationError); !ok {
		t.Errorf("非法省份应返回 *ValidationError，实际 %T", err)
	}
}
