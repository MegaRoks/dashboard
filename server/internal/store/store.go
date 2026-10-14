// Package store — SQLite-хранилище дашборда.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

//go:embed migrations/*.sql
var migrations embed.FS

var (
	ErrDuplicate = errors.New("store: already exists")
	ErrNotFound  = errors.New("store: not found")
)

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	// SQLite пишет одним писателем; для личного дашборда одного соединения достаточно
	db.SetMaxOpenConns(1)
	if err := migrate(context.Background(), db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// migrate применяет migrations/NNN_*.sql по порядку; номер последней применённой хранится в PRAGMA user_version.
func migrate(ctx context.Context, db *sql.DB) error {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("store: read schema version: %w", err)
	}
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	slices.Sort(names)
	for i := version; i < len(names); i++ {
		text, err := migrations.ReadFile(names[i])
		if err != nil {
			return err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(text)); err != nil {
			tx.Rollback()
			return fmt.Errorf("store: migration %s: %w", names[i], err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("store: migration %s: %w", names[i], err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("store: migration %s: %w", names[i], err)
		}
	}
	return nil
}

type Track struct {
	ID        int64
	Owner     string
	Repo      string
	Color     string
	CreatedAt time.Time
}

func (s *Store) Tracks(ctx context.Context) ([]Track, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, owner, repo, color, created_at FROM tracks ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("store: tracks: %w", err)
	}
	defer rows.Close()
	var out []Track
	for rows.Next() {
		t, err := scanTrack(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TrackByRepo ищет трек без учёта регистра owner/repo.
func (s *Store) TrackByRepo(ctx context.Context, owner, repo string) (Track, error) {
	row := s.db.QueryRowContext(ctx, "SELECT id, owner, repo, color, created_at FROM tracks WHERE owner = ? AND repo = ?", owner, repo)
	t, err := scanTrack(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Track{}, ErrNotFound
	}
	return t, err
}

func (s *Store) CreateTrack(ctx context.Context, t Track) (Track, error) {
	t.CreatedAt = time.Now().UTC().Truncate(time.Second)
	err := s.db.QueryRowContext(ctx,
		"INSERT INTO tracks (owner, repo, color, created_at) VALUES (?, ?, ?, ?) RETURNING id",
		t.Owner, t.Repo, t.Color, t.CreatedAt.Format(time.RFC3339),
	).Scan(&t.ID)
	if isUnique(err) {
		return Track{}, ErrDuplicate
	}
	if err != nil {
		return Track{}, fmt.Errorf("store: create track: %w", err)
	}
	return t, nil
}

func (s *Store) DeleteTrack(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM tracks WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("store: delete track: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanTrack(row interface{ Scan(...any) error }) (Track, error) {
	var t Track
	var created string
	if err := row.Scan(&t.ID, &t.Owner, &t.Repo, &t.Color, &created); err != nil {
		return Track{}, err
	}
	var err error
	if t.CreatedAt, err = time.Parse(time.RFC3339, created); err != nil {
		return Track{}, fmt.Errorf("store: track %d created_at: %w", t.ID, err)
	}
	return t, nil
}

func isUnique(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE
}
