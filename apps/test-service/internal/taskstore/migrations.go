package taskstore

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"unit-test-ide.local/test-service/internal/task"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type migration struct {
	version  int
	checksum string
	sql      string
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		sha256 TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return storageError("initialize schema", err)
	}

	migrations, err := loadMigrations()
	if err != nil {
		return storageError("load migrations", err)
	}
	applied, err := s.validateAppliedMigrations(ctx, migrations)
	if err != nil {
		return err
	}
	for _, current := range migrations {
		if applied[current.version] {
			continue
		}

		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return storageError("begin migration", err)
		}
		if _, err := tx.ExecContext(ctx, current.sql); err != nil {
			_ = tx.Rollback()
			return storageError("apply migration", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations(version, sha256, applied_at) VALUES(?,?,?)`,
			current.version, current.checksum, formatTime(time.Now()),
		); err != nil {
			_ = tx.Rollback()
			return storageError("record migration", err)
		}
		if err := tx.Commit(); err != nil {
			return storageError("commit migration", err)
		}
	}
	return nil
}

func (s *Store) validateAppliedMigrations(ctx context.Context, migrations []migration) (map[int]bool, error) {
	expected := make(map[int]string, len(migrations))
	for _, current := range migrations {
		expected[current.version] = current.checksum
	}
	rows, err := s.db.QueryContext(ctx, `SELECT version, sha256 FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, storageError("read migration state", err)
	}
	defer rows.Close()
	applied := make(map[int]bool, len(migrations))
	for rows.Next() {
		var version int
		var checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			return nil, storageError("read migration state", err)
		}
		expectedChecksum, ok := expected[version]
		if !ok {
			return nil, fmt.Errorf("%w: unknown migration version %d", task.ErrStorageUnavailable, version)
		}
		if checksum != expectedChecksum {
			return nil, fmt.Errorf("%w: migration %d checksum mismatch", task.ErrStorageUnavailable, version)
		}
		applied[version] = true
	}
	if err := rows.Err(); err != nil {
		return nil, storageError("read migration state", err)
	}
	if err := validateMigrationPrefix(migrations, applied); err != nil {
		return nil, err
	}
	return applied, nil
}

func validateMigrationPrefix(migrations []migration, applied map[int]bool) error {
	missing := false
	for _, current := range migrations {
		if !applied[current.version] {
			missing = true
			continue
		}
		if missing {
			return fmt.Errorf("%w: migration history is not a contiguous prefix", task.ErrStorageUnavailable)
		}
	}
	return nil
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, err
	}
	result := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("invalid migration name")
		}
		version, err := strconv.Atoi(prefix)
		if err != nil || version < 1 {
			return nil, fmt.Errorf("invalid migration version")
		}
		contents, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(contents)
		result = append(result, migration{version: version, checksum: hex.EncodeToString(digest[:]), sql: string(contents)})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].version < result[j].version })
	for index := range result {
		if result[index].version != index+1 {
			return nil, fmt.Errorf("non-contiguous migration versions")
		}
	}
	return result, nil
}
