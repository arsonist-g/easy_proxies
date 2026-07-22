// Package geocn 提供基于 GeoCN.mmdb 的中国 IP 省份/运营商查询。
//
// GeoCN(ljxi/GeoCN)仅收录中国大陆 IP,一次查询同时给出行政区划码(division_code,
// 国标 6 位,前 2 位为省级码)、运营商(isp:电信/联通/移动/广电/教育网)与网络类型
// (type)。查不到记录即判定为境外 IP——这正是访问控制"仅限中国"的判定依据。
//
// 设计与 internal/geoip 一致:进程级只读单例 + RWMutex,app.Run 启动时 Open 一次,
// 访问控制热路径调用 Lookup;未配置(库未就绪)时 Lookup 返回 ok=false(判定交策略)。
// maxminddb.Reader 在 Open 后并发安全。
package geocn

import (
	"fmt"
	"net/netip"
	"strconv"
	"sync"

	"github.com/oschwald/maxminddb-golang/v2"
)

var (
	mu     sync.RWMutex
	reader *maxminddb.Reader
)

// geoCNRecord 对应 GeoCN.mmdb 的记录结构(平铺三字段)。
// 注意:division_code 在库中是数值类型(uint64,国标 6 位行政区划码),必须用数值类型
// 接收;若用 string,maxminddb 解码会因类型不匹配失败,Lookup 把所有中国 IP 误判境外。
type geoCNRecord struct {
	DivisionCode uint64 `maxminddb:"division_code"`
	ISP          string `maxminddb:"isp"`
	NetType      string `maxminddb:"type"`
}

// Open 打开本地 GeoCN.mmdb。path 为空表示禁用(直接返回 nil)。
// 重复调用会先关闭已打开的 reader。
func Open(path string) error {
	if path == "" {
		return nil
	}
	db, err := maxminddb.Open(path)
	if err != nil {
		return fmt.Errorf("open geocn db %q: %w", path, err)
	}
	mu.Lock()
	if reader != nil {
		_ = reader.Close()
	}
	reader = db
	mu.Unlock()
	return nil
}

// Close 关闭已打开的 reader。
func Close() {
	mu.Lock()
	defer mu.Unlock()
	if reader != nil {
		_ = reader.Close()
		reader = nil
	}
}

// Lookup 查询 ip 的行政区划码/运营商/网络类型。
// 未配置 db / 解析失败 / 无记录均返回 ok=false(GeoCN 中无记录=境外 IP)。
// divisionCode 返回国标 6 位码的字符串形式(如 "451022"),供 ProvinceByCode 取前 2 位归一为省名。
func Lookup(ip string) (divisionCode, isp, netType string, ok bool) {
	mu.RLock()
	r := reader
	mu.RUnlock()
	if r == nil || ip == "" {
		return "", "", "", false
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return "", "", "", false
	}
	var rec geoCNRecord
	if err := r.Lookup(addr).Decode(&rec); err != nil {
		return "", "", "", false
	}
	if rec.DivisionCode == 0 && rec.ISP == "" && rec.NetType == "" {
		return "", "", "", false
	}
	return strconv.FormatUint(rec.DivisionCode, 10), rec.ISP, rec.NetType, true
}

// Loaded 报告当前是否已加载可用库(供装配阶段判断是否就绪)。
func Loaded() bool {
	mu.RLock()
	defer mu.RUnlock()
	return reader != nil
}
