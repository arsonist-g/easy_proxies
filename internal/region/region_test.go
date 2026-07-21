package region

import "testing"

func TestNormalize(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		// 标准名
		{"广西", "广西", true},
		{"北京", "北京", true},
		{"内蒙古", "内蒙古", true},
		// 全称
		{"广西壮族自治区", "广西", true},
		{"新疆维吾尔自治区", "新疆", true},
		{"宁夏回族自治区", "宁夏", true},
		{"西藏自治区", "西藏", true},
		{"香港特别行政区", "香港", true},
		{"北京市", "北京", true},
		{"河北省", "河北", true},
		// 简称
		{"桂", "广西", true},
		{"京", "北京", true},
		{"蒙", "内蒙古", true},
		{"沪", "上海", true},
		{"申", "上海", true},
		{"蜀", "四川", true},
		{"黔", "贵州", true},
		{"港", "香港", true},
		// 数字码
		{"45", "广西", true},
		{"450000", "广西", true},
		{"11", "北京", true},
		{"110000", "北京", true},
		{"450123", "广西", true}, // 县级码取前 2 位
		// 误写/容错
		{"广西省", "广西", true},      // 标准是自治区,但用户可能误写"省"
		{"  广西  ", "广西", true},    // 前后空格
		{"广 西", "广西", true},       // 中间空格
		{"广　西", "广西", true},       // 全角空格
		{"北京市市", "北京", true},    // 单次剥离"市"→"北京市"(全称)→命中,合理容错
		// 非法
		{"", "", false},
		{"   ", "", false},
		{"广夕", "", false},   // 不存在的省
		{"南省", "", false},   // 去后缀后"南"不匹配
		{"abc", "", false},   // 非法字符
		{"999", "", false},   // 不存在的码
		{"9", "", false},     // 数字不足 2 位
		{"东京", "", false},   // 非中国省份
	}
	for _, c := range cases {
		got, ok := Normalize(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("Normalize(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestNormalizeCoversAllProvinces(t *testing.T) {
	// 每个标准名、全称、简称、2位码、6位码都能识别(防止登记遗漏)
	for _, p := range provinces {
		for _, key := range append([]string{p.name, p.fullName, p.code2, p.code6}, p.abbr...) {
			got, ok := Normalize(key)
			if !ok {
				t.Errorf("Normalize(%q) 未识别(应=%s)", key, p.name)
				continue
			}
			if got != p.name {
				t.Errorf("Normalize(%q) = %q, want %q", key, got, p.name)
			}
		}
	}
}

func TestProvinceByCode(t *testing.T) {
	cases := []struct {
		code string
		want string
		ok   bool
	}{
		{"110000", "北京", true},
		{"11", "北京", true},
		{"450000", "广西", true},
		{"450123", "广西", true}, // 县级码
		{"820000", "澳门", true},
		{"1", "", false},   // 过短
		{"", "", false},    // 空
		{"990000", "", false}, // 不存在
		{"abcd00", "", false}, // 非数字前缀
	}
	for _, c := range cases {
		got, ok := ProvinceByCode(c.code)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("ProvinceByCode(%q) = (%q,%v), want (%q,%v)", c.code, got, ok, c.want, c.ok)
		}
	}
}

func TestStandardNames(t *testing.T) {
	names := StandardNames()
	if len(names) != 34 {
		t.Fatalf("StandardNames 长度 = %d, want 34", len(names))
	}
	// 确保返回的是拷贝(改返回值不影响包内)
	names[0] = "MUTATED"
	if standardNames[0] == "MUTATED" {
		t.Error("StandardNames 返回了包内切片(应返回拷贝)")
	}
}
