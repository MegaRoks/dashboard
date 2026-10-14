package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func hash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

type Session struct {
	ID         int64
	UserID     int64
	Via        string // telegram | browser
	UserAgent  string
	IP         string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

// CreateSession сохраняет сессию; в базу попадает только хэш token.
func (s *Store) CreateSession(ctx context.Context, sess Session, token string) (Session, error) {
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, via, user_agent, ip, created_at, last_seen_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		hash(token), sess.UserID, sess.Via, sess.UserAgent, sess.IP,
		sess.CreatedAt.Unix(), sess.LastSeenAt.Unix(), sess.ExpiresAt.Unix(),
	).Scan(&sess.ID)
	if err != nil {
		return Session{}, fmt.Errorf("store: create session: %w", err)
	}
	return sess, nil
}

// SessionByToken возвращает живую сессию; истёкшая — ErrNotFound.
func (s *Store) SessionByToken(ctx context.Context, token string, now time.Time) (Session, error) {
	var sess Session
	var created, seen, expires int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, via, user_agent, ip, created_at, last_seen_at, expires_at
		 FROM sessions WHERE token_hash = ? AND expires_at > ?`,
		hash(token), now.Unix(),
	).Scan(&sess.ID, &sess.UserID, &sess.Via, &sess.UserAgent, &sess.IP, &created, &seen, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("store: session: %w", err)
	}
	sess.CreatedAt, sess.LastSeenAt, sess.ExpiresAt = time.Unix(created, 0), time.Unix(seen, 0), time.Unix(expires, 0)
	return sess, nil
}

// TouchSession продлевает сессию: скользящий срок от последнего использования.
func (s *Store) TouchSession(ctx context.Context, id int64, now, expires time.Time) error {
	_, err := s.db.ExecContext(ctx, "UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id = ?", now.Unix(), expires.Unix(), id)
	if err != nil {
		return fmt.Errorf("store: touch session: %w", err)
	}
	return nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash = ?", hash(token))
	if err != nil {
		return fmt.Errorf("store: delete session: %w", err)
	}
	return nil
}

// DeleteAllSessions — «выйти везде»; возвращает число завершённых сессий.
func (s *Store) DeleteAllSessions(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM sessions")
	if err != nil {
		return 0, fmt.Errorf("store: delete sessions: %w", err)
	}
	return res.RowsAffected()
}

type LoginStatus string

const (
	LoginPending  LoginStatus = "pending"
	LoginApproved LoginStatus = "approved"
	LoginDenied   LoginStatus = "denied"
	LoginUsed     LoginStatus = "used"
)

type LoginRequest struct {
	ID        string
	Code      string // показывается в браузере и в боте: подтверждать, только если совпадает
	Status    LoginStatus
	UserAgent string
	IP        string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// CreateLogin сохраняет запрос на вход; secret — значение cookie браузера, начавшего вход.
func (s *Store) CreateLogin(ctx context.Context, req LoginRequest, secret string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO login_requests (id, secret_hash, code, status, user_agent, ip, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		req.ID, hash(secret), req.Code, req.Status, req.UserAgent, req.IP, req.CreatedAt.Unix(), req.ExpiresAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("store: create login: %w", err)
	}
	return nil
}

// CountLogins считает запросы на вход с момента since; ip == "" — со всех адресов.
func (s *Store) CountLogins(ctx context.Context, since time.Time, ip string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		"SELECT count(*) FROM login_requests WHERE created_at >= ? AND (? = '' OR ip = ?)",
		since.Unix(), ip, ip,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: count logins: %w", err)
	}
	return n, nil
}

// LoginForBrowser возвращает запрос, только если secret совпадает с браузером, который его начал.
func (s *Store) LoginForBrowser(ctx context.Context, id, secret string) (LoginRequest, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, code, status, user_agent, ip, created_at, expires_at FROM login_requests WHERE id = ? AND secret_hash = ?`,
		id, hash(secret))
	return scanLogin(row)
}

// DecideLogin переводит запрос из pending в approved или denied, пока он не истёк.
// Повторное решение и решение по истёкшему запросу — ErrNotFound.
func (s *Store) DecideLogin(ctx context.Context, id string, status LoginStatus, now time.Time) (LoginRequest, error) {
	if status != LoginApproved && status != LoginDenied {
		return LoginRequest{}, fmt.Errorf("store: decide login: bad status %q", status)
	}
	row := s.db.QueryRowContext(ctx,
		`UPDATE login_requests SET status = ? WHERE id = ? AND status = 'pending' AND expires_at > ?
		 RETURNING id, code, status, user_agent, ip, created_at, expires_at`,
		status, id, now.Unix())
	return scanLogin(row)
}

// ConsumeLogin один раз переводит подтверждённый запрос в used; повторно — ErrNotFound.
func (s *Store) ConsumeLogin(ctx context.Context, id string, now time.Time) error {
	res, err := s.db.ExecContext(ctx,
		"UPDATE login_requests SET status = 'used' WHERE id = ? AND status = 'approved' AND expires_at > ?", id, now.Unix())
	if err != nil {
		return fmt.Errorf("store: consume login: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanLogin(row *sql.Row) (LoginRequest, error) {
	var req LoginRequest
	var created, expires int64
	err := row.Scan(&req.ID, &req.Code, &req.Status, &req.UserAgent, &req.IP, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return LoginRequest{}, ErrNotFound
	}
	if err != nil {
		return LoginRequest{}, fmt.Errorf("store: login: %w", err)
	}
	req.CreatedAt, req.ExpiresAt = time.Unix(created, 0), time.Unix(expires, 0)
	return req, nil
}
