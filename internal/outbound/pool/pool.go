package pool

import (
	"context"
	"math/rand"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"easy_proxies/internal/availability"
	"easy_proxies/internal/countryprobe"
	"easy_proxies/internal/geoip"
	"easy_proxies/internal/monitor"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

const (
	// Type is the outbound type name exposed to sing-box.
	Type = "pool"
	// Tag is the default outbound tag used by builder.
	Tag = "proxy-pool"

	modeSequential = "sequential"
	modeRandom     = "random"
	modeBalance    = "balance"
	modeWeighted   = "weighted" // 加权随机：按延迟+可用率综合得分抽取（需 LatencyWeight/AvailabilityWeight）
)

// Options controls pool outbound behaviour.
type Options struct {
	Mode              string
	Members           []string
	FailureThreshold  int
	BlacklistDuration time.Duration
	Metadata          map[string]MemberMeta
	LatencyWeight      float64 // Mode=weighted：延迟权重（>0，内部归一化）
	AvailabilityWeight float64 // Mode=weighted：可用率权重（>0）
}

// MemberMeta carries optional descriptive information for monitoring UI.
type MemberMeta struct {
	Name          string
	URI           string
	StableID      string // 跨刷新稳定主键
	Mode          string
	ListenAddress string
	Port          uint16
	Username      string // 代理用户名
	Password      string // 代理密码
}

// Register wires the pool outbound into the registry.
func Register(registry *outbound.Registry) {
	outbound.Register[Options](registry, Type, newPool)
}

// 探测模式（分层探测，省流量）：CF 通过后常规健康检查降级为 apple:80 轻量探测。
// 节点的 IP/国家/ASN 只在更新时变化，CF 一次拿到后缓存，常规检查只需测"还活不活"。
const (
	probeModeCF    int32 = 0 // CF trace：拿可用性+延迟+出口IP+国家+ASN（新节点/Reload/CF 未通过）
	probeModeApple int32 = 1 // apple:80 轻量连通性（CF 已通过后常规健康检查，省 ~88% 流量）
	appleEndpoint        = "www.apple.com:80"
	appleHost            = "www.apple.com"
)

type memberState struct {
	outbound     adapter.Outbound
	tag          string
	stableID     string // 跨刷新稳定主键（可用率/去重 key）
	entry        *monitor.EntryHandle
	shared       *sharedMemberState
	probeMode    atomic.Int32  // 探测模式：见 probeMode* 常量；CF 成功后 Store(probeModeApple)
	lastLatencyMs atomic.Int64 // 最近探测延迟（毫秒），weighted 选择用；探测成功路径写入，选择时无锁读
}

type poolOutbound struct {
	outbound.Adapter
	ctx            context.Context
	logger         log.ContextLogger
	manager        adapter.OutboundManager
	options        Options
	mode           string
	wLat           float64 // weighted：延迟权重（归一化前的原始值，WeightedScore 内部归一化）
	wAvail         float64 // weighted：可用率权重
	members        []*memberState
	mu             sync.Mutex
	rrCounter      atomic.Uint32
	rng            *rand.Rand
	rngMu          sync.Mutex // protects rng for random mode
	monitor        *monitor.Manager
	tracker        *availability.Tracker   // 可用率统计（转发/探测计数），可空
	prober         *countryprobe.Prober    // 国家探测（cdn-cgi/trace），无状态
	candidatesPool sync.Pool
}

func newPool(ctx context.Context, _ adapter.Router, logger log.ContextLogger, tag string, options Options) (adapter.Outbound, error) {
	if len(options.Members) == 0 {
		return nil, E.New("pool requires at least one member")
	}
	manager := service.FromContext[adapter.OutboundManager](ctx)
	if manager == nil {
		return nil, E.New("missing outbound manager in context")
	}
	monitorMgr := monitor.FromContext(ctx)
	normalized := normalizeOptions(options)
	memberCount := len(normalized.Members)
	p := &poolOutbound{
		Adapter: outbound.NewAdapter(Type, tag, []string{N.NetworkTCP, N.NetworkUDP}, normalized.Members),
		ctx:     ctx,
		logger:  logger,
		manager: manager,
		options: normalized,
		mode:    normalized.Mode,
		wLat:    normalized.LatencyWeight,
		wAvail:  normalized.AvailabilityWeight,
		rng:     rand.New(rand.NewSource(time.Now().UnixNano())),
		monitor: monitorMgr,
		candidatesPool: sync.Pool{
			New: func() any {
				return make([]*memberState, 0, memberCount)
			},
		},
	}

	// 国家探测器（无状态）与可用率统计器（来自 monitor.Manager）
	p.prober = countryprobe.New()
	if monitorMgr != nil {
		p.tracker = monitorMgr.Tracker()
	}

	// Register nodes immediately if monitor is available
	if monitorMgr != nil {
		logger.Info("registering ", len(normalized.Members), " nodes to monitor")
		for _, memberTag := range normalized.Members {
			// Acquire shared state for this tag (creates if not exists)
			state := acquireSharedState(memberTag)

			meta := normalized.Metadata[memberTag]
			info := monitor.NodeInfo{
				Tag:           memberTag,
				StableID:      meta.StableID,
				Name:          meta.Name,
				URI:           meta.URI,
				Mode:          meta.Mode,
				ListenAddress: meta.ListenAddress,
				Port:          meta.Port,
				Username:      meta.Username,
				Password:      meta.Password,
			}
			entry := monitorMgr.Register(info)
			if entry != nil {
				// Attach entry to shared state so all pool instances share it
				state.attachEntry(entry)
				logger.Info("registered node: ", memberTag)
				// Set probe and release functions immediately
				entry.SetRelease(p.makeReleaseByTagFunc(memberTag))
				if probeFn := p.makeProbeByTagFunc(memberTag); probeFn != nil {
					entry.SetProbe(probeFn)
				}
			} else {
				logger.Warn("failed to register node: ", memberTag)
			}
		}
	} else {
		logger.Warn("monitor manager is nil, skipping node registration")
	}

	return p, nil
}

func normalizeOptions(options Options) Options {
	if options.FailureThreshold <= 0 {
		options.FailureThreshold = 3
	}
	if options.BlacklistDuration <= 0 {
		options.BlacklistDuration = 24 * time.Hour
	}
	if options.Metadata == nil {
		options.Metadata = make(map[string]MemberMeta)
	}
	switch strings.ToLower(options.Mode) {
	case modeRandom:
		options.Mode = modeRandom
	case modeBalance:
		options.Mode = modeBalance
	case modeWeighted:
		// weighted 需两个权重 >0；缺失则降级为 sequential（正常路径由 config 校验保证不触发）
		if options.LatencyWeight <= 0 || options.AvailabilityWeight <= 0 {
			options.Mode = modeSequential
		} else {
			options.Mode = modeWeighted
		}
	default:
		options.Mode = modeSequential
	}
	return options
}

func (p *poolOutbound) Start(stage adapter.StartStage) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	p.mu.Lock()
	err := p.initializeMembersLocked()
	p.mu.Unlock()
	if err != nil {
		return err
	}
	// 在初始化完成后，立即在后台触发健康检查
	if p.monitor != nil {
		go p.probeAllMembersOnStartup()
	}
	return nil
}

