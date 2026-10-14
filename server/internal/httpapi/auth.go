package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"time"

	"github.com/MegaRoks/dashboard/server/internal/store"
	"github.com/MegaRoks/dashboard/server/internal/tgauth"
)

const (
	// __Host-: только Secure, только этот хост, Path=/ — cookie нельзя подменить с поддомена
	sessionCookie = "__Host-session"
	loginCookie   = "__Host-login"

	loginTTL = 5 * time.Minute
	// Лимиты на запросы входа: каждый — сообщение владельцу в Telegram, спамить не дадим
	loginWindow   = 10 * time.Minute
	loginPerIP    = 3
	loginPerTotal = 10
	// Продлевать сессию не чаще раза в час, чтобы не писать в базу на каждый запрос
	touchEvery = time.Hour
)

type sessionKey struct{}

func sessionFrom(ctx context.Context) store.Session { return ctx.Value(sessionKey{}).(store.Session) }

// sameOrigin — защита от CSRF: изменяющий запрос принимается, только если браузер прислал наш Origin.
func (s *server) sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("Origin") != s.Origin {
			s.Log.Warn("cross-origin request rejected", "method", r.Method, "path", r.URL.Path, "origin", r.Header.Get("Origin"), "ip", clientIP(r))
			writeError(w, http.StatusForbidden, "bad_origin", "Запрос с чужого сайта")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// session находит сессию по cookie и при необходимости продлевает её.
func (s *server) session(w http.ResponseWriter, r *http.Request) (store.Session, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return store.Session{}, false
	}
	now := s.Now()
	sess, err := s.Store.SessionByToken(r.Context(), c.Value, now)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.Log.Error("session lookup failed", "err", err)
		}
		return store.Session{}, false
	}
	if now.Sub(sess.LastSeenAt) >= touchEvery {
		sess.LastSeenAt, sess.ExpiresAt = now, now.Add(s.SessionTTL)
		if err := s.Store.TouchSession(r.Context(), sess.ID, now, sess.ExpiresAt); err != nil {
			s.Log.Error("session touch failed", "err", err)
		} else {
			s.setCookie(w, sessionCookie, c.Value, s.SessionTTL)
		}
	}
	return sess, true
}

func (s *server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.session(w, r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Нужно войти")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, sess)))
	})
}

