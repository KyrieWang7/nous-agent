package bootstrap

import (
	"context"
	"path/filepath"
	"sync"

	repository "github.com/KyrieWang7/nous-agent/manager/manager_service/internal/repository/ideas"
	"github.com/KyrieWang7/nous-agent/platform-go/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
	"gorm.io/gorm"
)

func newDatabase(lc fx.Lifecycle, cfg Config) (*gorm.DB, error) {
	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	db, err := postgres.FromPool(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	var once sync.Once
	closeDB := func() { once.Do(func() { db.Close(); pool.Close() }) }
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := pool.Ping(ctx); err != nil {
				closeDB()
				return err
			}
			if err := repository.Migrate(ctx, db.ORM, filepath.Join(cfg.Workspace, "outputs", "_ideas.json")); err != nil {
				closeDB()
				return err
			}
			return nil
		},
		OnStop: func(context.Context) error { closeDB(); return nil },
	})
	return db.ORM, nil
}
