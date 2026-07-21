package builder

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"easy_proxies/internal/config"
	poolout "easy_proxies/internal/outbound/pool"
	"easy_proxies/internal/poolgateway"

	"github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
)

// TestBuildBoxStartNoInbound 验证步骤5 的真实 sing-box 启动链路：
// builder.Build 返回空 inbound + gateway entries → box.New(无 inbound) → box.Start
// （核心风险点：空 inbound 能否启动）→ gateway.Start(真实 OutboundManager) → pool 入口端口监听。
// 节点不可达不影响 Start（dial 发生在转发时，box.Start 不连节点）。
func TestBuildBoxStartNoInbound(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	poolPort := uint16(probe.Addr().(*net.TCPAddr).Port)
	probe.Close()

	cfg := &config.Config{
		Mode: "pool",
		Pool: config.PoolConfig{
			Mode:              "sequential",
			FailureThreshold:  3,
			BlacklistDuration: 30 * time.Minute,
			ListenerConfig: config.ListenerConfig{
				Address: "127.0.0.1",
				Port:    poolPort,
			},
		},
		Nodes: []config.NodeConfig{
			{Name: "local-test", URI: "trojan://pass@127.0.0.1:1?sni=localhost#local-test"},
		},
	}

	opts, entries, err := Build(cfg)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(opts.Inbounds) != 0 {
		t.Fatalf("opts.Inbounds should be empty after step5 (inbound moved to gateway), got %d", len(opts.Inbounds))
	}
	if len(entries) == 0 {
		t.Fatal("entries should be non-empty (pool gateway entry)")
	}

	ctx := context.Background()
	outboundRegistry := include.OutboundRegistry()
	poolout.Register(outboundRegistry)
	boxCtx := box.Context(ctx, include.InboundRegistry(), outboundRegistry,
		include.EndpointRegistry(), include.DNSTransportRegistry(), include.ServiceRegistry())

	instance, err := box.New(box.Options{Context: boxCtx, Options: opts})
	if err != nil {
		t.Fatalf("box.New: %v", err)
	}
	// 核心断言：空 inbound 的 box 能启动（inbound 已全部迁移到 poolgateway 自定义 listener）
	if err := instance.Start(); err != nil {
		t.Fatalf("box.Start (no inbound): %v", err)
	}
	defer instance.Close()

	gw := poolgateway.New()
	if err := gw.Start(ctx, entries, instance.Outbound()); err != nil {
		t.Fatalf("gateway.Start: %v", err)
	}
	defer gw.Stop()
	if !gw.Started() {
		t.Fatal("gateway should be started")
	}

	// pool 入口端口真实监听（gateway 自定义 listener 已接管）
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(poolPort))), 2*time.Second)
	if err != nil {
		t.Fatalf("pool entry port %d not listening: %v", poolPort, err)
	}
	_ = conn.Close()
}
