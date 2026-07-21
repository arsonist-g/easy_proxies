// Package poolgateway 在 pool/multi-port 代理入口前做访问控制，再转发到 pool outbound。
//
// 替代原 sing-box HTTPMixed inbound：自定义 listener → Accept → accesscontrol.Check
// → sing http.HandleConnectionEx（完整 HTTP 代理：CONNECT + 明文 + Upgrade，行为同 HTTPMixed）
// → pool outbound.DialContext → bufio.CopyConn relay。
//
// 端口绑定从 sing-box inbound 移到此处：box.Start 不再因入口端口冲突失败，
// 端口冲突自愈在 openListener 内完成（递增重试 + 经 OnPortReassign 回写 cfg），无需重建 box。
// 生命周期由 boxmgr 拥有（依赖 box.Start 后的 outbound 句柄）。
package poolgateway

import (
	std_bufio "bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	"easy_proxies/internal/accesscontrol"
	"easy_proxies/internal/accesslog"
	"easy_proxies/internal/logger"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/common/auth"
	"github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/protocol/http"
)

// EntrySpec 描述一个网关入口（由 builder 从 cfg 生成）。
type EntrySpec struct {
	Tag      string // pool outbound tag（"proxy-pool" 或 "proxy-pool-{node}"）
	Inbound  string // 访问日志入口标签（"pool" 或 "multi-port:{node}"）
	Address  string
	Port     uint16
	Username string
	Password string
	// OnPortReassign 端口被占用而重分配时回调，用于回写 cfg（保持"实际端口=配置端口"）。
	// builder 创建 spec 时绑定到具体的 cfg 字段（cfg.Pool.Port / cfg.Nodes[idx].Port）。
	OnPortReassign func(newPort uint16)
}

// Gateway 管理所有 pool/multi-port 入口 listener。
type Gateway struct {
	mu        sync.Mutex
	listeners []*listenerEntry
	ctx       context.Context
	cancel    context.CancelFunc
	started   bool
}

type listenerEntry struct {
	spec     EntrySpec
	listener net.Listener
	outbound adapter.Outbound
	auth     *auth.Authenticator
}

// New 创建空 Gateway（未启动）。
func New() *Gateway { return &Gateway{} }

// Started 返回 Gateway 是否处于启动状态。
func (g *Gateway) Started() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.started
}

// Start 为每个 spec 开 listener 并进入 Accept 循环。
// 端口被占用时递增重试（最多 100 次），并经 OnPortReassign 回写 cfg。
// 任意 spec 失败则回滚已开的 listener。
func (g *Gateway) Start(ctx context.Context, entries []EntrySpec, obMgr adapter.OutboundManager) error {
	g.mu.Lock()
	if g.started {
		g.mu.Unlock()
		return errors.New("poolgateway: already started")
	}
	gwCtx, cancel := context.WithCancel(ctx)
	g.ctx, g.cancel = gwCtx, cancel

	started := make([]*listenerEntry, 0, len(entries))
	rollback := func(reason error) error {
		cancel()
		for _, l := range started {
			_ = l.listener.Close()
		}
		g.ctx, g.cancel = nil, nil
		g.mu.Unlock()
		return reason
	}

	for _, spec := range entries {
		ob, ok := obMgr.Outbound(spec.Tag)
		if !ok {
			return rollback(fmt.Errorf("poolgateway: outbound %q not found for entry %q", spec.Tag, spec.Inbound))
		}
		le, err := openListener(spec, ob)
		if err != nil {
			return rollback(fmt.Errorf("poolgateway: listen for %q: %w", spec.Inbound, err))
		}
		started = append(started, le)
		go le.serve(gwCtx)
	}
	g.listeners = started
	g.started = true
	g.mu.Unlock()
	return nil
}

