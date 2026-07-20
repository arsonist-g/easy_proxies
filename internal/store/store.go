// Package store 封装 bbolt KV 存储，管理低频管理数据
//（订阅 / API Key / 订阅 token / 节点探测结果）。
// 代理转发路径不触达本包（ADR-0007：转发只走 sing-box + 内存 atomic 计数）。
package store

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	bbolt "github.com/sagernet/bbolt"
)

// 哨兵错误。
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

// 桶名（data-model §2.1）。
const (
	BucketAPIKeys            = "api_keys"
	BucketAPIKeysByHash      = "api_keys_by_hash"
	BucketSubTokens          = "sub_tokens"
	BucketSubTokensByHash    = "sub_tokens_by_hash"
	BucketSubscriptions      = "subscriptions"
	BucketSubscriptionsByURL = "subscriptions_by_url"
	BucketNodeProbe          = "node_probe"
)

var allBuckets = []string{
	BucketAPIKeys, BucketAPIKeysByHash,
	BucketSubTokens, BucketSubTokensByHash,
	BucketSubscriptions, BucketSubscriptionsByURL,
	BucketNodeProbe,
}

// Store 封装 bbolt 句柄。
type Store struct {
	db *bbolt.DB
}

// Open 打开/创建 bbolt 文件并确保所有桶存在。
func Open(path string) (*Store, error) {
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: 3 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open bbolt %s: %w", path, err)
	}
	s := &Store{db: db}
	if err := s.ensureBuckets(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close 关闭底层 bbolt。
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) ensureBuckets() error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		for _, b := range allBuckets {
			if _, err := tx.CreateBucketIfNotExists([]byte(b)); err != nil {
				return fmt.Errorf("create bucket %s: %w", b, err)
			}
		}
		return nil
	})
}

// View 暴露只读事务。
func (s *Store) View(fn func(tx *bbolt.Tx) error) error { return s.db.View(fn) }

// Update 暴露读写事务。
func (s *Store) Update(fn func(tx *bbolt.Tx) error) error { return s.db.Update(fn) }

// NextSequence 在主桶生成下一个 uint64 ID（有序）。
func (s *Store) NextSequence(bucket string) (uint64, error) {
	var id uint64
	err := s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return fmt.Errorf("bucket %s not found", bucket)
		}
		seq, err := b.NextSequence()
		if err != nil {
			return err
		}
		id = seq
		return nil
	})
	return id, err
}

// --- 通用 JSON KV 助手 ---

// itob 将 uint64 编码为 8 字节 big-endian（bbolt 有序键）。
func itob(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}

// putJSON 序列化 val 存入 bucket[key]。
func putJSON(b *bbolt.Bucket, key []byte, val any) error {
	data, err := json.Marshal(val)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return b.Put(key, data)
}

// getJSON 从 bucket[key] 反序列化到 ptr，返回是否命中。
func getJSON(b *bbolt.Bucket, key []byte, ptr any) (bool, error) {
	data := b.Get(key)
	if data == nil {
		return false, nil
	}
	if err := json.Unmarshal(data, ptr); err != nil {
		return false, fmt.Errorf("unmarshal: %w", err)
	}
	return true, nil
}

// getView 在只读事务里从 bucket[key] 读取单条记录。
func (s *Store) getView(bucket string, key []byte, ptr any) (bool, error) {
	var found bool
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return fmt.Errorf("bucket %s not found", bucket)
		}
		f, err := getJSON(b, key, ptr)
		found = f
		return err
	})
	return found, err
}

// jsonUnmarshal 是 json.Unmarshal 的包内别名，保持调用简洁。
func jsonUnmarshal(data []byte, ptr any) error { return json.Unmarshal(data, ptr) }

// now 返回当前 UTC 时间（包内统一入口）。
func now() time.Time { return time.Now().UTC() }
