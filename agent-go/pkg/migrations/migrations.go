// Package migrations exposes explicit schema migration commands. Agentd never
// invokes these automatically.
package migrations

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

func Open(databaseURL, dir string) (*migrate.Migrate, error) {
	if databaseURL == "" {
		return nil, errors.New("migrations: database URL is required")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	source := (&url.URL{Scheme: "file", Path: abs}).String()
	m, err := migrate.New(source, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("migrations: open: %w", err)
	}
	return m, nil
}
func Up(databaseURL, dir string) error {
	m, err := Open(databaseURL, dir)
	if err != nil {
		return err
	}
	defer closeMigrate(m)
	err = m.Up()
	if errors.Is(err, migrate.ErrNoChange) {
		return nil
	}
	return err
}
func Down(databaseURL, dir string, steps int) error {
	m, err := Open(databaseURL, dir)
	if err != nil {
		return err
	}
	defer closeMigrate(m)
	if steps <= 0 {
		steps = 1
	}
	err = m.Steps(-steps)
	if errors.Is(err, migrate.ErrNoChange) {
		return nil
	}
	return err
}
func Version(databaseURL, dir string) (uint, bool, error) {
	m, err := Open(databaseURL, dir)
	if err != nil {
		return 0, false, err
	}
	defer closeMigrate(m)
	v, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	return v, dirty, err
}

// Force resets the recorded migration version without executing SQL. It is
// intended only for recovering a dirty version after the failed migration has
// been made idempotent and reviewed.
func Force(databaseURL, dir string, version int) error {
	m, err := Open(databaseURL, dir)
	if err != nil {
		return err
	}
	defer closeMigrate(m)
	return m.Force(version)
}

func closeMigrate(m *migrate.Migrate) { _, _ = m.Close() }
