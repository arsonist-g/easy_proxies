// Package region 提供中国省级行政区(GB/T 2260)的规范化与查询。
//
// 设计目标:让用户在配置/前端里用任意常见写法填省份都能被正确识别——
// "广西"/"广西壮族自治区"/"桂"/"45"/"450000" 均归一到标准名 "广西";
// 无法识别的输入返回 ok=false(由调用方决定是报错还是放行),绝不静默吞掉。
//
// GeoCN.mmdb 的 division_code 字段是国标 6 位行政区划码,前 2 位即省级码,
// 故 ProvinceByCode 只取前 2 位查表,无论库返回省/市/县级码都能正确归省。
package region

import "strings"

// province 描述一个省级行政区。
type province struct {
	code2    string   // 2 位省级码 "11"(GB/T 2260 前 2 位)
	code6    string   // 6 位省级全码 "110000"(省级专用,后 4 位为 0)
	name     string   // 标准名 "北京" / "广西" / "内蒙古"(归一化目标)
	fullName string   // 全称 "北京市" / "广西壮族自治区"
	abbr     []string // 简称 ["京"] / ["桂"] / ["内蒙古","蒙"]
}

// provinces 中国 34 个省级行政区(GB/T 2260):23 省 + 5 自治区 + 4 直辖市 + 2 特别行政区。
// 标准名采用民政部惯用简称式(如 "广西" 而非 "广西壮族自治区"),便于展示与匹配。
var provinces = []province{
	{code2: "11", code6: "110000", name: "北京", fullName: "北京市", abbr: []string{"京"}},
	{code2: "12", code6: "120000", name: "天津", fullName: "天津市", abbr: []string{"津"}},
	{code2: "13", code6: "130000", name: "河北", fullName: "河北省", abbr: []string{"冀"}},
	{code2: "14", code6: "140000", name: "山西", fullName: "山西省", abbr: []string{"晋"}},
	{code2: "15", code6: "150000", name: "内蒙古", fullName: "内蒙古自治区", abbr: []string{"内蒙古", "蒙"}},
	{code2: "21", code6: "210000", name: "辽宁", fullName: "辽宁省", abbr: []string{"辽"}},
	{code2: "22", code6: "220000", name: "吉林", fullName: "吉林省", abbr: []string{"吉"}},
	{code2: "23", code6: "230000", name: "黑龙江", fullName: "黑龙江省", abbr: []string{"黑"}},
	{code2: "31", code6: "310000", name: "上海", fullName: "上海市", abbr: []string{"沪", "申"}},
	{code2: "32", code6: "320000", name: "江苏", fullName: "江苏省", abbr: []string{"苏"}},
	{code2: "33", code6: "330000", name: "浙江", fullName: "浙江省", abbr: []string{"浙"}},
	{code2: "34", code6: "340000", name: "安徽", fullName: "安徽省", abbr: []string{"皖"}},
	{code2: "35", code6: "350000", name: "福建", fullName: "福建省", abbr: []string{"闽"}},
	{code2: "36", code6: "360000", name: "江西", fullName: "江西省", abbr: []string{"赣"}},
	{code2: "37", code6: "370000", name: "山东", fullName: "山东省", abbr: []string{"鲁"}},
	{code2: "41", code6: "410000", name: "河南", fullName: "河南省", abbr: []string{"豫"}},
	{code2: "42", code6: "420000", name: "湖北", fullName: "湖北省", abbr: []string{"鄂"}},
	{code2: "43", code6: "430000", name: "湖南", fullName: "湖南省", abbr: []string{"湘"}},
	{code2: "44", code6: "440000", name: "广东", fullName: "广东省", abbr: []string{"粤"}},
	{code2: "45", code6: "450000", name: "广西", fullName: "广西壮族自治区", abbr: []string{"桂"}},
	{code2: "46", code6: "460000", name: "海南", fullName: "海南省", abbr: []string{"琼"}},
	{code2: "50", code6: "500000", name: "重庆", fullName: "重庆市", abbr: []string{"渝"}},
	{code2: "51", code6: "510000", name: "四川", fullName: "四川省", abbr: []string{"川", "蜀"}},
	{code2: "52", code6: "520000", name: "贵州", fullName: "贵州省", abbr: []string{"贵", "黔"}},
	{code2: "53", code6: "530000", name: "云南", fullName: "云南省", abbr: []string{"云", "滇"}},
	{code2: "54", code6: "540000", name: "西藏", fullName: "西藏自治区", abbr: []string{"藏"}},
	{code2: "61", code6: "610000", name: "陕西", fullName: "陕西省", abbr: []string{"陕", "秦"}},
	{code2: "62", code6: "620000", name: "甘肃", fullName: "甘肃省", abbr: []string{"甘", "陇"}},
	{code2: "63", code6: "630000", name: "青海", fullName: "青海省", abbr: []string{"青"}},
	{code2: "64", code6: "640000", name: "宁夏", fullName: "宁夏回族自治区", abbr: []string{"宁"}},
	{code2: "65", code6: "650000", name: "新疆", fullName: "新疆维吾尔自治区", abbr: []string{"新"}},
	{code2: "71", code6: "710000", name: "台湾", fullName: "台湾省", abbr: []string{"台"}},
	{code2: "81", code6: "810000", name: "香港", fullName: "香港特别行政区", abbr: []string{"港"}},
	{code2: "82", code6: "820000", name: "澳门", fullName: "澳门特别行政区", abbr: []string{"澳"}},
}

