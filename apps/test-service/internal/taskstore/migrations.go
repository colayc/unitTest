package taskstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
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

const migrationCleanupTimeout = 5 * time.Second

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
		if err := s.applyMigration(ctx, current); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) applyMigration(ctx context.Context, current migration) (resultErr error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return storageError("reserve migration connection", err)
	}
	defer conn.Close()

	var foreignKeys int
	if err := conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		return storageError("read foreign key setting", err)
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return storageError("disable foreign keys", err)
	}
	restored := false
	defer func() {
		if restored {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), migrationCleanupTimeout)
		defer cancel()
		if err := s.restoreMigrationConnection(cleanupCtx, conn, foreignKeys); err != nil {
			cleanupErr := errors.Join(storageError("restore foreign keys", err), err)
			resultErr = errors.Join(resultErr, cleanupErr)
		}
	}()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return storageError("begin migration", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, current.sql); err != nil {
		return storageError("apply migration", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, sha256, applied_at) VALUES(?,?,?)`,
		current.version, current.checksum, formatTime(time.Now()),
	); err != nil {
		return storageError("record migration", err)
	}
	if violations, err := countForeignKeyViolations(ctx, tx); err != nil {
		return storageError("check migration foreign keys", err)
	} else if violations != 0 {
		return storageError("check migration foreign keys", fmt.Errorf("%d violations", violations))
	}
	if err := tx.Commit(); err != nil {
		return storageError("commit migration", err)
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), migrationCleanupTimeout)
	defer cancel()
	restored = true
	if err := s.restoreMigrationConnection(cleanupCtx, conn, foreignKeys); err != nil {
		return errors.Join(storageError("restore foreign keys", err), err)
	}
	return nil
}

func (s *Store) restoreMigrationConnection(ctx context.Context, conn *sql.Conn, enabled int) error {
	cleanupErrors := make([]error, 0, 6)
	if err := restoreForeignKeys(ctx, conn, enabled); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("restore pinned connection: %w", err))
	}
	if violations, err := countForeignKeyViolations(ctx, conn); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("check pinned connection: %w", err))
	} else if violations != 0 {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("check pinned connection: %d foreign key violations", violations))
	}
	if err := conn.Close(); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("release pinned connection: %w", err))
	}

	replacement, err := s.db.Conn(ctx)
	if err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("reserve effective pool connection: %w", err))
		return errors.Join(cleanupErrors...)
	}
	if err := restoreForeignKeys(ctx, replacement, enabled); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("restore effective pool connection: %w", err))
	}
	if violations, err := countForeignKeyViolations(ctx, replacement); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("check effective pool connection: %w", err))
	} else if violations != 0 {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("check effective pool connection: %d foreign key violations", violations))
	}
	if err := replacement.Close(); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("release effective pool connection: %w", err))
	}
	return errors.Join(cleanupErrors...)
}

func restoreForeignKeys(ctx context.Context, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, enabled int) error {
	setting := "OFF"
	if enabled != 0 {
		setting = "ON"
	}
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=`+setting); err != nil {
		return err
	}
	var restored int
	if err := db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&restored); err != nil {
		return err
	}
	if restored != enabled {
		return fmt.Errorf("foreign key setting remained %d", restored)
	}
	return nil
}

func countForeignKeyViolations(ctx context.Context, queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) (int, error) {
	rows, err := queryer.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return count, nil
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
