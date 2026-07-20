// Package countryprobe 经代理节点访问 Cloudflare cdn-cgi/trace，
// 解析出口真实 IP（ip=）与国家码（loc=），用于节点国家标注。
//
// 失败降级为"未知"，不阻断节点可用性判定（ADR-0004）。
package countryprobe

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// 默认探测目标：Cloudflare 主站 cdn-cgi/trace（域名，经代理远程解析）。
// 不用 1.1.1.1：实测 CF 对 1.1.1.1 的 /cdn-cgi/trace 对大量代理出口 IP 返回 403 或拒连
// （反滥用），导致 ~37% 可用节点被误判死。www.cloudflare.com 是稳定端点，无此限制。
const (
	traceEndpoint = "www.cloudflare.com:443"
	traceHost     = "www.cloudflare.com"
	tracePath     = "/cdn-cgi/trace"
)

// Result 国家探测结果。
type Result struct {
	ExitIP      string        // 节点出口真实 IP
	CountryCode string        // ISO 3166-1 alpha-2（大写），未知为空
	CountryName string        // 国家名称（中文），未知为 "未知"
	Latency     time.Duration // dial+TLS+HTTP 全程耗时
}

// DialFunc 经节点出站的拨号函数（来自 sing-box outbound 包装）。
type DialFunc func(network, addr string) (net.Conn, error)

// Prober 无状态，可复用。
type Prober struct{}

// New 构造 Prober。
func New() *Prober { return &Prober{} }

// Probe 经 dial 访问 cdn-cgi/trace，返回出口 IP / 国家码 / 延迟。
// 任一步失败返回错误，由调用方决定降级策略。
func (p *Prober) Probe(ctx context.Context, dial DialFunc) (Result, error) {
	if dial == nil {
		return Result{}, fmt.Errorf("dial func 未提供")
	}

	start := time.Now()
	raw, err := dial("tcp", traceEndpoint)
	if err != nil {
		return Result{}, fmt.Errorf("dial %s: %w", traceEndpoint, err)
	}

	// ctx 超时则强制关闭底层连接
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = raw.Close()
		case <-done:
		}
	}()
	defer close(done)

	tlsConn := tls.Client(raw, &tls.Config{
		InsecureSkipVerify: true, // 节点证书各异，仅取 IP/国家，不校验
		ServerName:         traceHost,
	})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return Result{}, fmt.Errorf("tls handshake: %w", err)
	}

	reqLine := "GET " + tracePath + " HTTP/1.1\r\n" +
		"Host: " + traceHost + "\r\n" +
		"Connection: close\r\n" +
		"User-Agent: easy-proxies/1.0\r\n\r\n"
	if _, err := tlsConn.Write([]byte(reqLine)); err != nil {
		_ = tlsConn.Close()
		return Result{}, fmt.Errorf("write request: %w", err)
	}

	body, err := io.ReadAll(tlsConn) // Connection: close，读到 EOF
	_ = tlsConn.Close()
	if err != nil {
		return Result{}, fmt.Errorf("read response: %w", err)
	}

	ip, loc := parseTrace(body)
	res := Result{
		ExitIP:  ip,
		Latency: time.Since(start),
	}
	if loc != "" {
		res.CountryCode = strings.ToUpper(loc)
		res.CountryName = CountryName(res.CountryCode)
	} else {
		res.CountryName = "未知"
	}
	if ip == "" && loc == "" {
		return res, fmt.Errorf("cdn-cgi/trace 响应无 ip/loc 字段")
	}
	return res, nil
}

// parseTrace 从响应全文（含 HTTP header）中提取 ip=/loc=。
// header 不会出现这些裸字段，全文逐行扫描即可。
func parseTrace(data []byte) (ip, loc string) {
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	sc.Buffer(make([]byte, 0, 4096), 64*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "ip="):
			ip = strings.TrimPrefix(line, "ip=")
		case strings.HasPrefix(line, "loc="):
			loc = strings.TrimPrefix(line, "loc=")
		}
	}
	return ip, loc
}
