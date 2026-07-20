package store

import (
	"errors"
	"fmt"

	bbolt "github.com/sagernet/bbolt"
)

// UpsertNodeProbe 写入/更新探测结果（key = stable_id，非 sequence）。
func (s *Store) UpsertNodeProbe(p *NodeProbe) error {
	if p == nil || p.NodeID == "" {
		return errors.New("node_id required")
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(BucketNodeProbe))
		if b == nil {
			return fmt.Errorf("bucket %s not found", BucketNodeProbe)
		}
		return putJSON(b, []byte(p.NodeID), p)
	})
}

// GetNodeProbe 按 stable_id 读取探测结果。
func (s *Store) GetNodeProbe(stableID string) (*NodeProbe, error) {
	var p NodeProbe
	found, err := s.getView(BucketNodeProbe, []byte(stableID), &p)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrNotFound
	}
	return &p, nil
}

// ListNodeProbes 列出全部探测结果（country/去重查询用）。
func (s *Store) ListNodeProbes() ([]NodeProbe, error) {
	var list []NodeProbe
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(BucketNodeProbe))
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, v []byte) error {
			var p NodeProbe
			if err := jsonUnmarshal(v, &p); err != nil {
				return err
			}
			list = append(list, p)
			return nil
		})
	})
	return list, err
}