// authCheck — для nginx auth_request: 204 пускает к статике, 401 отправляет на /login.
func (s *server) authCheck(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.session(w, r); !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// loginTelegram меняет initData из Mini App на сессию.
func (s *server) loginTelegram(w http.ResponseWriter, r *http.Request) {
	var req struct {
		InitData string `json:"initData"`
	}
	if !decode(w, r, &req) {
		return
	}
	data, err := tgauth.Validate(req.InitData, s.BotToken, s.InitDataTTL, s.Now())
	if err != nil {
		s.Log.Warn("initData rejected", "err", err, "ip", clientIP(r))
		writeError(w, http.StatusUnauthorized, "unauthorized", "Сессия Telegram недействительна, откройте дашборд заново")
		return
	}
	if data.User.ID != s.AllowedUserID {
		s.Log.Warn("user is not allowed", "user_id", data.User.ID, "username", data.User.Username, "ip", clientIP(r))
		writeError(w, http.StatusForbidden, "forbidden", "Доступ запрещён")
		return
	}
	if err := s.startSession(w, r, "telegram"); err != nil {
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// loginBrowserStart создаёт запрос на вход и присылает его владельцу в бот.
func (s *server) loginBrowserStart(w http.ResponseWriter, r *http.Request) {
	ctx, now, ip := r.Context(), s.Now(), clientIP(r)
	byIP, err := s.Store.CountLogins(ctx, now.Add(-loginWindow), ip)
	if err != nil {
		s.internalError(w, err)
		return
	}
	total, err := s.Store.CountLogins(ctx, now.Add(-loginWindow), "")
	if err != nil {
		s.internalError(w, err)
		return
	}
	if byIP >= loginPerIP || total >= loginPerTotal {
		s.Log.Warn("login rate limited", "ip", ip, "by_ip", byIP, "total", total)
		writeError(w, http.StatusTooManyRequests, "too_many_requests", "Слишком много попыток входа, подождите несколько минут")
		return
	}

	code, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		s.internalError(w, err)
		return
	}
	req := store.LoginRequest{
		ID: token(16), Code: fmt.Sprintf("%04d", code.Int64()), Status: store.LoginPending,
		UserAgent: r.UserAgent(), IP: ip, CreatedAt: now, ExpiresAt: now.Add(loginTTL),
	}
	secret := token(32)
	if err := s.Store.CreateLogin(ctx, req, secret); err != nil {
		s.internalError(w, err)
		return
	}
	if err := s.Bot.NotifyLogin(ctx, req); err != nil {
		s.Log.Error("login notification failed", "err", err)
		writeError(w, http.StatusBadGateway, "bot_unavailable", "Не удалось отправить запрос в Telegram, попробуйте ещё раз")
		return
	}
	s.setCookie(w, loginCookie, secret, loginTTL)
	writeJSON(w, http.StatusCreated, map[string]string{
		"id": req.ID, "code": req.Code, "expiresAt": req.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

// loginBrowserPoll — браузер ждёт решения в боте; после подтверждения получает сессию ровно один раз.
func (s *server) loginBrowserPoll(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(loginCookie)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Запрос на вход не найден, начните заново")
		return
	}
	ctx, now := r.Context(), s.Now()
	req, err := s.Store.LoginForBrowser(ctx, r.PathValue("id"), c.Value)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Запрос на вход не найден, начните заново")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	status := "expired"
	switch {
	case req.Status == store.LoginDenied:
		status = "denied"
	case !now.Before(req.ExpiresAt):
		// истёк — остаётся expired
	case req.Status == store.LoginPending:
		status = "pending"
	case req.Status == store.LoginApproved:
		switch err := s.Store.ConsumeLogin(ctx, req.ID, now); {
		case errors.Is(err, store.ErrNotFound):
			// параллельный опрос уже забрал сессию
		case err != nil:
			s.internalError(w, err)
			return
		default:
			if err := s.startSession(w, r, "browser"); err != nil {
				s.internalError(w, err)
				return
			}
			s.setCookie(w, loginCookie, "", -1)
			status = "ok"
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": status})
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.Store.DeleteSession(r.Context(), c.Value); err != nil {
			s.internalError(w, err)
			return
		}
	}
	s.setCookie(w, sessionCookie, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) startSession(w http.ResponseWriter, r *http.Request, via string) error {
	now, tok := s.Now(), token(32)
	sess := store.Session{UserID: s.AllowedUserID, Via: via, UserAgent: r.UserAgent(), IP: clientIP(r),
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(s.SessionTTL)}
	if _, err := s.Store.CreateSession(r.Context(), sess, tok); err != nil {
		return err
	}
	s.Log.Info("session started", "via", via, "ip", sess.IP)
	s.setCookie(w, sessionCookie, tok, s.SessionTTL)
	return nil
}

// setCookie: ttl < 0 удаляет cookie.
func (s *server) setCookie(w http.ResponseWriter, name, value string, ttl time.Duration) {
	c := &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode}
	if ttl < 0 {
		c.MaxAge = -1
	} else {
		c.MaxAge = int(ttl.Seconds())
	}
	http.SetCookie(w, c)
}

// clientIP: сервис слушает только localhost за nginx, поэтому X-Real-IP (из proxy_protocol) можно доверять.
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func token(n int) string {
	b := make([]byte, n)
	rand.Read(b) // crypto/rand.Read не возвращает ошибок с Go 1.24
	return base64.RawURLEncoding.EncodeToString(b)
}
