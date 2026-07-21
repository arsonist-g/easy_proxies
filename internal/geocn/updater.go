// updater.go — GeoCN.mmdb 自动更新(免认证 GitHub 直链,无需 MaxMind 账号)。
//
// 与 internal/geoip 的 MaxMind 更新器相比大幅简化:
//   - 无 Basic Auth(releases/latest/download 是公开永久直链)
//   - 无 tar.gz 解压(响应体直接就是 .mmdb)
//   - GitHub 此永久链接恒 302→200,不支持 If-Modified-Since 协商,故按周期全量覆盖
//
// 健壮性:下载体先落临时文件、用 maxminddb.Open 校验完整性后才原子替换并热加载;
// 校验失败保留旧库,避免坏库破坏访问控制判定。与 [Open] 共用单例,热替换对并发 Lookup 安全。
package geocn

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"easy_proxies/internal/logger"

	"github.com/oschwald/maxminddb-golang/v2"
)

// UpdaterConfig GeoCN 自动更新配置。
type UpdaterConfig struct {
	DestPath string        // 本地落地 .mmdb 路径(必填)
	URL      string        // 下载地址,默认 GitHub releases/latest 永久直链
	Interval time.Duration // 检查间隔,默认 24h
}

const (
	defaultGeoCNURL = "https://github.com/ljxi/GeoCN/releases/latest/download/GeoCN.mmdb"
	defaultInterval = 24 * time.Hour
	downloadTimeout = 2 * time.Minute
	maxMMDBSize     = 128 << 20 // 128MiB 读取上限(GeoCN 实际 ~9MB,留余量防异常响应)
)

func (c UpdaterConfig) normalized() UpdaterConfig {
	if c.URL == "" {
		c.URL = defaultGeoCNURL
	}
	if c.Interval <= 0 {
		c.Interval = defaultInterval
	}
	return c
}

// RunUpdater 后台周期下载并热替换 reader。阻塞至 ctx 取消。
// 启动立即下载一次(缺库/首次安装),之后按 Interval 周期全量覆盖。
// 任一轮失败仅记日志,保持旧库继续服务,下一轮重试。
func RunUpdater(ctx context.Context, cfg UpdaterConfig) {
	cfg = cfg.normalized()
	if cfg.DestPath == "" {
		return
	}
	if err := checkAndUpdate(ctx, cfg); err != nil {
		logger.Warnf("GeoCN 初始更新失败: %v", err)
	}
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := checkAndUpdate(ctx, cfg); err != nil {
				logger.Warnf("GeoCN 更新失败: %v", err)
			}
		}
	}
}

// checkAndUpdate 下载→校验→原子写→热替换。
func checkAndUpdate(ctx context.Context, cfg UpdaterConfig) error {
	dctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(dctx, http.MethodGet, cfg.URL, nil)
	if err != nil {
		return fmt.Errorf("构建请求: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("下载: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载: %s", resp.Status)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxMMDBSize))
	if err != nil {
		return fmt.Errorf("读取响应: %w", err)
	}
	if err := validateAndSwap(cfg.DestPath, data); err != nil {
		return err
	}
	logger.Infof("GeoCN 数据库已更新 (%d 字节)", len(data))
	return nil
}

// validateAndSwap 把下载内容写到临时文件、校验可被 maxminddb 打开,再原子替换并热加载。
// 校验失败删除临时文件、保留旧库,避免坏库覆盖可用库导致访问控制误判。
func validateAndSwap(destPath string, data []byte) error {
	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建目录: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".geocn-tmp-*")
	if err != nil {
		return fmt.Errorf("创建临时文件: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // 重命名成功后 tmpName 已不存在,Remove 无副作用

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("写入临时文件: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// 校验完整性:坏库会破坏访问控制判定,故下载后先验证再替换
	if db, err := maxminddb.Open(tmpName); err != nil {
		return fmt.Errorf("校验新库失败(保留旧库): %w", err)
	} else {
		_ = db.Close()
	}
	if err := os.Rename(tmpName, destPath); err != nil {
		return fmt.Errorf("替换 %s: %w", destPath, err)
	}
	// 热替换 reader(Open 会关闭旧 reader;并发 Lookup 由内部 RWMutex 保护)
	if err := Open(destPath); err != nil {
		return fmt.Errorf("更新后重开失败: %w", err)
	}
	return nil
}
