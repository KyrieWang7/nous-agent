package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	platformdb "github.com/KyrieWang7/nous-agent/platform-go/postgres"
)

// RegistryBackend keeps cross-instance cancel intent durable. Polling is scoped
// to one process identity and repeats until the owner confirms termination.
// Cache eviction or a missed Pub/Sub signal therefore cannot lose cancellation.
type RegistryBackend struct {
	db    *platformdb.DB
	owner string
}

func NewRegistryBackend(db *platformdb.DB, owner string) *RegistryBackend {
	return &RegistryBackend{db: db, owner: owner}
}
func (s *RegistryBackend) Save(ctx context.Context, rec runtime.RunRecord, ttl time.Duration) error {
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO agent_run_registry(run_id,owner,payload,expires_at) VALUES($1,$2,$3,NOW()+$4::interval) ON CONFLICT(run_id) DO UPDATE SET payload=EXCLUDED.payload,expires_at=EXCLUDED.expires_at WHERE agent_run_registry.owner=EXCLUDED.owner`, rec.RunID, rec.Owner, raw, fmt.Sprintf("%f seconds", ttl.Seconds()))
	return err
}
func (s *RegistryBackend) Load(ctx context.Context, id string) (runtime.RunRecord, error) {
	var raw []byte
	err := s.db.QueryRow(ctx, `SELECT payload FROM agent_run_registry WHERE run_id=$1 AND expires_at>NOW()`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.RunRecord{}, runtime.ErrRunNotFound
	}
	if err != nil {
		return runtime.RunRecord{}, err
	}
	var rec runtime.RunRecord
	err = json.Unmarshal(raw, &rec)
	return rec, err
}
func (s *RegistryBackend) Delete(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM agent_run_registry WHERE run_id=$1`, id)
	return err
}
func (s *RegistryBackend) BroadcastCancel(ctx context.Context, id string) error {
	tag, err := s.db.Exec(ctx, `UPDATE agent_run_registry SET cancel_requested=TRUE WHERE run_id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return runtime.ErrRunNotFound
	}
	return nil
}
func (s *RegistryBackend) SubscribeCancel(ctx context.Context) (<-chan string, func(), error) {
	owned, cancel := context.WithCancel(ctx)
	out := make(chan string, 64)
	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { cancel(); <-done }) }
	go func() {
		defer close(done)
		defer close(out)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-owned.Done():
				return
			case <-ticker.C:
				rows, err := s.db.Query(owned, `SELECT run_id FROM agent_run_registry WHERE owner=$1 AND cancel_requested AND expires_at>NOW() AND payload->>'status'='running'`, s.owner)
				if err != nil {
					continue
				}
				for rows.Next() {
					var id string
					if err = rows.Scan(&id); err != nil {
						break
					}
					select {
					case out <- id:
					case <-owned.Done():
						_ = rows.Close()
						return
					}
				}
				_ = rows.Close()
			}
		}
	}()
	return out, stop, nil
}

var _ runtime.RegistryBackend = (*RegistryBackend)(nil)
