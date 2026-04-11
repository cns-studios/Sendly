package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type migrationFile struct {
	version  string
	path     string
	checksum string
	content  string
}

func (p *Postgres) RunMigrations(ctx context.Context, migrationDir string) error {
	if migrationDir == "" {
		return fmt.Errorf("migration directory is required")
	}

	if err := p.ensureMigrationsTable(ctx); err != nil {
		return fmt.Errorf("ensure migrations table: %w", err)
	}

	files, err := loadMigrationFiles(migrationDir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("migration directory %q not found", migrationDir)
		}
		return err
	}

	for _, m := range files {
		appliedChecksum, applied, err := p.getAppliedMigration(ctx, m.version)
		if err != nil {
			return fmt.Errorf("read migration state for %s: %w", m.version, err)
		}

		if applied {
			if appliedChecksum != m.checksum {
				return fmt.Errorf("migration checksum mismatch for %s", m.version)
			}
			continue
		}

		if err := p.applyMigration(ctx, m); err != nil {
			return fmt.Errorf("apply migration %s: %w", m.version, err)
		}
	}

	return nil
}

func (p *Postgres) ensureMigrationsTable(ctx context.Context) error {
	const query = `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			checksum TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`
	_, err := p.db.ExecContext(ctx, query)
	return err
}

func (p *Postgres) getAppliedMigration(ctx context.Context, version string) (string, bool, error) {
	const query = `SELECT checksum FROM schema_migrations WHERE version = $1`
	var checksum string
	err := p.db.GetContext(ctx, &checksum, query, version)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return checksum, true, nil
}

func (p *Postgres) applyMigration(ctx context.Context, m migrationFile) error {
	tx, err := p.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}

	if _, err = tx.ExecContext(ctx, m.content); err != nil {
		_ = tx.Rollback()
		return err
	}

	if _, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, checksum, applied_at) VALUES ($1, $2, $3)`, m.version, m.checksum, time.Now().UTC()); err != nil {
		_ = tx.Rollback()
		return err
	}

	return tx.Commit()
}

func loadMigrationFiles(migrationDir string) ([]migrationFile, error) {
	entries, err := os.ReadDir(migrationDir)
	if err != nil {
		return nil, err
	}

	migrations := make([]migrationFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".sql") {
			continue
		}

		path := filepath.Join(migrationDir, name)
		contentBytes, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read migration file %s: %w", name, err)
		}
		checksumRaw := sha256.Sum256(contentBytes)
		migrations = append(migrations, migrationFile{
			version:  name,
			path:     path,
			checksum: hex.EncodeToString(checksumRaw[:]),
			content:  string(contentBytes),
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})

	return migrations, nil
}
