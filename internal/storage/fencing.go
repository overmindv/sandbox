package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"strings"
	"sync"
	"time"
)

// AttemptFencer удерживает владение до завершения обработки и retry bookkeeping.
type AttemptFencer interface {
	AcquireAttempt(context.Context, string) (context.Context, func(), error)
}
type attemptConnectionKey struct{}
type attemptConnection struct {
	store *PostgresRunStore
	conn  *sql.Conn
}

func (s *PostgresRunStore) AcquireAttempt(ctx context.Context, attemptID string) (context.Context, func(), error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return ctx, nil, err
	}
	digest := sha256.Sum256([]byte("sandbox-attempt:" + strings.ToLower(attemptID)))
	key := int64(binary.BigEndian.Uint64(digest[:8]))
	var locked bool
	if err = conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&locked); err != nil || !locked {
		_ = conn.Close()
		if err == nil {
			err = ErrAttemptInProgress
		}
		return ctx, nil, err
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := conn.ExecContext(cleanup, "SELECT pg_advisory_unlock($1)", key); err != nil {
				// Нельзя возвращать connection с неизвестным состоянием lock в pool.
				_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			}
			_ = conn.Close()
		})
	}
	return context.WithValue(ctx, attemptConnectionKey{}, attemptConnection{s, conn}), release, nil
}

func (s *PostgresRunStore) beginTx(ctx context.Context) (*sql.Tx, error) {
	if owned, ok := ctx.Value(attemptConnectionKey{}).(attemptConnection); ok && owned.store == s {
		return owned.conn.BeginTx(ctx, nil)
	}
	return s.db.BeginTx(ctx, nil)
}
