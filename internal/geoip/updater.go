// updater.go — MaxMind GeoLite2 自动更新。
//
// 配置 geoip.license_key + geoip.account_id 后，后台周期检查并热替换本地 .mmdb：
//   - 库缺失 → 全量下载（首次安装 / DB 丢失）
//   - 库存在 → 带 If-Modified-Since 请求；MaxMind 返回 304 跳过，200 才下载
//
// 下载走官方直链 permalink（Basic Auth AccountID:LicenseKey），302 跳转到 R2 presigned
// URL（Go http.Client 默认跟随重定向，且会在跨域跳转时自动剥离 Authorization —— R2
// presigned 不需要鉴权，故默认行为正合适）。下载体为 tar.gz，解压出 *.mmdb 原子落盘后
// 调 Open 热替换 reader。与 [Open] 共用单例，热替换对并发 LookupASN 安全。
package geoip

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"easy_proxies/internal/logger"
)

// UpdaterConfig MaxMind GeoLite2 自动更新配置。
type UpdaterConfig struct {
	AccountID  string        // MaxMind Account ID（Basic Auth 用户名）
	LicenseKey string        // MaxMind License Key（Basic Auth 密码）
	EditionID  string        // 数据库 edition，默认 GeoLite2-ASN
	DestPath   string        // 本地落地 .mmdb 路径
	Interval   time.Duration // 检查间隔，默认 24h（MaxMind 每周二更新）
}

const (
	defaultEdition  = "GeoLite2-ASN"
	defaultInterval = 24 * time.Hour
	downloadTimeout = 2 * time.Minute
	maxMMDBSize     = 256 << 20 // 单成员读取上限 256MiB，防异常归档耗内存
)

func (c UpdaterConfig) normalized() UpdaterConfig {
	if c.EditionID == "" {
		c.EditionID = defaultEdition
	}
	if c.Interval <= 0 {
		c.Interval = defaultInterval
	}
	return c
}

// downloadURL MaxMind 直链下载 permalink（302 → R2 presigned）。
func (c UpdaterConfig) downloadURL() string {
	return fmt.Sprintf("https://download.maxmind.com/geoip/databases/%s/download?suffix=tar.gz", c.EditionID)
}

// RunUpdater 后台周期检查更新并热替换 reader。阻塞至 ctx 取消。
// 启动立即检查一次（处理缺库 / 上次未完成更新），之后按 Interval 周期检查。
// 任一轮失败仅记日志，保持旧库继续服务，下一轮重试。
func RunUpdater(ctx context.Context, cfg UpdaterConfig) {
	cfg = cfg.normalized()
	if cfg.LicenseKey == "" || cfg.DestPath == "" {
		return
	}
	if err := checkAndUpdate(ctx, cfg); err != nil {
		logger.Warnf("GeoIP 初始更新检查失败: %v", err)
	}
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := checkAndUpdate(ctx, cfg); err != nil {
				logger.Warnf("GeoIP 更新检查失败: %v", err)
			}
		}
	}
}

// checkAndUpdate 单次检查：缺库全量下，有库走 If-Modified-Since。
func checkAndUpdate(ctx context.Context, cfg UpdaterConfig) error {
	return fetchAndSwap(ctx, cfg, !fileExists(cfg.DestPath))
}

// fetchAndSwap 下载→解压→原子写→热替换。
// full=true：无条件全量下载（缺库）。full=false：带 If-Modified-Since，304 跳过。
func fetchAndSwap(ctx context.Context, cfg UpdaterConfig, full bool) error {
	dctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(dctx, http.MethodGet, cfg.downloadURL(), nil)
	if err != nil {
		return fmt.Errorf("构建请求: %w", err)
	}
	req.SetBasicAuth(cfg.AccountID, cfg.LicenseKey)
	if !full {
		if mtime, ok := fileModTime(cfg.DestPath); ok {
			req.Header.Set("If-Modified-Since", mtime.UTC().Format(http.TimeFormat))
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("下载: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotModified:
		return nil // 库未变更
	case http.StatusOK:
		// 继续
	default:
		return fmt.Errorf("下载: %s", resp.Status)
	}

	data, err := extractMMDB(resp.Body)
	if err != nil {
		return fmt.Errorf("解压 mmdb: %w", err)
	}
	if err := atomicWrite(cfg.DestPath, data); err != nil {
		return fmt.Errorf("写入 %s: %w", cfg.DestPath, err)
	}
	// 热替换 reader（Open 会关闭旧 reader；并发 LookupASN 由内部 RWMutex 保护）
	if err := Open(cfg.DestPath); err != nil {
		return fmt.Errorf("更新后重开失败: %w", err)
	}
	logger.Infof("GeoIP 已更新: %s (%d 字节)", cfg.EditionID, len(data))
	return nil
}

// extractMMDB 从 MaxMind tar.gz 响应中提取 .mmdb 文件内容。
// tar 结构：{Edition}_{YYYYMMDD}/{Edition}.mmdb + LICENSE.txt + COPYRIGHT.txt；
// 按 .mmdb 后缀匹配，忽略日期目录前缀，兼容任意 edition 命名。
func extractMMDB(r io.Reader) ([]byte, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, errors.New("归档内未找到 .mmdb")
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg || !strings.HasSuffix(hdr.Name, ".mmdb") {
			continue
		}
		return io.ReadAll(io.LimitReader(tr, maxMMDBSize))
	}
}

// atomicWrite 先写同目录临时文件再 rename（同文件系统原子）。
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".mmdb-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // 重命名成功后 tmpName 已不存在，Remove 无副作用

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func fileModTime(p string) (time.Time, bool) {
	fi, err := os.Stat(p)
	if err != nil {
		return time.Time{}, false
	}
	return fi.ModTime(), true
}