// regionSuffixes 行政区划后缀(长在前,保证 "壮族自治区" 先于 "自治区" 被剥离)。
var regionSuffixes = []string{
	"特别行政区",
	"壮族自治区",
	"回族自治区",
	"维吾尔自治区",
	"自治区",
	"省",
	"市",
}

// aliasMap 预计算所有可识别称呼 → 标准名。登记标准名/全称/简称/2 位码/6 位码。
var aliasMap = func() map[string]string {
	m := make(map[string]string, len(provinces)*6)
	for _, p := range provinces {
		m[p.name] = p.name
		m[p.fullName] = p.name
		m[p.code2] = p.name
		m[p.code6] = p.name
		for _, a := range p.abbr {
			m[a] = p.name
		}
	}
	return m
}()

// code2Map 2 位省级码 → 标准名,供 ProvinceByCode 用。
var code2Map = func() map[string]string {
	m := make(map[string]string, len(provinces))
	for _, p := range provinces {
		m[p.code2] = p.name
	}
	return m
}()

// standardNames 所有标准名(稳定顺序),供前端下拉选项生成。
var standardNames = func() []string {
	out := make([]string, len(provinces))
	for i, p := range provinces {
		out[i] = p.name
	}
	return out
}()

// Normalize 把任意常见省份写法归一为标准名。
// 识别:标准名("广西")、全称("广西壮族自治区")、简称("桂")、
// 2 位码("45")、6 位码("450000")以及去后缀后的误写("广西省"→"广西")。
// 识别失败返回 ok=false,调用方应据此报 422 而非静默放行。
func Normalize(in string) (string, bool) {
	s := strings.TrimSpace(in)
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "　", "") // 全角空格
	if s == "" {
		return "", false
	}
	// 直接命中别名表(覆盖标准名/全称/简称/码)
	if std, ok := aliasMap[s]; ok {
		return std, true
	}
	// 去后缀兜底:处理 "广西省" 之类的误写(标准是自治区,但用户习惯加"省")
	for _, suf := range regionSuffixes {
		if strings.HasSuffix(s, suf) {
			if std, ok := aliasMap[strings.TrimSuffix(s, suf)]; ok {
				return std, true
			}
		}
	}
	// 纯数字且 ≥2 位:按省级码前缀查(450123 → 45 → 广西)
	if isAllDigit(s) && len(s) >= 2 {
		if std, ok := aliasMap[s[:2]]; ok {
			return std, true
		}
	}
	return "", false
}

// ProvinceByCode 按国标行政区划码查省标准名,取前 2 位(省/市/县级码均可)。
// 用于把 GeoCN 的 division_code 归一到省份名。空/过短/未知返回 ok=false。
func ProvinceByCode(code string) (string, bool) {
	code = strings.TrimSpace(code)
	if len(code) < 2 {
		return "", false
	}
	std, ok := code2Map[code[:2]]
	return std, ok
}

// StandardNames 返回全部标准名(稳定顺序),供前端下拉选项。
func StandardNames() []string {
	out := make([]string, len(standardNames))
	copy(out, standardNames)
	return out
}

// isAllDigit 判断字符串是否全为 ASCII 数字。
func isAllDigit(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
