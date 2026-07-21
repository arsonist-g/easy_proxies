package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"easy_proxies/internal/accesscontrol"
	"easy_proxies/internal/accesslog"
	"easy_proxies/internal/boxmgr"
	"easy_proxies/internal/config"
	"easy_proxies/internal/geocn"
	"easy_proxies/internal/geoip"
	"easy_proxies/internal/logger"
	"easy_proxies/internal/monitor"
	"easy_proxies/internal/store"
	"easy_proxies/internal/subscription"
	"easy_proxies/internal/virtualpool"
)

// Run builds the runtime components from config and blocks until shutdown.
func Run(ctx context.Context, cfg *config.Config) error {
	// Build monitor config
	proxyUsername := cfg.Pool.Username
	proxyPassword := cfg.Pool.Password
	if cfg.Mode == "multi-port" || cfg.Mode == "hybrid" {
		proxyUsername = cfg.MultiPort.Username
		proxyPassword = cfg.MultiPort.Password
	}

	monitorCfg := monitor.Config{
		Enabled:       cfg.ManagementEnabled(),
		Listen:        cfg.Management.Listen,
		ProbeTarget:   cfg.Management.ProbeTarget,
		Password:      cfg.Management.Password,
		PathPwd:       cfg.Management.PathPwd,
		ProxyUsername: proxyUsername,
		ProxyPassword: proxyPassword,
		ExternalIP:    cfg.ExternalIP,
	}

	// 打开 bbolt 存储（凭证/订阅/探测结果）。在 boxMgr.Start 之前完成，
	// 因为 Start 的初始健康检查会阻塞，期间管理 API 已开始服务——
	// store 必须在 server 创建时就位，否则凭证/订阅等写端点会返回 501。
	dbPath := filepath.Join(filepath.Dir(cfg.FilePath()), "easy.db")
	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open store %s: %w", dbPath, err)
	}
	defer st.Close()

	// 确保凭证加密密钥存在：db 中凭证以 AES-256-GCM 密文存储，密钥放 config.yaml（与 db 文件分离），
	// db 单独泄露时无法解密。密钥纯服务端使用，前端点复制时 plain 接口用它解密返回明文。
	if err := cfg.EnsureCredentialKey(); err != nil {
		return fmt.Errorf("ensure credential key: %w", err)
	}

	// 可选：打开本地 MaxMind GeoLite2-ASN，供节点探测时回填 ASN。未配置则跳过（no-op）。
	if cfg.GeoIP.ASNDatabase != "" {
		updaterOn := cfg.GeoIP.LicenseKey != ""
		// 即时打开已有 DB（无论是否配自动更新），保证 ASN 立即可用。
		if err := geoip.Open(cfg.GeoIP.ASNDatabase); err != nil {
			if updaterOn {
				// 自动更新模式下不致命：后台会下载，期间 ASN 暂不可用
				logger.Warnf("GeoIP 打开 %s 失败: %v（后台将尝试自动下载）", cfg.GeoIP.ASNDatabase, err)
			} else {
				return fmt.Errorf("open geoip: %w", err)
			}
		} else {
			defer geoip.Close()
			logger.Infof("GeoIP ASN database loaded: %s", cfg.GeoIP.ASNDatabase)
		}
		// 配置 license_key 时开后台自动更新：启动立即检查（缺库下载/有库 If-Modified-Since），之后按 Interval 周期
		if updaterOn {
			go geoip.RunUpdater(ctx, geoip.UpdaterConfig{
				AccountID:  cfg.GeoIP.AccountID,
				LicenseKey: cfg.GeoIP.LicenseKey,
				EditionID:  cfg.GeoIP.EditionID,
				DestPath:   cfg.GeoIP.ASNDatabase,
				Interval:   cfg.GeoIP.UpdateInterval,
			})
		}
	}

	// 可选：打开 GeoCN.mmdb（访问控制的省份/运营商判定数据源）。
	// database 路径空 = 不启用 GeoCN（访问控制退化为仅 IP 白名单）。
	// database 非空 + auto_download=true → 文件缺失则后台自动下载（GitHub 免认证直链）并周期更新，下载完热加载。
	// database 非空 + auto_download=false → 只读已有文件不下载（适合挂载自带库；文件缺失则 GeoCN 不就绪）。
	autoDL := cfg.GeoCN.AutoDownload != nil && *cfg.GeoCN.AutoDownload
	if geocnDB := cfg.GeoCN.DatabasePath; geocnDB != "" {
		defer geocn.Close()
		if err := geocn.Open(geocnDB); err != nil {
			if autoDL {
				logger.Warnf("GeoCN 打开 %s 失败: %v（后台将自动下载）", geocnDB, err)
			} else {
				logger.Warnf("GeoCN 打开 %s 失败: %v（auto_download=false，请确认 mmdb 文件已就位）", geocnDB, err)
			}
		} else {
			logger.Infof("GeoCN database loaded: %s", geocnDB)
		}
		if autoDL {
			go geocn.RunUpdater(ctx, geocn.UpdaterConfig{
				DestPath: geocnDB,
				URL:      cfg.GeoCN.DownloadURL,
				Interval: cfg.GeoCN.UpdateInterval,
			})
		}
	} else if cfg.AccessControl.Enabled {
		logger.Warnf("⚠️ 访问控制已启用但未配置 geocn.database_path：省份/ISP/仅中国判定不可用，仅 IP 白名单将生效")
	}

	// 访问控制：从配置构造策略并热换加载（atomic.Pointer，运行时 Check 无锁读）。
	// 省份名已在 config.normalize 阶段归一为标准名；非法 CIDR 在此报错（启动失败，避免错误策略静默生效）。
	policy, err := accesscontrol.Build(accesscontrol.Options{
		Enabled:        cfg.AccessControl.Enabled,
		AllowIPs:       cfg.AccessControl.AllowIPs,
		ChinaOnly:      cfg.AccessControl.ChinaOnly,
		AllowProvinces: cfg.AccessControl.AllowProvinces,
		AllowISPs:      cfg.AccessControl.AllowISPs,
		BlockIDC:       cfg.AccessControl.BlockIDC,
		UnknownISP:     cfg.AccessControl.UnknownISP,
	})
	if err != nil {
		return fmt.Errorf("build access control policy: %w", err)
	}
	accesscontrol.Set(policy)
	if cfg.AccessControl.Enabled {
		logger.Infof("🛡️ 访问控制已启用（china_only=%v, allow_provinces=%v, allow_isps=%v, block_idc=%v）",
			cfg.AccessControl.ChinaOnly, cfg.AccessControl.AllowProvinces, cfg.AccessControl.AllowISPs, cfg.AccessControl.BlockIDC)
	}

	// 访问日志：内存环形缓冲（转发路径不触达 store；重启后清空）。
	if cfg.AccessLog.Enabled {
		accesslog.SetDefault(accesslog.New(cfg.AccessLog.Capacity))
		logger.Infof("📝 访问日志已启用（容量 %d，内存环形）", cfg.AccessLog.Capacity)
	}

	// 同步 config.yaml 与 bbolt 订阅定义：yaml 为权威源，bbolt 保留运行时状态。
	if err := subscription.SyncSubscriptions(cfg, st); err != nil {
		return fmt.Errorf("sync subscriptions: %w", err)
	}

	// Create BoxManager 并提前初始化 monitor（创建 server、注入 store、开始服务）。
	// 必须在 Start 之前完成：Start 的初始健康检查（MinAvailableNodes 默认 1）会阻塞，
	// 期间管理 API 已在服务——若 store/vpMgr 未就位，凭证/虚拟池等端点会返回 501。
	boxMgr := boxmgr.New(cfg, monitorCfg)
	boxMgr.SetStore(st) // ensureMonitor 创建 server 时注入
	if err := boxMgr.PrepareMonitor(ctx); err != nil {
		return fmt.Errorf("prepare monitor: %w", err)
	}
	if server := boxMgr.MonitorServer(); server != nil {
		server.SetConfig(cfg)
	}

	// 装配 VirtualPoolManager（始终创建，即使初始无池，以支持经 API 创建第一个虚拟池）。
	// 在 Start 阻塞前装配；GetMatchingNodes 实时查询 + TTL 缓存，节点在 box 启动后自动纳入。
	vpMgr := virtualpool.NewManager(cfg, boxMgr.MonitorManager())
	if err := vpMgr.Start(); err != nil {
		return fmt.Errorf("start virtual pool manager: %w", err)
	}
	defer vpMgr.Stop()
	if server := boxMgr.MonitorServer(); server != nil {
		server.SetVirtualPoolManager(vpMgr)
	}
	printVirtualPoolLinks(cfg, vpMgr)

	// 启动 sing-box（ensureMonitor 幂等跳过；createBox + 初始健康检查，可能阻塞）。
	if err := boxMgr.Start(ctx); err != nil {
		return fmt.Errorf("start box manager: %w", err)
	}
	defer boxMgr.Close()

	// SubscriptionManager 始终创建并注入（即使 subscription_refresh.enabled=false 或启动时无订阅），
	// 保证运行时经 API 新增订阅后手动/单订阅刷新可用。Manager.Start 内部按 enabled 决定是否定时刷新，
	// 手动刷新信号始终被消费。需在 box 就绪后启动（刷新会触发 reload，依赖 currentBox）。
	subMgr := subscription.New(cfg, boxMgr, subscription.WithStore(st))
	subMgr.Start()
	defer subMgr.Stop()
	if server := boxMgr.MonitorServer(); server != nil {
		server.SetSubscriptionRefresher(subMgr)
	}

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case <-ctx.Done():
	case sig := <-sigCh:
		fmt.Printf("received %s, shutting down\n", sig)
	}

	return nil
}

