package tracker

import (
	"context"
	"database/sql"
	"fmt"
	"os"
)

func (s *Store) NotificationCursor(ctx context.Context) (int64, error) {
	var cursor int64
	err := s.db.QueryRowContext(ctx, "SELECT coalesce((SELECT seq FROM sqlite_sequence WHERE name='notifications'),0)").Scan(&cursor)
	return cursor, err
}

// ClearNotificationHistory is a local administrative operation, never an HTTP
// or MCP tool. It does not run migrations, reconcile activity or reset IDs.
func ClearNotificationHistory(ctx context.Context, path string) (int64, error) {
	if _, err := os.Stat(path); err != nil {
		return 0, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.ExecContext(ctx, "PRAGMA busy_timeout=5000"); err != nil {
		return 0, err
	}
	var version int
	if err = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, err
	}
	if version != 1 {
		return 0, fmt.Errorf("unexpected database schema %d", version)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "DELETE FROM notifications")
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return count, tx.Commit()
}
