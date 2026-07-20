// Package geoip 提供基于 MaxMind GeoLite2-ASN 的本地 ASN 查询（可选）。
//
// 设计：进程级只读单例。app.Run 在启动时按配置 Open 一次，pool/countryprobe
// 等热路径调用 LookupASN；未配置时 LookupASN 返回 ok=false（no-op），
// 与 logger 全局单例风格一致。maxminddb.Reader 在 Open 后并发安全。
package geoip

import (
	"fmt"
	"net/netip"
	"sync"

	"github.com/oschwald/maxminddb-golang/v2"
)

var (
	mu     sync.RWMutex
	reader *maxminddb.Reader
)

// asnRecord 对应 GeoLite2-ASN 的记录结构。
type asnRecord struct {
	AutonomousSystemNumber       uint   `maxminddb:"autonomous_system_number"`
	AutonomousSystemOrganization string `maxminddb:"autonomous_system_organization"`
}

// Open 打开本地 GeoLite2-ASN .mmdb。path 为空表示禁用（直接返回 nil）。
// 重复调用会先关闭已打开的 reader。
func Open(path string) error {
	if path == "" {
		return nil
	}
	db, err := maxminddb.Open(path)
	if err != nil {
		return fmt.Errorf("open geoip db %q: %w", path, err)
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

// LookupASN 查询 ip 的 ASN 与组织名。未配置 db / 解析失败 / 无记录均返回 ok=false。
func LookupASN(ip string) (asn uint, org string, ok bool) {
	mu.RLock()
	r := reader
	mu.RUnlock()
	if r == nil || ip == "" {
		return 0, "", false
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return 0, "", false
	}
	var rec asnRecord
	if err := r.Lookup(addr).Decode(&rec); err != nil {
		return 0, "", false
	}
	if rec.AutonomousSystemNumber == 0 && rec.AutonomousSystemOrganization == "" {
		return 0, "", false
	}
	return rec.AutonomousSystemNumber, rec.AutonomousSystemOrganization, true
}
