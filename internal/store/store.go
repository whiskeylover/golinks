package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("link not found")

//go:embed migrations/*.sql
var migrations embed.FS

type Link struct {
	Shortcut       string
	DestinationURL string
	UseCount       int64
	IsFavorite     bool
	LastUsedAt     *time.Time
	ExpiresAt      *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (l Link) ExpirationDate() string {
	if l.ExpiresAt == nil {
		return ""
	}
	return l.ExpiresAt.UTC().Add(-time.Nanosecond).Format("2006-01-02")
}

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Ping(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	var ok int
	if err := s.db.QueryRowContext(ctx, "SELECT 1").Scan(&ok); err != nil {
		return fmt.Errorf("check database query: %w", err)
	}
	if ok != 1 {
		return fmt.Errorf("check database query: got %d", ok)
	}
	return nil
}

func (s *Store) migrate(ctx context.Context) error {
	const migrationTable = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	name TEXT PRIMARY KEY,
	applied_at TEXT NOT NULL
);`
	if _, err := s.db.ExecContext(ctx, migrationTable); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if err := s.applyMigration(ctx, entry.Name()); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) applyMigration(ctx context.Context, name string) error {
	var exists bool
	if err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name = ?)", name).Scan(&exists); err != nil {
		return fmt.Errorf("inspect migration %q: %w", name, err)
	}
	if exists {
		return nil
	}

	schema, err := migrations.ReadFile("migrations/" + name)
	if err != nil {
		return fmt.Errorf("read migration %q: %w", name, err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("start migration %q: %w", name, err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, string(schema)); err != nil {
		return fmt.Errorf("apply migration %q: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)", name, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("record migration %q: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %q: %w", name, err)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, shortcut string) (Link, error) {
	if err := s.deleteExpired(ctx, shortcut); err != nil {
		return Link{}, err
	}
	const query = `
SELECT shortcut, destination_url, use_count, is_favorite, last_used_at, expires_at, created_at, updated_at
FROM links
WHERE shortcut = ?`

	link, err := scanLink(s.db.QueryRowContext(ctx, query, shortcut))
	if errors.Is(err, sql.ErrNoRows) {
		return Link{}, ErrNotFound
	}
	if err != nil {
		return Link{}, fmt.Errorf("get link %q: %w", shortcut, err)
	}
	return link, nil
}

func (s *Store) List(ctx context.Context) ([]Link, error) {
	if err := s.DeleteExpired(ctx); err != nil {
		return nil, err
	}
	const query = `
SELECT shortcut, destination_url, use_count, is_favorite, last_used_at, expires_at, created_at, updated_at
FROM links
ORDER BY shortcut`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list links: %w", err)
	}
	defer rows.Close()

	var links []Link
	for rows.Next() {
		link, err := scanLink(rows)
		if err != nil {
			return nil, fmt.Errorf("scan link: %w", err)
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list links: %w", err)
	}
	return links, nil
}

func (s *Store) CountFavorites(ctx context.Context) (int, error) {
	if err := s.DeleteExpired(ctx); err != nil {
		return 0, err
	}
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM links WHERE is_favorite = 1").Scan(&count); err != nil {
		return 0, fmt.Errorf("count favorite links: %w", err)
	}
	return count, nil
}

func (s *Store) ListFavorites(ctx context.Context, limit int) ([]Link, error) {
	if limit <= 0 {
		return nil, nil
	}
	if err := s.DeleteExpired(ctx); err != nil {
		return nil, err
	}
	const query = `
SELECT shortcut, destination_url, use_count, is_favorite, last_used_at, expires_at, created_at, updated_at
FROM links
WHERE is_favorite = 1
ORDER BY shortcut
LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list favorite links: %w", err)
	}
	defer rows.Close()

	var links []Link
	for rows.Next() {
		link, err := scanLink(rows)
		if err != nil {
			return nil, fmt.Errorf("scan favorite link: %w", err)
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list favorite links: %w", err)
	}
	return links, nil
}

