// Package postgres owns the shared GORM/PostgreSQL connection and SQL boundary.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type DB struct {
	ORM      *gorm.DB
	Native   *pgxpool.Pool
	sql      *sql.DB
	ownsPool bool
}
type Result struct{ affected int64 }

func (r Result) RowsAffected() int64 { return r.affected }

func Open(ctx context.Context, url string) (*DB, error) {
	if url == "" {
		return nil, nil
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("postgres: configuration: %w", err)
	}
	db, err := FromPool(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	db.ownsPool = true
	check, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.sql.PingContext(check); err != nil {
		db.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return db, nil
}

// FromPool shares the native pool with GORM. Native transaction adapters use
// this same pool for PostgreSQL-specific append logs and advisory locking.
func FromPool(pool *pgxpool.Pool) (*DB, error) {
	if pool == nil {
		return nil, errors.New("postgres: pool is nil")
	}
	db, err := FromSQL(stdlib.OpenDBFromPool(pool))
	if err != nil {
		return nil, err
	}
	db.Native = pool
	return db, nil
}

// FromSQL is also the injection boundary for database/sql contract tests.
func FromSQL(connection *sql.DB) (*DB, error) {
	if connection == nil {
		return nil, errors.New("postgres: connection is nil")
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: connection}), &gorm.Config{DisableAutomaticPing: true, SkipDefaultTransaction: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	return &DB{ORM: orm, sql: connection}, nil
}
func (db *DB) Close() {
	if db == nil {
		return
	}
	if db.sql != nil {
		_ = db.sql.Close()
	}
	if db.Native != nil && db.ownsPool {
		db.Native.Close()
	}
}
func (db *DB) Exec(ctx context.Context, query string, args ...any) (Result, error) {
	result := db.ORM.WithContext(ctx).Exec("?", queryExpression{SQL: query, Args: args})
	return Result{affected: result.RowsAffected}, result.Error
}
func (db *DB) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return db.ORM.WithContext(ctx).Raw("?", queryExpression{SQL: query, Args: args}).Rows()
}

// Row preserves a single-row Scan contract while returning GORM build and
// context errors directly, including errors that would otherwise yield a nil sql.Row.
type Row struct {
	rows *sql.Rows
	err  error
}

func (r *Row) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	defer r.rows.Close()
	if !r.rows.Next() {
		if err := r.rows.Err(); err != nil {
			return err
		}
		return sql.ErrNoRows
	}
	return r.rows.Scan(dest...)
}
func (db *DB) QueryRow(ctx context.Context, query string, args ...any) *Row {
	rows, err := db.Query(ctx, query, args...)
	return &Row{rows: rows, err: err}
}

// Transaction keeps the same request context and commits only a successful batch.
func (db *DB) Transaction(ctx context.Context, fn func(*DB) error) error {
	return db.ORM.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return fn(&DB{ORM: tx}) })
}
