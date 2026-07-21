package poolgateway

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"easy_proxies/internal/accesscontrol"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
)

// mockOutbound 把所有 DialContext 转发到 target（本地 echo），模拟 pool outbound 上游。
type mockOutbound struct {
	target string
}

func (m *mockOutbound) Type() string           { return "mock" }
func (m *mockOutbound) Tag() string            { return "test" }
func (m *mockOutbound) Network() []string      { return []string{"tcp"} }
func (m *mockOutbound) Dependencies() []string { return nil }
func (m *mockOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	_ = ctx
	_ = network
	_ = destination
	return net.Dial("tcp", m.target)
}
func (m *mockOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	_ = ctx
	_ = destination
	return nil, errors.New("udp not supported in mock")
}

type mockMgr struct{ ob *mockOutbound }

func (m *mockMgr) Start(adapter.StartStage) error { return nil }
func (m *mockMgr) Close() error                   { return nil }
func (m *mockMgr) Outbounds() []adapter.Outbound  { return []adapter.Outbound{m.ob} }
func (m *mockMgr) Outbound(tag string) (adapter.Outbound, bool) {
	return m.ob, tag == "test"
}
func (m *mockMgr) Default() adapter.Outbound { return m.ob }
func (m *mockMgr) Remove(tag string) error   { return nil }
func (m *mockMgr) Create(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag, outboundType string, options any) error {
	_ = ctx
	_ = router
	_ = logger
	_ = tag
	_ = outboundType
	_ = options
	return nil
}

// readHTTPHeaders 逐字节读响应头直到空行 \r\n\r\n（避免 bufio 预读吞掉隧道 payload）。
func readHTTPHeaders(conn net.Conn) error {
	buf := make([]byte, 0, 256)
	one := make([]byte, 1)
	for {
		if _, err := conn.Read(one); err != nil {
			return err
		}
		buf = append(buf, one[0])
		if len(buf) >= 4 && string(buf[len(buf)-4:]) == "\r\n\r\n" {
			return nil
		}
		if len(buf) > 4096 {
			return errors.New("response header too long")
		}
	}
}

// TestGatewayCONNECTForward 验证 gateway 完整转发链路：
// Accept → accesscontrol.Check(放行) → HandleConnectionEx 解析 CONNECT → 回 200
// → dialHandler.DialContext(mock outbound) → bufio.CopyConn 双向 relay → echo 回显。
// 这是步骤5 的核心验证：复用 sing HandleConnectionEx（CONNECT + 隧道），行为同 HTTPMixed。
func TestGatewayCONNECTForward(t *testing.T) {
	// 本地 echo server（充当"上游节点"）
	echoLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo listen: %v", err)
	}
	defer echoLn.Close()
	go func() {
		for {
			c, err := echoLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) { defer c.Close(); _, _ = io.Copy(c, c) }(c) // 回显
		}
	}()
	echoAddr := echoLn.Addr().String()

	// accesscontrol：放行策略（gateway handleConn 会调 Check）
	policy, err := accesscontrol.Build(accesscontrol.Options{Enabled: false})
	if err != nil {
		t.Fatalf("build policy: %v", err)
	}
	accesscontrol.Set(policy)

	// 探测一个空闲端口给 gateway listener
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	gwPort := uint16(probe.Addr().(*net.TCPAddr).Port)
	probe.Close()

	gw := New()
	spec := EntrySpec{Tag: "test", Inbound: "test", Address: "127.0.0.1", Port: gwPort}
	if err := gw.Start(context.Background(), []EntrySpec{spec},
		&mockMgr{ob: &mockOutbound{target: echoAddr}}); err != nil {
		t.Fatalf("gateway start: %v", err)
	}
	defer gw.Stop()

	// 客户端：CONNECT 握手 + 隧道 echo
	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(int(gwPort)))
	if err != nil {
		t.Fatalf("dial gateway: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	if _, err := conn.Write([]byte("CONNECT example.com:80 HTTP/1.1\r\nHost: example.com:80\r\n\r\n")); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	// 读 200 响应头（CONNECT 成功）
	if err := readHTTPHeaders(conn); err != nil {
		t.Fatalf("read CONNECT response: %v", err)
	}

	// 隧道已建立：发 payload，验证 echo 回显
	payload := "ping-gateway\n"
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatalf("write tunnel payload: %v", err)
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(buf[:n]) != payload {
		t.Fatalf("echo = %q, want %q", string(buf[:n]), payload)
	}
}

// TestGatewayAccessControlDeny 验证访问控制拒绝时连接被直接关闭（不读请求、不回响应）。
func TestGatewayAccessControlDeny(t *testing.T) {
	// 拒绝策略：ChinaOnly 且无 GeoCN 数据 → 任何 IP 视为境外 → 拒绝
	policy, err := accesscontrol.Build(accesscontrol.Options{Enabled: true, ChinaOnly: true})
	if err != nil {
		t.Fatalf("build policy: %v", err)
	}
	accesscontrol.Set(policy)

	probe, _ := net.Listen("tcp", "127.0.0.1:0")
	gwPort := uint16(probe.Addr().(*net.TCPAddr).Port)
	probe.Close()

	gw := New()
	spec := EntrySpec{Tag: "test", Inbound: "test-deny", Address: "127.0.0.1", Port: gwPort}
	// 注意：outbound 不应被调用（拒绝早于转发）
	if err := gw.Start(context.Background(), []EntrySpec{spec},
		&mockMgr{ob: &mockOutbound{target: "127.0.0.1:1"}}); err != nil {
		t.Fatalf("gateway start: %v", err)
	}
	defer gw.Stop()

	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(int(gwPort)))
	if err != nil {
		t.Fatalf("dial gateway: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))

	if _, err := conn.Write([]byte("CONNECT example.com:80 HTTP/1.1\r\nHost: example.com:80\r\n\r\n")); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	// 拒绝：服务端直接 Close，读应得 EOF（无 200 响应）
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err == nil && n > 0 {
		// 收到数据说明未被拒绝（不应出现 200）
		if strings.HasPrefix(string(buf[:n]), "HTTP/") {
			t.Fatalf("expected connection closed (denied), got HTTP response: %q", string(buf[:n]))
		}
	}
}