// initializeMembersLocked must be called with p.mu held
func (p *poolOutbound) initializeMembersLocked() error {
	if len(p.members) > 0 {
		return nil // Already initialized
	}

	members := make([]*memberState, 0, len(p.options.Members))
	for _, tag := range p.options.Members {
		detour, loaded := p.manager.Outbound(tag)
		if !loaded {
			return E.New("pool member not found: ", tag)
		}

		// Acquire shared state (creates if not exists, reuses if already created)
		state := acquireSharedState(tag)

		meta := p.options.Metadata[tag]
		member := &memberState{
			outbound: detour,
			tag:      tag,
			stableID: meta.StableID,
			shared:   state,
			entry:    state.entryHandle(),
		}

		// Connect to existing monitor entry if available
		if p.monitor != nil {
			info := monitor.NodeInfo{
				Tag:           tag,
				StableID:      meta.StableID,
				Name:          meta.Name,
				URI:           meta.URI,
				Mode:          meta.Mode,
				ListenAddress: meta.ListenAddress,
				Port:          meta.Port,
				Username:      meta.Username,
				Password:      meta.Password,
			}
			entry := p.monitor.Register(info)
			if entry != nil {
				state.attachEntry(entry)
				member.entry = entry
				entry.SetRelease(p.makeReleaseFunc(member))
				if probe := p.makeProbeFunc(member); probe != nil {
					entry.SetProbe(probe)
				}
			}
		}
		members = append(members, member)
	}
	p.members = members
	p.logger.Info("pool initialized with ", len(members), " members")

	return nil
}

