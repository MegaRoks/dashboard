package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

func TestSessions(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()

	sess, err := s.CreateSession(ctx, Session{UserID: 42, Via: "telegram", UserAgent: "ua", IP: "1.2.3.4",
		CreatedAt: t0, LastSeenAt: t0, ExpiresAt: t0.Add(30 * 24 * time.Hour)}, "token-a")
	if err != nil || sess.ID == 0 {
		t.Fatalf("CreateSession = %+v, %v", sess, err)
	}
	got, err := s.SessionByToken(ctx, "token-a", t0.Add(time.Hour))
	if err != nil || got.UserID != 42 || got.Via != "telegram" || !got.ExpiresAt.Equal(sess.ExpiresAt) {
		t.Fatalf("SessionByToken = %+v, %v", got, err)
	}
	if _, err := s.SessionByToken(ctx, "token-b", t0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown token: err = %v", err)
	}
	if _, err := s.SessionByToken(ctx, "token-a", t0.Add(31*24*time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session: err = %v", err)
	}

	// продление сдвигает срок
	if err := s.TouchSession(ctx, sess.ID, t0.Add(20*24*time.Hour), t0.Add(50*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionByToken(ctx, "token-a", t0.Add(40*24*time.Hour)); err != nil {
		t.Fatalf("touched session expired: %v", err)
	}

	if err := s.DeleteSession(ctx, "token-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionByToken(ctx, "token-a", t0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted session found: %v", err)
	}

	for _, tok := range []string{"x", "y"} {
		s.CreateSession(ctx, Session{UserID: 42, Via: "browser", CreatedAt: t0, LastSeenAt: t0, ExpiresAt: t0.Add(time.Hour)}, tok)
	}
	if n, err := s.DeleteAllSessions(ctx); err != nil || n != 2 {
		t.Fatalf("DeleteAllSessions = %d, %v", n, err)
	}
}

func TestLoginFlow(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	req := LoginRequest{ID: "req1", Code: "4821", Status: LoginPending, UserAgent: "ua", IP: "1.2.3.4", CreatedAt: t0, ExpiresAt: t0.Add(5 * time.Minute)}
	if err := s.CreateLogin(ctx, req, "secret"); err != nil {
		t.Fatal(err)
	}

	// другой браузер не видит запрос
	if _, err := s.LoginForBrowser(ctx, "req1", "other"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign secret: err = %v", err)
	}
	if got, err := s.LoginForBrowser(ctx, "req1", "secret"); err != nil || got.Status != LoginPending || got.Code != "4821" {
		t.Fatalf("LoginForBrowser = %+v, %v", got, err)
	}

	// pending нельзя «употребить», решение принимается один раз
	if err := s.ConsumeLogin(ctx, "req1", t0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("consume pending: err = %v", err)
	}
	if got, err := s.DecideLogin(ctx, "req1", LoginApproved, t0.Add(time.Minute)); err != nil || got.Status != LoginApproved {
		t.Fatalf("DecideLogin = %+v, %v", got, err)
	}
	if _, err := s.DecideLogin(ctx, "req1", LoginDenied, t0.Add(time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second decision: err = %v", err)
	}
	if err := s.ConsumeLogin(ctx, "req1", t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.ConsumeLogin(ctx, "req1", t0.Add(time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second consume: err = %v", err)
	}

	// истёкший запрос нельзя подтвердить
	s.CreateLogin(ctx, LoginRequest{ID: "req2", Code: "1111", Status: LoginPending, CreatedAt: t0, ExpiresAt: t0.Add(5 * time.Minute)}, "s2")
	if _, err := s.DecideLogin(ctx, "req2", LoginApproved, t0.Add(6*time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("approve expired: err = %v", err)
	}
	if _, err := s.DecideLogin(ctx, "req2", LoginUsed, t0); err == nil {
		t.Fatal("DecideLogin accepted status used")
	}

	n, _ := s.CountLogins(ctx, t0, "")
	nIP, _ := s.CountLogins(ctx, t0, "1.2.3.4")
	nLater, _ := s.CountLogins(ctx, t0.Add(time.Second), "")
	if n != 2 || nIP != 1 || nLater != 0 {
		t.Fatalf("CountLogins: all=%d ip=%d later=%d", n, nIP, nLater)
	}
}
