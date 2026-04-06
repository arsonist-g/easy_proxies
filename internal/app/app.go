package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"easy_proxies/internal/boxmgr"
	"easy_proxies/internal/config"
	"easy_proxies/internal/logger"
	"easy_proxies/internal/monitor"
	"easy_proxies/internal/subscription"
	"easy_proxies/internal/virtualpool"
)

// Run builds the runtime components from config and blocks until shutdown.
func Run(ctx context.Context, cfg *config.Config) error {
	// Build monitor config
	proxyUsername := cfg.Listener.Username
	proxyPassword := cfg.Listener.Password
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

	// Create and start BoxManager
	boxMgr := boxmgr.New(cfg, monitorCfg)
	if err := boxMgr.Start(ctx); err != nil {
		return fmt.Errorf("start box manager: %w", err)
	}
	defer boxMgr.Close()

	// Wire up config to monitor server for settings API
	if server := boxMgr.MonitorServer(); server != nil {
		server.SetConfig(cfg)
	}

	// Create and start SubscriptionManager if enabled
	var subMgr *subscription.Manager
	if cfg.SubscriptionRefresh.Enabled && len(cfg.Subscriptions) > 0 {
		subMgr = subscription.New(cfg, boxMgr)
		subMgr.Start()
		defer subMgr.Stop()

		// Wire up subscription manager to monitor server for API endpoints
		if server := boxMgr.MonitorServer(); server != nil {
			server.SetSubscriptionRefresher(subMgr)
		}
	}

	// Create and start VirtualPoolManager if configured
	var vpMgr *virtualpool.Manager
	if len(cfg.VirtualPools) > 0 {
		vpMgr = virtualpool.NewManager(cfg, boxMgr.MonitorManager())
		if err := vpMgr.Start(); err != nil {
			return fmt.Errorf("start virtual pool manager: %w", err)
		}
		defer vpMgr.Stop()

		// Wire up virtual pool manager to monitor server for API endpoints
		if server := boxMgr.MonitorServer(); server != nil {
			server.SetVirtualPoolManager(vpMgr)
		}

		// 输出虚拟池 Entry Points 信息
		printVirtualPoolLinks(cfg, vpMgr)
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