func (s *Store) ListTop(ctx context.Context, limit int) ([]Link, error) {
	if limit <= 0 {
		return nil, nil
	}
	if err := s.DeleteExpired(ctx); err != nil {
		return nil, err
	}
	const query = `
SELECT shortcut, destination_url, use_count, is_favorite, last_used_at, expires_at, created_at, updated_at
FROM links
WHERE is_favorite = 0
ORDER BY use_count DESC, shortcut
LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list top links: %w", err)
	}
	defer rows.Close()

	var links []Link
	for rows.Next() {
		link, err := scanLink(rows)
		if err != nil {
			return nil, fmt.Errorf("scan top link: %w", err)
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list top links: %w", err)
	}
	return links, nil
}

func (s *Store) Search(ctx context.Context, query string, limit int) ([]Link, error) {
	query = strings.TrimSpace(query)
	if query == "" || limit <= 0 {
		return nil, nil
	}
	if err := s.DeleteExpired(ctx); err != nil {
		return nil, err
	}
	const statement = `
SELECT shortcut, destination_url, use_count, is_favorite, last_used_at, expires_at, created_at, updated_at
FROM links
WHERE shortcut LIKE ? ESCAPE '\'
ORDER BY use_count DESC, shortcut
LIMIT ?`

	pattern := "%" + escapeLike(query) + "%"
	rows, err := s.db.QueryContext(ctx, statement, pattern, limit)
	if err != nil {
		return nil, fmt.Errorf("search links: %w", err)
	}
	defer rows.Close()

	var links []Link
	for rows.Next() {
		link, err := scanLink(rows)
		if err != nil {
			return nil, fmt.Errorf("scan searched link: %w", err)
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search links: %w", err)
	}
	return links, nil
}

func escapeLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

func (s *Store) RecordUse(ctx context.Context, shortcut string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx, "UPDATE links SET use_count = use_count + 1, last_used_at = ? WHERE shortcut = ? AND (expires_at IS NULL OR expires_at > ?)", now, shortcut, now)
	if err != nil {
		return fmt.Errorf("record use of link %q: %w", shortcut, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("record use of link %q: %w", shortcut, err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteExpired(ctx context.Context) error {
	if err := s.deleteExpired(ctx, ""); err != nil {
		return err
	}
	return nil
}

func (s *Store) deleteExpired(ctx context.Context, shortcut string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var err error
	if shortcut == "" {
		_, err = s.db.ExecContext(ctx, "DELETE FROM links WHERE expires_at IS NOT NULL AND expires_at <= ?", now)
	} else {
		_, err = s.db.ExecContext(ctx, "DELETE FROM links WHERE shortcut = ? AND expires_at IS NOT NULL AND expires_at <= ?", shortcut, now)
	}
	if err != nil {
		return fmt.Errorf("delete expired links: %w", err)
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, shortcut string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM links WHERE shortcut = ?", shortcut)
	if err != nil {
		return fmt.Errorf("delete link %q: %w", shortcut, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete link %q: %w", shortcut, err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetFavorite(ctx context.Context, shortcut string, favorite bool) error {
	result, err := s.db.ExecContext(ctx, "UPDATE links SET is_favorite = ?, updated_at = ? WHERE shortcut = ?", favorite, time.Now().UTC().Format(time.RFC3339Nano), shortcut)
	if err != nil {
		return fmt.Errorf("set favorite for link %q: %w", shortcut, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("set favorite for link %q: %w", shortcut, err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) Upsert(ctx context.Context, shortcut, destinationURL string, expiresAt *time.Time) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var expiresAtValue sql.NullString
	if expiresAt != nil {
		expiresAtValue = sql.NullString{String: expiresAt.UTC().Format(time.RFC3339Nano), Valid: true}
	}
	const query = `
INSERT INTO links (shortcut, destination_url, expires_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(shortcut) DO UPDATE SET
	destination_url = excluded.destination_url,
	expires_at = excluded.expires_at,
	updated_at = excluded.updated_at`

	if _, err := s.db.ExecContext(ctx, query, shortcut, destinationURL, expiresAtValue, now, now); err != nil {
		return fmt.Errorf("upsert link %q: %w", shortcut, err)
	}
	return nil
}

func (s *Store) Backup(ctx context.Context, outputPath string) error {
	if outputPath == "" {
		return errors.New("backup output path is required")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	if _, err := os.Stat(outputPath); err == nil {
		return fmt.Errorf("backup output already exists: %s", outputPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect backup output: %w", err)
	}

	escapedPath := strings.ReplaceAll(outputPath, "'", "''")
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO '"+escapedPath+"'"); err != nil {
		return fmt.Errorf("create backup: %w", err)
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanLink(s scanner) (Link, error) {
	var link Link
	var lastUsedAt, expiresAt sql.NullString
	var createdAt, updatedAt string
	if err := s.Scan(&link.Shortcut, &link.DestinationURL, &link.UseCount, &link.IsFavorite, &lastUsedAt, &expiresAt, &createdAt, &updatedAt); err != nil {
		return Link{}, err
	}

	var err error
	if lastUsedAt.Valid {
		parsed, err := time.Parse(time.RFC3339Nano, lastUsedAt.String)
		if err != nil {
			return Link{}, fmt.Errorf("parse last_used_at: %w", err)
		}
		link.LastUsedAt = &parsed
	}
	if expiresAt.Valid {
		parsed, err := time.Parse(time.RFC3339Nano, expiresAt.String)
		if err != nil {
			return Link{}, fmt.Errorf("parse expires_at: %w", err)
		}
		link.ExpiresAt = &parsed
	}
	link.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return Link{}, fmt.Errorf("parse created_at: %w", err)
	}
	link.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return Link{}, fmt.Errorf("parse updated_at: %w", err)
	}
	return link, nil
}