// probeAllMembersOnStartup performs initial health checks on all members.
// 统一走 Cloudflare cdn-cgi/trace：一次取得 可用性+延迟+出口IP+国家+ASN。
func (p *poolOutbound) probeAllMembersOnStartup() {
	if p.prober == nil {
		p.logger.Warn("prober not initialized, skipping initial health check")
		p.mu.Lock()
		for _, member := range p.members {
			if member.entry != nil {
				member.entry.MarkInitialCheckDone(true)
			}
		}
		p.mu.Unlock()
		return
	}

	p.logger.Info("starting initial health check (Cloudflare cdn-cgi/trace) for all nodes")

	p.mu.Lock()
	members := make([]*memberState, len(p.members))
	copy(members, p.members)
	p.mu.Unlock()

	availableCount := 0
	failedCount := 0

	for _, member := range members {
		ctx, cancel := context.WithTimeout(p.ctx, 15*time.Second)
		latency, err := p.probeOnce(ctx, member)
		cancel()

		if err != nil {
			p.logger.Warn("initial probe failed for ", member.tag, ": ", err)
			failedCount++
			if member.entry != nil {
				member.entry.MarkInitialCheckDone(false)
			}
			continue
		}

		availableCount++
		if member.entry != nil {
			member.entry.MarkInitialCheckDone(true)
		}
		p.logger.Info("initial probe success for ", member.tag, ", latency: ", latency.Milliseconds(), "ms")
	}

	p.logger.Info("initial health check completed: ", availableCount, " available, ", failedCount, " failed")
}

func (p *poolOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	member, err := p.pickMember(network)
	if err != nil {
		return nil, err
	}
	p.incActive(member)
	conn, err := member.outbound.DialContext(ctx, network, destination)
	if err != nil {
		p.decActive(member)
		p.recordFailure(member, err)
		return nil, err
	}
	p.recordSuccess(member)
	return p.wrapConn(conn, member), nil
}

func (p *poolOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	member, err := p.pickMember(N.NetworkUDP)
	if err != nil {
		return nil, err
	}
	p.incActive(member)
	conn, err := member.outbound.ListenPacket(ctx, destination)
	if err != nil {
		p.decActive(member)
		p.recordFailure(member, err)
		return nil, err
	}
	p.recordSuccess(member)
	return p.wrapPacketConn(conn, member), nil
}

