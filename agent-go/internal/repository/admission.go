package repository

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"sync"
	"time"
)

type ThreadLease struct {
	Context context.Context
	Release func() error
}

var ErrThreadBusy = errors.New("thread already has an active run")

// AcquireThread serializes history writers across processes. PostgreSQL releases
// this session lock on worker death; a stale worker cannot keep a lock forever.
func (s *PostgresStore) AcquireThread(ctx context.Context, id string) (ThreadLease, error) {
	// Keep admission locks off the query pool: otherwise N active runs could
	// exhaust all N connections while each waits for a metadata query.
	conn, err := pgx.ConnectConfig(ctx, s.pool.Native.Config().ConnConfig.Copy())
	if err != nil {
		return ThreadLease{}, err
	}
	var acquired bool
	err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 572643))`, id).Scan(&acquired)
	if err != nil || !acquired {
		_ = conn.Close(context.WithoutCancel(ctx))
		if err != nil {
			return ThreadLease{}, err
		}
		return ThreadLease{}, ErrThreadBusy
	}
	owned, cancel := context.WithCancel(context.WithoutCancel(ctx))
	stopPoll := make(chan struct{})
	pollDone := make(chan struct{})
	go func() {
		defer close(pollDone)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopPoll:
				return
			case <-ticker.C:
				probe, stop := context.WithTimeout(owned, 2*time.Second)
				err := conn.Ping(probe)
				stop()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	var once sync.Once
	var releaseErr error
	release := func() error { //nolint:contextcheck // cleanup closes the independent admission session after request cancellation
		once.Do(func() {
			close(stopPoll)
			<-pollDone
			cancel()
			bounded, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			var unlocked bool
			releaseErr = conn.QueryRow(bounded, `SELECT pg_advisory_unlock(hashtextextended($1, 572643))`, id).Scan(&unlocked)
			if releaseErr == nil && !unlocked {
				releaseErr = errors.New("thread lease was lost")
			}
			releaseErr = errors.Join(releaseErr, conn.Close(bounded))
		})
		return releaseErr
	}
	return ThreadLease{Context: owned, Release: release}, nil
}

func (s *MemoryStore) AcquireThread(_ context.Context, id string) (ThreadLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeThreads == nil {
		s.activeThreads = map[string]bool{}
	}
	if s.activeThreads[id] {
		return ThreadLease{}, ErrThreadBusy
	}
	s.activeThreads[id] = true
	owned, cancel := context.WithCancel(context.Background())
	var once sync.Once
	return ThreadLease{Context: owned, Release: func() error {
		once.Do(func() { s.mu.Lock(); delete(s.activeThreads, id); s.mu.Unlock(); cancel() })
		return nil
	}}, nil
}
