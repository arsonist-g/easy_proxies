package store

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"

	bbolt "github.com/sagernet/bbolt"
)

// SaveAPIKey 存储新 API Key（KeyHash/KeyPrefix 由调用方计算），分配 id + 维护 by_hash 索引。
func (s *Store) SaveAPIKey(k *APIKey) error {
	if k == nil {
		return errors.New("nil api key")
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(BucketAPIKeys))
		id, err := b.NextSequence()
		if err != nil {
			return err
		}
		k.ID = id
		if k.CreatedAt.IsZero() {
			k.CreatedAt = now()
		}
		if err := putJSON(b, itob(id), k); err != nil {
			return err
		}
		idx := tx.Bucket([]byte(BucketAPIKeysByHash))
		return idx.Put([]byte(k.KeyHash), itob(id))
	})
}

// GetAPIKey 按 id 读取。
func (s *Store) GetAPIKey(id uint64) (*APIKey, error) {
	var k APIKey
	found, err := s.getView(BucketAPIKeys, itob(id), &k)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrNotFound
	}
	return &k, nil
}

// ListAPIKeys 列出全部 API Key（不含明文，明文从不存储）。
func (s *Store) ListAPIKeys() ([]APIKey, error) {
	var list []APIKey
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(BucketAPIKeys))
		return b.ForEach(func(_, v []byte) error {
			var k APIKey
			if err := jsonUnmarshal(v, &k); err != nil {
				return err
			}
			list = append(list, k)
			return nil
		})
	})
	return list, err
}

// UpdateAPIKey 更新（吊销时间/最近使用时间/scope）。
func (s *Store) UpdateAPIKey(k *APIKey) error {
	if k == nil || k.ID == 0 {
		return errors.New("api key id required")
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(BucketAPIKeys))
		return putJSON(b, itob(k.ID), k)
	})
}

// DeleteAPIKey 删除 API Key 及其 by_hash 索引。
func (s *Store) DeleteAPIKey(id uint64) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(BucketAPIKeys))
		data := b.Get(itob(id))
		if data == nil {
			return ErrNotFound
		}
		var k APIKey
		if err := jsonUnmarshal(data, &k); err == nil && k.KeyHash != "" {
			_ = tx.Bucket([]byte(BucketAPIKeysByHash)).Delete([]byte(k.KeyHash))
		}
		return b.Delete(itob(id))
	})
}

// FindAPIKeyByHash 鉴权反查：明文 key → sha256 hex → id → 记录。
func (s *Store) FindAPIKeyByHash(plainKey string) (*APIKey, error) {
	hash := HashSecret(plainKey)
	var idBytes []byte
	err := s.db.View(func(tx *bbolt.Tx) error {
		v := tx.Bucket([]byte(BucketAPIKeysByHash)).Get([]byte(hash))
		if v == nil {
			return nil
		}
		idBytes = append([]byte(nil), v...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if idBytes == nil {
		return nil, nil
	}
	return s.GetAPIKey(binary.BigEndian.Uint64(idBytes))
}

// HashSecret 计算 sha256 hex（API Key / 订阅 token 共用）。
func HashSecret(plain string) string {
	h := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(h[:])
}
