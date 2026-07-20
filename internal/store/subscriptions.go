package store

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	bbolt "github.com/sagernet/bbolt"
)

// CreateSubscription 创建订阅记录，维护 by_url 索引防重复 URL。
func (s *Store) CreateSubscription(name, url, subType string) (*Subscription, error) {
	existing, err := s.FindSubscriptionByURL(url)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, ErrConflict
	}

	sub := &Subscription{
		Name:              name,
		URL:               url,
		Type:              subType,
		LastRefreshStatus: SubStatusNever,
		CreatedAt:         now(),
		UpdatedAt:         now(),
	}
	err = s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(BucketSubscriptions))
		id, err := b.NextSequence()
		if err != nil {
			return err
		}
		sub.ID = id
		if err := putJSON(b, itob(id), sub); err != nil {
			return err
		}
		idx := tx.Bucket([]byte(BucketSubscriptionsByURL))
		return idx.Put([]byte(hashURL(url)), itob(id))
	})
	if err != nil {
		return nil, err
	}
	return sub, nil
}

// GetSubscription 按 id 读取订阅。
func (s *Store) GetSubscription(id uint64) (*Subscription, error) {
	var sub Subscription
	found, err := s.getView(BucketSubscriptions, itob(id), &sub)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrNotFound
	}
	return &sub, nil
}

// ListSubscriptions 列出全部订阅。
func (s *Store) ListSubscriptions() ([]Subscription, error) {
	var list []Subscription
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(BucketSubscriptions))
		return b.ForEach(func(_, v []byte) error {
			var sub Subscription
			if err := jsonUnmarshal(v, &sub); err != nil {
				return err
			}
			list = append(list, sub)
			return nil
		})
	})
	return list, err
}

// UpdateSubscription 更新订阅（状态/名称等）。
func (s *Store) UpdateSubscription(sub *Subscription) error {
	if sub == nil || sub.ID == 0 {
		return errors.New("subscription id required")
	}
	sub.UpdatedAt = now()
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(BucketSubscriptions))
		return putJSON(b, itob(sub.ID), sub)
	})
}

// DeleteSubscription 删除订阅及其 by_url 索引。
func (s *Store) DeleteSubscription(id uint64) error {
	var sub Subscription
	found, _ := s.getView(BucketSubscriptions, itob(id), &sub)
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(BucketSubscriptions))
		if err := b.Delete(itob(id)); err != nil {
			return err
		}
		if found && sub.URL != "" {
			idx := tx.Bucket([]byte(BucketSubscriptionsByURL))
			return idx.Delete([]byte(hashURL(sub.URL)))
		}
		return nil
	})
}

// FindSubscriptionByURL 按 URL 反查（防重复添加）。
func (s *Store) FindSubscriptionByURL(url string) (*Subscription, error) {
	var idBytes []byte
	err := s.db.View(func(tx *bbolt.Tx) error {
		v := tx.Bucket([]byte(BucketSubscriptionsByURL)).Get([]byte(hashURL(url)))
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
	if len(idBytes) != 8 {
		return nil, fmt.Errorf("invalid subscription index value")
	}
	return s.GetSubscription(binary.BigEndian.Uint64(idBytes))
}

func hashURL(url string) string {
	h := sha256.Sum256([]byte(url))
	return hex.EncodeToString(h[:])
}

// SubscriptionEntries 把订阅列表编码为 config.yaml 的 []string 条目（"name:url"，无 name 则裸 url）。
// 供 config.yaml 与 bbolt 双向同步：订阅定义落 yaml 供用户编辑，运行时状态留 bbolt。
func SubscriptionEntries(subs []Subscription) []string {
	entries := make([]string, 0, len(subs))
	for _, s := range subs {
		name := strings.TrimSpace(s.Name)
		url := strings.TrimSpace(s.URL)
		if name == "" {
			entries = append(entries, url)
		} else {
			entries = append(entries, name+":"+url)
		}
	}
	return entries
}