func (p *poolOutbound) pickMember(network string) (*memberState, error) {
	now := time.Now()
	candidates := p.getCandidateBuffer()

	p.mu.Lock()
	if len(p.members) == 0 {
		if err := p.initializeMembersLocked(); err != nil {
			p.mu.Unlock()
			p.putCandidateBuffer(candidates)
			return nil, err
		}
	}
	candidates = p.availableMembersLocked(now, network, candidates)
	p.mu.Unlock()

	if len(candidates) == 0 {
		p.mu.Lock()
		if p.releaseIfAllBlacklistedLocked(now) {
			candidates = p.availableMembersLocked(now, network, candidates)
		}
		p.mu.Unlock()
	}

	if len(candidates) == 0 {
		p.putCandidateBuffer(candidates)
		return nil, E.New("no healthy proxy available")
	}

	member := p.selectMember(candidates)
	p.putCandidateBuffer(candidates)
	return member, nil
}

func (p *poolOutbound) availableMembersLocked(now time.Time, network string, buf []*memberState) []*memberState {
	result := buf[:0]
	for _, member := range p.members {
		// Check blacklist via shared state (auto-clears if expired)
		if member.shared != nil && member.shared.isBlacklisted(now) {
			continue
		}
		if network != "" && !common.Contains(member.outbound.Network(), network) {
			continue
		}
		result = append(result, member)
	}
	return result
}

func (p *poolOutbound) releaseIfAllBlacklistedLocked(now time.Time) bool {
	if len(p.members) == 0 {
		return false
	}
	// Check if all members are blacklisted
	for _, member := range p.members {
		if member.shared == nil || !member.shared.isBlacklisted(now) {
			return false
		}
	}
	// All blacklisted, force release all
	for _, member := range p.members {
		if member.shared != nil {
			member.shared.forceRelease()
		}
	}
	p.logger.Warn("all upstream proxies were blacklisted, releasing them for retry")
	return true
}

func (p *poolOutbound) selectMember(candidates []*memberState) *memberState {
	switch p.mode {
	case modeRandom:
		p.rngMu.Lock()
		idx := p.rng.Intn(len(candidates))
		p.rngMu.Unlock()
		return candidates[idx]
	case modeBalance:
		var selected *memberState
		var minActive int32
		for _, member := range candidates {
			var active int32
			if member.shared != nil {
				active = member.shared.activeCount()
			}
			if selected == nil || active < minActive {
				selected = member
				minActive = active
			}
		}
		return selected
	case modeWeighted:
		// 加权随机：按延迟+可用率综合得分抽取。可用率一次取全量（Tracker.SnapshotAll，
		// 单次 RLock），延迟走 member.lastLatencyMs（atomic 无锁读）。
		if p.monitor == nil || p.tracker == nil || p.wLat <= 0 || p.wAvail <= 0 {
			// 监控不可用或权重缺失：降级顺序轮询，保证可用
			idx := int(p.rrCounter.Add(1)-1) % len(candidates)
			return candidates[idx]
		}
		rates := p.tracker.SnapshotAll()
		scores := make([]float64, len(candidates))
		for i, m := range candidates {
			av := rates[m.stableID]
			scores[i] = monitor.WeightedScore(m.lastLatencyMs.Load(), av.Rate, av.Total, p.wLat, p.wAvail)
		}
		p.rngMu.Lock()
		idx := monitor.PickWeighted(scores, p.rng)
		p.rngMu.Unlock()
		return candidates[idx]
	default:
		idx := int(p.rrCounter.Add(1)-1) % len(candidates)
		return candidates[idx]
	}
}

func (p *poolOutbound) recordFailure(member *memberState, cause error) {
	if member.shared == nil {
		p.logger.Warn("proxy ", member.tag, " failure (no shared state): ", cause)
	} else {
		failures, blacklisted, _ := member.shared.recordFailure(cause, p.options.FailureThreshold, p.options.BlacklistDuration)
		if blacklisted {
			p.logger.Warn("proxy ", member.tag, " blacklisted for ", p.options.BlacklistDuration, ": ", cause)
		} else {
			p.logger.Warn("proxy ", member.tag, " failure ", failures, "/", p.options.FailureThreshold, ": ", cause)
		}
	}
	p.recordCall(member, false)
}

