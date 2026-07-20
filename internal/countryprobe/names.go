package countryprobe

// countryNames ISO 3166-1 alpha-2 → 中文国名（项目面向中文用户）。
// 未收录时 CountryName 返回 code 本身。
var countryNames = map[string]string{
	"US": "美国", "CA": "加拿大", "MX": "墨西哥",
	"BR": "巴西", "CL": "智利", "AR": "阿根廷", "CO": "哥伦比亚",
	"GB": "英国", "IE": "爱尔兰", "FR": "法国", "DE": "德国",
	"NL": "荷兰", "BE": "比利时", "LU": "卢森堡", "CH": "瑞士",
	"AT": "奥地利", "ES": "西班牙", "PT": "葡萄牙", "IT": "意大利",
	"SE": "瑞典", "NO": "挪威", "DK": "丹麦", "FI": "芬兰",
	"IS": "冰岛", "PL": "波兰", "CZ": "捷克", "SK": "斯洛伐克",
	"HU": "匈牙利", "RO": "罗马尼亚", "BG": "保加利亚", "HR": "克罗地亚",
	"RS": "塞尔维亚", "GR": "希腊", "SI": "斯洛文尼亚", "EE": "爱沙尼亚",
	"LV": "拉脱维亚", "LT": "立陶宛", "UA": "乌克兰", "MD": "摩尔多瓦",
	"RU": "俄罗斯", "TR": "土耳其", "IL": "以色列", "AE": "阿联酋",
	"SA": "沙特阿拉伯", "QA": "卡塔尔", "EG": "埃及", "ZA": "南非",
	"NG": "尼日利亚", "KE": "肯尼亚", "IN": "印度", "PK": "巴基斯坦",
	"BD": "孟加拉国", "LK": "斯里兰卡", "TH": "泰国", "VN": "越南",
	"ID": "印度尼西亚", "MY": "马来西亚", "SG": "新加坡", "PH": "菲律宾",
	"KH": "柬埔寨", "MM": "缅甸", "KR": "韩国", "JP": "日本",
	"CN": "中国", "TW": "台湾", "HK": "香港", "MO": "澳门",
	"MN": "蒙古", "KZ": "哈萨克斯坦", "AU": "澳大利亚", "NZ": "新西兰",
}

// CountryName 返回 alpha-2 国家码对应的中文国名；未收录时返回 code 本身。
func CountryName(code string) string {
	if name, ok := countryNames[code]; ok {
		return name
	}
	return code
}
