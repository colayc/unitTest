package taskstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"unit-test-ide.local/test-service/internal/task"

	_ "modernc.org/sqlite"
)

type Store struct {
	db    *sql.DB
	newID func() string
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, storageError("open task history", err)
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA synchronous=FULL",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, storageError("configure task history", err)
		}
	}
	store := &Store{db: db, newID: task.NewID}
	if err := store.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return storageError("close task history", err)
	}
	return nil
}

func storageError(operation string, _ error) error {
	return fmt.Errorf("%w: %s failed", task.ErrStorageUnavailable, operation)
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func nullableOutcome(value task.Task) any {
	if value.Status != task.StatusFinished {
		return nil
	}
	return string(value.Outcome)
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}