// printVirtualPoolLinks 输出虚拟池 Entry Points 信息
func printVirtualPoolLinks(cfg *config.Config, vpMgr *virtualpool.Manager) {
	if vpMgr == nil || len(cfg.VirtualPools) == 0 {
		return
	}

	logger.Print("")
	logger.Print("🔮 Virtual Pool Entry Points:")
	logger.Print("───────────────────────────────────────────────────────────────")

	for _, poolCfg := range cfg.VirtualPools {
		pool := vpMgr.GetPool(poolCfg.Name)
		nodeCount := 0
		if pool != nil {
			nodeCount = len(pool.GetMatchingNodes())
		}

		var auth string
		if poolCfg.Username != "" {
			auth = fmt.Sprintf("%s:%s@", poolCfg.Username, poolCfg.Password)
		}
		proxyURL := fmt.Sprintf("http://%s%s:%d", auth, poolCfg.Address, poolCfg.Port)

		logger.Print(fmt.Sprintf("   [%d] %s (nodes: %d, strategy: %s)", poolCfg.Port, poolCfg.Name, nodeCount, poolCfg.Strategy))
		logger.Print(fmt.Sprintf("       %s", proxyURL))
	}

	logger.Print("───────────────────────────────────────────────────────────────")
	logger.Print("")
}