func (p *poolOutbound) recordSuccess(member *memberState) {
	if member.shared != nil {
		member.shared.recordSuccess()
	}
	p.recordCall(member, true)
}

// recordCall 记录一次真实转发调用结果到可用率统计（call 路，无锁 atomic）。
func (p *poolOutbound) recordCall(member *memberState, success bool) {
	if p.tracker != nil && member.stableID != "" {
		p.tracker.RecordCall(member.stableID, success)
	}
}

// recordProbe 记录一次健康探测结果到可用率统计（probe 路）。
func (p *poolOutbound) recordProbe(member *memberState, success bool) {
	if p.tracker != nil && member.stableID != "" {
		p.tracker.RecordProbe(member.stableID, success)
	}
}

// probeOnce 经节点访问 Cloudflare cdn-cgi/trace，单次取得 可用性+延迟+出口IP+国家+ASN。
// 可用性探测与国家探测已统一为这一步（ADR-0004）：trace 成功即判可用并回填全部字段、
// 返回全程延迟；失败即判不可用。由 entry.probe（周期健康检查）与启动探测共用，
// 避免两套探测逻辑漂移与"可用但无国家"的不一致。
func (p *poolOutbound) probeOnce(ctx context.Context, member *memberState) (time.Duration, error) {
	if p.prober == nil {
		return 0, E.New("prober not initialized")
	}
	dial := func(network, addr string) (net.Conn, error) {
		return member.outbound.DialContext(ctx, network, M.ParseSocksaddr(addr))
	}
	res, err := p.prober.Probe(ctx, dial)
	if err != nil {
		if member.entry != nil {
			member.entry.RecordFailure(err)
		}
		p.recordProbe(member, false)
		return 0, err
	}
	if member.entry != nil {
		member.entry.SetCountry(res.ExitIP, res.CountryCode, res.CountryName)
		// 本地 GeoLite2-ASN 查询（可选）：未配置 geoip 时 LookupASN 返回 ok=false，ASN 字段留空
		if asn, org, ok := geoip.LookupASN(res.ExitIP); ok {
			member.entry.SetASN(asn, org)
		} else {
			member.entry.SetASN(0, "")
		}
		member.entry.RecordSuccessWithLatency(res.Latency)
		member.lastLatencyMs.Store(res.Latency.Milliseconds())
	}
	p.recordProbe(member, true)
	// CF 通过：国家/IP/ASN 已回填，后续常规检查降级为 apple:80 轻量探测（省 ~88% 流量）
	member.probeMode.Store(probeModeApple)
	return res.Latency, nil
}

