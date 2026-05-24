package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"easy_proxies/internal/app"
	"easy_proxies/internal/config"
	"easy_proxies/internal/logger"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	logger.Init(cfg.LogLevel)
	defer logger.Sync()

	if err := app.Run(context.Background(), cfg); err != nil {
		logger.Errorf("run app: %v", err)
		os.Exit(1)
	}
}
