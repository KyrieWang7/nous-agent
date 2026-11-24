package ideas

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	model "github.com/KyrieWang7/nous-agent/manager/manager_service/internal/model/ideas"
	"gorm.io/gorm"
)

//go:embed migrations/001_ideas.sql
var ideasSchema string

type legacyIdea struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Note    string `json:"note"`
	Source  string `json:"source"`
	Status  string `json:"status"`
	Created int64  `json:"created"`
}

func Migrate(ctx context.Context, db *gorm.DB, legacyPath string) error {
	const version = "001_ideas"
	sum := sha256.Sum256([]byte(ideasSchema))
	expected := hex.EncodeToString(sum[:])
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(1314280525)").Error; err != nil {
			return err
		}
		if err := tx.Exec(`CREATE TABLE IF NOT EXISTS manager_schema_migrations (
			version TEXT PRIMARY KEY, checksum TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`).Error; err != nil {
			return err
		}
		var recorded string
		err := tx.Raw("SELECT checksum FROM manager_schema_migrations WHERE version = ?", version).Row().Scan(&recorded)
		if err == nil {
			if recorded != expected {
				return fmt.Errorf("manager migration %s checksum changed", version)
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := tx.Exec(ideasSchema).Error; err != nil {
			return err
		}
		if err := importLegacy(tx, legacyPath); err != nil {
			return err
		}
		return tx.Exec("INSERT INTO manager_schema_migrations(version, checksum) VALUES (?, ?)", version, expected).Error
	})
}

func importLegacy(tx *gorm.DB, path string) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var records []legacyIdea
	if err := json.Unmarshal(raw, &records); err != nil {
		return fmt.Errorf("read legacy ideas: %w", err)
	}
	for i := len(records) - 1; i >= 0; i-- {
		entry := records[i]
		item := model.Idea{ID: entry.ID, Title: entry.Title, Note: entry.Note, Source: entry.Source,
			Status: entry.Status, CreatedAt: time.Unix(entry.Created, 0).UTC()}
		if err := tx.Create(&item).Error; err != nil {
			return fmt.Errorf("import legacy idea at index %d: %w", i, err)
		}
	}
	return nil
}
