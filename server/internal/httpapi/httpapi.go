// Package httpapi — HTTP API дашборда для Telegram Mini App и браузера.
//
// Дашборд личный: данные получает только владелец (AllowedUserID). Вход — в auth.go:
// внутри Telegram по initData, в браузере — подтверждением в боте. После входа выдаётся
// cookie сессии, по ней работают /api/* и проверка nginx /auth/check, закрывающая статику.
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/MegaRoks/dashboard/server/internal/github"
	"github.com/MegaRoks/dashboard/server/internal/store"
)

// LoginNotifier присылает владельцу запрос на вход из браузера (реализация — tgbot.Bot).
type LoginNotifier interface {
	NotifyLogin(ctx context.Context, req store.LoginRequest) error
}

type Deps struct {
	Store         *store.Store
	GitHub        *github.Client
	Bot           LoginNotifier
	BotToken      string
	AllowedUserID int64
	InitDataTTL   time.Duration
	SessionTTL    time.Duration
	Origin        string // https://dashboard.apisky.org — единственный Origin для изменяющих запросов
	Location      *time.Location
	Log           *slog.Logger
	Now           func() time.Time // nil — time.Now
}

type server struct{ Deps }

func New(d Deps) http.Handler {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	s := &server{d}

	api := http.NewServeMux()
	api.HandleFunc("GET /api/me", s.me)
	api.HandleFunc("GET /api/tracks", s.listTracks)
	api.HandleFunc("POST /api/tracks/validate", s.validateTrack)
	api.HandleFunc("POST /api/tracks", s.createTrack)
	api.HandleFunc("DELETE /api/tracks/{id}", s.deleteTrack)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /auth/check", s.authCheck)
	mux.HandleFunc("POST /auth/telegram", s.loginTelegram)
	mux.HandleFunc("POST /auth/browser", s.loginBrowserStart)
	mux.HandleFunc("GET /auth/browser/{id}", s.loginBrowserPoll)
	mux.HandleFunc("POST /auth/logout", s.logout)
	mux.Handle("/api/", s.requireSession(api))
	return s.sameOrigin(mux)
}

func (s *server) me(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"id":        sess.UserID,
		"via":       sess.Via,
		"expiresAt": sess.ExpiresAt.UTC().Format(time.RFC3339),
		"timezone":  s.Location.String(),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store") // личные данные: не кэшировать ни в браузере, ни в прокси
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Некорректный запрос")
		return false
	}
	return true
}

func (s *server) internalError(w http.ResponseWriter, err error) {
	s.Log.Error("internal error", "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "Что-то пошло не так")
}