// openListener 在 spec.Address:spec.Port 监听；端口被占用则递增重试，并回调回写 cfg。
func openListener(spec EntrySpec, ob adapter.Outbound) (*listenerEntry, error) {
	addr := spec.Address
	if addr == "" {
		addr = "0.0.0.0"
	}
	port := spec.Port
	var (
		listener net.Listener
		err      error
	)
	const maxAttempts = 100
	for attempt := 0; attempt < maxAttempts; attempt++ {
		listener, err = net.Listen("tcp", fmt.Sprintf("%s:%d", addr, port))
		if err == nil {
			break
		}
		if !isAddrInUse(err) {
			return nil, err // 非端口占用错误（如地址非法），直接返回
		}
		port++ // 端口占用 → 递增重试
	}
	if err != nil {
		return nil, err
	}
	if port != spec.Port {
		logger.Warnf("[poolgateway] %s 端口 %d 被占用，改用 %d", spec.Inbound, spec.Port, port)
		if spec.OnPortReassign != nil {
			spec.OnPortReassign(port)
		}
	}
	var authenticator *auth.Authenticator
	if spec.Username != "" || spec.Password != "" {
		authenticator = auth.NewAuthenticator([]auth.User{{Username: spec.Username, Password: spec.Password}})
	}
	return &listenerEntry{spec: spec, listener: listener, outbound: ob, auth: authenticator}, nil
}

// serve 接受连接循环；listener 关闭（Stop）时 Accept 报错退出。
func (le *listenerEntry) serve(ctx context.Context) {
	for {
		conn, err := le.listener.Accept()
		if err != nil {
			return
		}
		go le.handleConn(ctx, conn)
	}
}

// handleConn 处理单个客户端连接：访问控制 → HTTP 代理握手 → 转发 pool outbound。
func (le *listenerEntry) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	// 访问控制：按调用方源 IP 多层过滤（固定IP白名单>仅中国>省份>ISP/IDC）。
	// 拒绝则不读请求、不回响应——信息最少，不向探测者暴露"这是受访问控制的代理"。
	srcAddr := conn.RemoteAddr()
	srcIP := srcAddr.String()
	if h, _, err := net.SplitHostPort(srcIP); err == nil {
		srcIP = h
	}
	decision := accesscontrol.Check(srcIP)
	accesslog.Record(decision.Allowed, srcIP, decision.Reason, decision.Info.Province,
		decision.Info.ISP, decision.Info.NetType, "", le.spec.Inbound)
	if !decision.Allowed {
		return
	}

	// 复用 sing 的 HTTP 代理握手（CONNECT + 明文 + Upgrade，行为同 HTTPMixed），
	// dialHandler 把已解析目标的连接转发到 pool outbound。
	src := M.ParseSocksaddr(srcAddr.String())
	if err := http.HandleConnectionEx(ctx, conn, std_bufio.NewReader(conn), le.auth,
		&dialHandler{outbound: le.outbound}, src, func(error){}); err != nil {
		logger.Debugf("[poolgateway] %s handle error: %v", le.spec.Inbound, err)
	}
}

// dialHandler 实现 N.TCPConnectionHandlerEx：把已解析目标的连接转发到 pool outbound。
// CONNECT/Upgrade/明文三种场景均由 bufio.CopyConn 双向 relay 覆盖（与 sing-box HTTPMixed 等价）。
type dialHandler struct {
	outbound adapter.Outbound
}

func (h *dialHandler) NewConnectionEx(ctx context.Context, conn net.Conn, source, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	upstream, err := h.outbound.DialContext(ctx, N.NetworkTCP, destination)
	if err != nil {
		if onClose != nil {
			onClose(err)
		}
		return // conn 由调用方（handleConn 的 defer）关闭
	}
	// CopyConn 完成后会 CloseWrite/Close 两端；defer upstream.Close() 兜底（幂等）。
	defer upstream.Close()
	err = bufio.CopyConn(ctx, conn, upstream)
	if onClose != nil {
		onClose(err)
	}
}

// Stop 关闭所有 listener（已建立的连接任其自然结束或随 ctx 取消）。
func (g *Gateway) Stop() {
	g.mu.Lock()
	if !g.started {
		g.mu.Unlock()
		return
	}
	g.started = false
	if g.cancel != nil {
		g.cancel()
	}
	listeners := g.listeners
	g.listeners = nil
	g.ctx, g.cancel = nil, nil
	g.mu.Unlock()

	for _, le := range listeners {
		_ = le.listener.Close() // Accept 报错，serve 退出
	}
}

func isAddrInUse(err error) bool {
	return err != nil && strings.Contains(err.Error(), "address already in use")
}