// probeApple 轻量连通性探测：经节点 HTTP GET www.apple.com:80，收到响应即判可用。
// 仅用于 CF 已通过节点的常规健康检查（probeMode=apple），不重拿国家/IP（节点更新时
// 由 Reload 重建 member 重置为 CF 模式重探）。相比 CF trace 省 ~88% 流量（无 TLS 握手）。
func (p *poolOutbound) probeApple(ctx context.Context, member *memberState) (time.Duration, error) {
	dial := func(network, addr string) (net.Conn, error) {
		return member.outbound.DialContext(ctx, network, M.ParseSocksaddr(addr))
	}
	start := time.Now()
	conn, err := dial("tcp", appleEndpoint)
	if err != nil {
		if member.entry != nil {
			member.entry.RecordFailure(err)
		}
		p.recordProbe(member, false)
		return 0, err
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	reqLine := "GET / HTTP/1.1\r\nHost: " + appleHost + "\r\nConnection: close\r\nUser-Agent: easy-proxies/1.0\r\n\r\n"
	if _, err := conn.Write([]byte(reqLine)); err != nil {
		_ = conn.Close()
		close(done)
		if member.entry != nil {
			member.entry.RecordFailure(err)
		}
		p.recordProbe(member, false)
		return 0, err
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	_ = conn.Close()
	close(done)
	if err != nil || n == 0 {
		e := err
		if e == nil {
			e = E.New("apple probe empty response")
		}
		if member.entry != nil {
			member.entry.RecordFailure(e)
		}
		p.recordProbe(member, false)
		return 0, e
	}
	lat := time.Since(start)
	if member.entry != nil {
		member.entry.RecordSuccessWithLatency(lat)
	}
	member.lastLatencyMs.Store(lat.Milliseconds())
	p.recordProbe(member, true)
	return lat, nil
}

// dispatchProbe 按节点 probeMode 分派：CF 未通过走 CF trace（拿国家），已通过走 apple:80 轻量探测。
func (p *poolOutbound) dispatchProbe(ctx context.Context, member *memberState) (time.Duration, error) {
	if member.probeMode.Load() == probeModeApple {
		return p.probeApple(ctx, member)
	}
	return p.probeOnce(ctx, member)
}

func (p *poolOutbound) wrapConn(conn net.Conn, member *memberState) net.Conn {
	return &trackedConn{Conn: conn, release: func() {
		p.decActive(member)
	}}
}

func (p *poolOutbound) wrapPacketConn(conn net.PacketConn, member *memberState) net.PacketConn {
	return &trackedPacketConn{PacketConn: conn, release: func() {
		p.decActive(member)
	}}
}

func (p *poolOutbound) makeReleaseFunc(member *memberState) func() {
	return func() {
		if member.shared != nil {
			member.shared.forceRelease()
		}
	}
}

func (p *poolOutbound) makeProbeFunc(member *memberState) func(ctx context.Context) (time.Duration, error) {
	if p.monitor == nil {
		return nil
	}
	return func(ctx context.Context) (time.Duration, error) {
		// 分层探测：CF 通过后转 apple:80 轻量探测，省流量
		return p.dispatchProbe(ctx, member)
	}
}

// makeProbeByTagFunc creates a probe function that works before member initialization
func (p *poolOutbound) makeProbeByTagFunc(tag string) func(ctx context.Context) (time.Duration, error) {
	if p.monitor == nil {
		return nil
	}
	return func(ctx context.Context) (time.Duration, error) {
		// Ensure members are initialized
		p.mu.Lock()
		if len(p.members) == 0 {
			if err := p.initializeMembersLocked(); err != nil {
				p.mu.Unlock()
				return 0, err
			}
		}

		// Find the member by tag
		var member *memberState
		for _, m := range p.members {
			if m.tag == tag {
				member = m
				break
			}
		}
		p.mu.Unlock()

		if member == nil {
			return 0, E.New("member not found: ", tag)
		}
		// 分层探测：CF 通过后转 apple:80 轻量探测，省流量
		return p.dispatchProbe(ctx, member)
	}
}

// makeReleaseByTagFunc creates a release function that works before member initialization
func (p *poolOutbound) makeReleaseByTagFunc(tag string) func() {
	return func() {
		releaseSharedMember(tag)
	}
}

type trackedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *trackedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

type trackedPacketConn struct {
	net.PacketConn
	once    sync.Once
	release func()
}

func (c *trackedPacketConn) Close() error {
	err := c.PacketConn.Close()
	c.once.Do(c.release)
	return err
}

func (p *poolOutbound) incActive(member *memberState) {
	if member.shared != nil {
		member.shared.incActive()
	}
}

func (p *poolOutbound) decActive(member *memberState) {
	if member.shared != nil {
		member.shared.decActive()
	}
}

func (p *poolOutbound) getCandidateBuffer() []*memberState {
	if buf := p.candidatesPool.Get(); buf != nil {
		return buf.([]*memberState)
	}
	return make([]*memberState, 0, len(p.options.Members))
}

func (p *poolOutbound) putCandidateBuffer(buf []*memberState) {
	if buf == nil {
		return
	}
	const maxCached = 4096
	if cap(buf) > maxCached {
		return
	}
	p.candidatesPool.Put(buf[:0])
}
