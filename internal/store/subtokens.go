package store

import (
	"encoding/binary"
	"errors"

	bbolt "github.com/sagernet/bbolt"
)

// SaveSubscribeToken 存储新订阅 token，分配 id + 维护 by_hash 索引。
func (s *Store) SaveSubscribeToken(t *SubscribeToken) error {
	if t == nil {
		return errors.New("nil subscribe token")
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(BucketSubTokens))
		id, err := b.NextSequence()
		if err != nil {
			return err
		}
		t.ID = id
		if t.CreatedAt.IsZero() {
			t.CreatedAt = now()
		}
		if err := putJSON(b, itob(id), t); err != nil {
			return err
		}
		idx := tx.Bucket([]byte(BucketSubTokensByHash))
		return idx.Put([]byte(t.TokenHash), itob(id))
	})
}

// GetSubscribeToken 按 id 读取。
func (s *Store) GetSubscribeToken(id uint64) (*SubscribeToken, error) {
	var t SubscribeToken
	found, err := s.getView(BucketSubTokens, itob(id), &t)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrNotFound
	}
	return &t, nil
}

// ListSubscribeTokens 列出全部订阅 token。
func (s *Store) ListSubscribeTokens() ([]SubscribeToken, error) {
	var list []SubscribeToken
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(BucketSubTokens))
		return b.ForEach(func(_, v []byte) error {
			var t SubscribeToken
			if err := jsonUnmarshal(v, &t); err != nil {
				return err
			}
			list = append(list, t)
			return nil
		})
	})
	return list, err
}

// UpdateSubscribeToken 更新（吊销时间/最近使用时间/过滤）。
func (s *Store) UpdateSubscribeToken(t *SubscribeToken) error {
	if t == nil || t.ID == 0 {
		return errors.New("subscribe token id required")
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(BucketSubTokens))
		return putJSON(b, itob(t.ID), t)
	})
}

// DeleteSubscribeToken 删除订阅 token 及其 by_hash 索引。
func (s *Store) DeleteSubscribeToken(id uint64) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(BucketSubTokens))
		data := b.Get(itob(id))
		if data == nil {
			return ErrNotFound
		}
		var t SubscribeToken
		if err := jsonUnmarshal(data, &t); err == nil && t.TokenHash != "" {
			_ = tx.Bucket([]byte(BucketSubTokensByHash)).Delete([]byte(t.TokenHash))
		}
		return b.Delete(itob(id))
	})
}

// FindSubscribeTokenByHash 鉴权反查：明文 token → sha256 hex → id → 记录。
func (s *Store) FindSubscribeTokenByHash(plainToken string) (*SubscribeToken, error) {
	hash := HashSecret(plainToken)
	var idBytes []byte
	err := s.db.View(func(tx *bbolt.Tx) error {
		v := tx.Bucket([]byte(BucketSubTokensByHash)).Get([]byte(hash))
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
	return s.GetSubscribeToken(binary.BigEndian.Uint64(idBytes))
}
