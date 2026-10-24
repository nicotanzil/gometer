// Package workspace provides transactional team persistence. It is independent
// of personal-mode JSON records and never executes workload.
package workspace

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

const schemaVersion = 2

var migrationFiles = []string{"001_workspace.sql", "002_sessions.sql"}

const migrationLock int64 = 0x676f6d65746572

var (
	ErrUnavailable = errors.New("workspace database unavailable; check connectivity and credentials")
	ErrSchema      = errors.New("workspace database schema is not compatible; run workspace migrate with this binary")
	ErrForbidden   = errors.New("workspace action is not permitted")
	ErrNotFound    = errors.New("saved test not found")
	ErrConflict    = errors.New("saved test revision changed; retain your draft and reload or copy")
)

type Database struct{ pool *pgxpool.Pool }

func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
func ValidID(id string) bool {
	if len(id) != 32 || id != strings.ToLower(id) {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

// Open does not migrate. Remote connections must verify the server certificate
// and hostname; cleartext is accepted only for explicit loopback/Unix sockets.
func Open(ctx context.Context, dsn string) (*Database, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("set GOMETER_DATABASE_URL")
	}
	options, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("invalid GOMETER_DATABASE_URL")
	}
	safeTransport := func(host string, tlsConfig *tls.Config) bool {
		ip := net.ParseIP(host)
		if strings.HasPrefix(host, "/") || (ip != nil && ip.IsLoopback()) {
			return true
		}
		return tlsConfig != nil && !tlsConfig.InsecureSkipVerify && tlsConfig.ServerName != ""
	}
	if !safeTransport(options.ConnConfig.Host, options.ConnConfig.TLSConfig) {
		return nil, fmt.Errorf("remote PostgreSQL requires verified TLS; use sslmode=verify-full and a trusted CA")
	}
	for _, fallback := range options.ConnConfig.Fallbacks {
		if !safeTransport(fallback.Host, fallback.TLSConfig) {
			return nil, fmt.Errorf("PostgreSQL must not fall back to unverified remote transport")
		}
	}
	options.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	options.ConnConfig.RuntimeParams["lock_timeout"] = "5000"
	options.MaxConns = 8
	options.MinConns = 0
	options.ConnConfig.ConnectTimeout = 5 * time.Second
	options.MaxConnIdleTime = time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, options)
	if err != nil {
		return nil, ErrUnavailable
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, ErrUnavailable
	}
	return &Database{pool: pool}, nil
}
func (d *Database) Close() { d.pool.Close() }

// Migrate serializes schema changes and commits each schema version with its
// checksum. It refuses altered migration history and future schema versions.
func (d *Database) Migrate(ctx context.Context) error {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLock); err != nil {
		return ErrUnavailable
	}
	if _, err = tx.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS gometer; CREATE TABLE IF NOT EXISTS gometer.schema_migrations(version integer PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT clock_timestamp())`); err != nil {
		return fmt.Errorf("workspace migration metadata could not be created")
	}
	rows, err := tx.Query(ctx, `SELECT version,checksum FROM gometer.schema_migrations ORDER BY version`)
	if err != nil {
		return ErrUnavailable
	}
	known := map[int]string{}
	for rows.Next() {
		var version int
		var checksum string
		if rows.Scan(&version, &checksum) != nil {
			rows.Close()
			return ErrSchema
		}
		known[version] = checksum
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ErrUnavailable
	}
	for version := range known {
		if version < 1 || version > schemaVersion {
			return ErrSchema
		}
	}
	for version := 1; version <= schemaVersion; version++ {
		data, hash := migrationData(version)
		if old, exists := known[version]; exists {
			if old != hash {
				return ErrSchema
			}
			continue
		}
		// A later recorded migration cannot precede an absent earlier migration.
		for later := version + 1; later <= schemaVersion; later++ {
			if _, exists := known[later]; exists {
				return ErrSchema
			}
		}
		if _, err = tx.Exec(ctx, string(data)); err != nil {
			return fmt.Errorf("workspace schema migration %d failed; no schema changes committed", version)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO gometer.schema_migrations(version,checksum) VALUES($1,$2)`, version, hash); err != nil {
			return ErrUnavailable
		}
	}
	if tx.Commit(ctx) != nil {
		return ErrUnavailable
	}
	return nil
}
func migrationData(version int) ([]byte, string) {
	data, err := migrations.ReadFile("migrations/" + migrationFiles[version-1])
	if err != nil {
		panic("embedded workspace migration missing")
	}
	return data, fmt.Sprintf("%x", sha256.Sum256(data))
}
func (d *Database) Check(ctx context.Context) error {
	rows, err := d.pool.Query(ctx, `SELECT version,checksum FROM gometer.schema_migrations ORDER BY version`)
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && (databaseError.Code == "42P01" || databaseError.Code == "3F000") {
			return ErrSchema
		}
		return ErrUnavailable
	}
	defer rows.Close()
	version := 0
	for rows.Next() {
		var current int
		var checksum string
		if rows.Scan(&current, &checksum) != nil {
			return ErrUnavailable
		}
		version++
		if current != version || current > schemaVersion {
			return ErrSchema
		}
		_, expected := migrationData(current)
		if checksum != expected {
			return ErrSchema
		}
	}
	if rows.Err() != nil {
		return ErrUnavailable
	}
	if version != schemaVersion {
		return ErrSchema
	}
	return nil
}
