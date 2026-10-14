package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MegaRoks/dashboard/server/internal/github"
	"github.com/MegaRoks/dashboard/server/internal/store"
	"github.com/MegaRoks/dashboard/server/internal/tgauth"
)

const (
	botToken  = "123456:test-token"
	allowedID = 42
	origin    = "https://dashboard.apisky.org"
)

var (
	msk, _ = time.LoadLocation("Europe/Moscow")
	start  = time.Date(2026, 10, 15, 12, 0, 0, 0, msk) // день после последнего занятия: серия ещё жива
)

const interviewPrep = `{"generatedAt":"2026-10-14","title":"Подготовка к собеседованиям","goal":"закрыть пробелы к senior-интервью",
"progress":{"done":0,"total":37,"percent":0},"items":{"done":25,"partial":1,"total":312},"streak":1,
"lastActivity":"2026-10-14","today":"промисы и async/await",
"sections":[{"id":"1.1","title":"JavaScript core","block":1,"done":7,"partial":0,"total":24,"completed":false}]}`

// fakeGitHub знает три репозитория: трек, репозиторий без progress.json и с чужим progress.json.
func fakeGitHub(t *testing.T) *github.Client {
	repos := map[string]string{"interview-prep": "Interview-prep", "grain-ui": "grain-ui", "other": "other"}
	files := map[string]string{"Interview-prep": interviewPrep, "other": `{"name":"pkg"}`}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/{owner}/{repo}", func(w http.ResponseWriter, r *http.Request) {
		name, ok := repos[strings.ToLower(r.PathValue("repo"))]
		if !ok || !strings.EqualFold(r.PathValue("owner"), "MegaRoks") {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"name": name, "owner": map[string]string{"login": "MegaRoks"}})
	})
	mux.HandleFunc("GET /repos/MegaRoks/{repo}/contents/progress.json", func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[r.PathValue("repo")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := github.New("")
	c.BaseURL = srv.URL
	return c
}

// fakeBot запоминает запросы на вход вместо отправки в Telegram.
type fakeBot struct {
	mu   sync.Mutex
	sent []store.LoginRequest
	fail bool
}

func (b *fakeBot) NotifyLogin(_ context.Context, req store.LoginRequest) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fail {
		return errors.New("telegram is down")
	}
	b.sent = append(b.sent, req)
	return nil
}

type env struct {
	h     http.Handler
	store *store.Store
	bot   *fakeBot
	now   time.Time
}

func newEnv(t *testing.T) *env {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := &env{store: st, bot: &fakeBot{}, now: start}
	e.h = New(Deps{
		Store: st, GitHub: fakeGitHub(t), Bot: e.bot, BotToken: botToken, AllowedUserID: allowedID,
		InitDataTTL: 10 * time.Minute, SessionTTL: 30 * 24 * time.Hour, Origin: origin, Location: msk,
		Now: func() time.Time { return e.now }, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return e
}

// browser — клиент с cookie и своим IP, как отдельный браузер.
type browser struct {
	e       *env
	ip      string
	cookies map[string]*http.Cookie
}

func (e *env) browser(ip string) *browser {
	return &browser{e: e, ip: ip, cookies: map[string]*http.Cookie{}}
}

type resp struct {
	code int
	obj  map[string]any
	arr  []any
	set  map[string]*http.Cookie
}

func (b *browser) do(t *testing.T, method, path, body string) resp {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-Real-IP", b.ip)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Chrome/141.0 Safari/537.36")
	if method != http.MethodGet {
		req.Header.Set("Origin", origin)
	}
	for _, c := range b.cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	b.e.h.ServeHTTP(rec, req)

	r := resp{code: rec.Code, set: map[string]*http.Cookie{}}
	for _, c := range rec.Result().Cookies() {
		r.set[c.Name] = c
		if c.MaxAge < 0 {
			delete(b.cookies, c.Name)
		} else {
			b.cookies[c.Name] = c
		}
	}
	if strings.HasPrefix(rec.Body.String(), "[") {
		json.Unmarshal(rec.Body.Bytes(), &r.arr)
	} else {
		json.Unmarshal(rec.Body.Bytes(), &r.obj)
	}
	return r
}

func initData(userID int64, authDate time.Time) string {
	return tgauth.Sign(url.Values{
		"auth_date": {strconv.FormatInt(authDate.Unix(), 10)},
		"user":      {`{"id":` + strconv.FormatInt(userID, 10) + `,"first_name":"Максим","username":"megaroks"}`},
	}, botToken)
}

func telegramLogin(userID int64, authDate time.Time) string {
	b, _ := json.Marshal(map[string]string{"initData": initData(userID, authDate)})
	return string(b)
}

// loggedIn — браузер владельца, вошедший через Telegram.
func (e *env) loggedIn(t *testing.T) *browser {
	t.Helper()
	b := e.browser("10.0.0.1")
	if r := b.do(t, "POST", "/auth/telegram", telegramLogin(allowedID, e.now)); r.code != http.StatusNoContent {
		t.Fatalf("telegram login = %d %v", r.code, r.obj)
	}
	return b
}

func TestStrangerSeesNothing(t *testing.T) {
	e := newEnv(t)
	b := e.browser("6.6.6.6")
	for _, path := range []string{"/api/me", "/api/tracks"} {
		if r := b.do(t, "GET", path, ""); r.code != http.StatusUnauthorized {
			t.Fatalf("GET %s = %d", path, r.code)
		}
	}
	if r := b.do(t, "GET", "/auth/check", ""); r.code != http.StatusUnauthorized {
		t.Fatalf("auth check = %d", r.code)
	}
	b.cookies[sessionCookie] = &http.Cookie{Name: sessionCookie, Value: "forged"}
	if r := b.do(t, "GET", "/auth/check", ""); r.code != http.StatusUnauthorized {
		t.Fatalf("forged cookie = %d", r.code)
	}
	if r := b.do(t, "GET", "/healthz", ""); r.code != http.StatusOK {
		t.Fatalf("healthz = %d", r.code)
	}
}

func TestTelegramLogin(t *testing.T) {
	e := newEnv(t)
	b := e.browser("10.0.0.1")

	cases := []struct {
		name string
		body string
		want int
	}{
		{"other user", telegramLogin(7, e.now), http.StatusForbidden},
		{"expired initData", telegramLogin(allowedID, e.now.Add(-11*time.Minute)), http.StatusUnauthorized},
		{"bad signature", strings.Replace(telegramLogin(allowedID, e.now), "megaroks", "mallory", 1), http.StatusUnauthorized},
		{"garbage", `{"initData":"x"}`, http.StatusUnauthorized},
	}
	for _, c := range cases {
		if r := b.do(t, "POST", "/auth/telegram", c.body); r.code != c.want || r.set[sessionCookie] != nil {
			t.Fatalf("%s: %d (cookie %v), want %d without cookie", c.name, r.code, r.set[sessionCookie], c.want)
		}
	}

	r := b.do(t, "POST", "/auth/telegram", telegramLogin(allowedID, e.now))
	c := r.set[sessionCookie]
	if r.code != http.StatusNoContent || c == nil {
		t.Fatalf("login = %d, cookie %v", r.code, c)
	}
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Domain != "" || c.MaxAge != 30*24*3600 {
		t.Fatalf("session cookie attributes: %+v", c)
	}
	if r := b.do(t, "GET", "/auth/check", ""); r.code != http.StatusNoContent {
		t.Fatalf("auth check = %d", r.code)
	}
	if r := b.do(t, "GET", "/api/me", ""); r.code != http.StatusOK || r.obj["id"] != float64(allowedID) || r.obj["via"] != "telegram" {
		t.Fatalf("me = %d %v", r.code, r.obj)
	}
}

func TestUnsafeRequestsNeedOrigin(t *testing.T) {
	e := newEnv(t)
	b := e.loggedIn(t)
	for _, o := range []string{"", "https://evil.example", "http://dashboard.apisky.org"} {
		req := httptest.NewRequest("POST", "/api/tracks", strings.NewReader(`{"url":"github.com/MegaRoks/Interview-prep"}`))
		req.AddCookie(b.cookies[sessionCookie])
		if o != "" {
			req.Header.Set("Origin", o)
		}
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("Origin %q = %d, want 403", o, rec.Code)
		}
	}
	if list, _ := e.store.Tracks(context.Background()); len(list) != 0 {
		t.Fatal("cross-origin request created a track")
	}
}

func TestBrowserLogin(t *testing.T) {
	e := newEnv(t)
	b := e.browser("1.2.3.4")

	r := b.do(t, "POST", "/auth/browser", "")
	if r.code != http.StatusCreated || len(e.bot.sent) != 1 {
		t.Fatalf("start = %d %v, sent %d", r.code, r.obj, len(e.bot.sent))
	}
	id, code := r.obj["id"].(string), r.obj["code"].(string)
	sent := e.bot.sent[0]
	if sent.ID != id || sent.Code != code || len(code) != 4 || sent.IP != "1.2.3.4" || !strings.Contains(sent.UserAgent, "Chrome") {
		t.Fatalf("bot got %+v; browser got id=%s code=%s", sent, id, code)
	}
	loginSecret := r.set[loginCookie]
	if loginSecret == nil || !loginSecret.HttpOnly || !loginSecret.Secure || loginSecret.MaxAge != 300 {
		t.Fatalf("login cookie: %+v", loginSecret)
	}

	if r := b.do(t, "GET", "/auth/browser/"+id, ""); r.obj["status"] != "pending" {
		t.Fatalf("poll before decision = %v", r.obj)
	}
	// чужой браузер, узнавший id, ничего не получает
	if r := e.browser("6.6.6.6").do(t, "GET", "/auth/browser/"+id, ""); r.code != http.StatusNotFound {
		t.Fatalf("foreign poll = %d", r.code)
	}

	e.store.DecideLogin(context.Background(), id, store.LoginApproved, e.now)
	r = b.do(t, "GET", "/auth/browser/"+id, "")
	if r.obj["status"] != "ok" || r.set[sessionCookie] == nil || r.set[loginCookie] == nil || r.set[loginCookie].MaxAge >= 0 {
		t.Fatalf("poll after approve = %v, cookies %v", r.obj, r.set)
	}
	if r := b.do(t, "GET", "/api/me", ""); r.code != http.StatusOK || r.obj["via"] != "browser" {
		t.Fatalf("me after browser login = %d %v", r.code, r.obj)
	}

	// одноразовость: повтор опроса с той же login-cookie (например, перехваченной) новой сессии не даёт
	replay := e.browser("1.2.3.4")
	replay.cookies[loginCookie] = loginSecret
	if r := replay.do(t, "GET", "/auth/browser/"+id, ""); r.obj["status"] != "expired" || r.set[sessionCookie] != nil {
		t.Fatalf("replayed poll = %v, cookies %v", r.obj, r.set)
	}
}

func TestBrowserLoginDeniedAndExpired(t *testing.T) {
	e := newEnv(t)
	b := e.browser("1.2.3.4")

	denied := b.do(t, "POST", "/auth/browser", "").obj["id"].(string)
	e.store.DecideLogin(context.Background(), denied, store.LoginDenied, e.now)
	if r := b.do(t, "GET", "/auth/browser/"+denied, ""); r.obj["status"] != "denied" || r.set[sessionCookie] != nil {
		t.Fatalf("denied poll = %v", r.obj)
	}

	late := b.do(t, "POST", "/auth/browser", "").obj["id"].(string)
	e.now = e.now.Add(6 * time.Minute)
	if r := b.do(t, "GET", "/auth/browser/"+late, ""); r.obj["status"] != "expired" {
		t.Fatalf("expired poll = %v", r.obj)
	}
	if _, err := e.store.DecideLogin(context.Background(), late, store.LoginApproved, e.now); err == nil {
		t.Fatal("expired request was approved")
	}
}

func TestBrowserLoginRateLimit(t *testing.T) {
	e := newEnv(t)
	spammer := e.browser("6.6.6.6")
	for i := range loginPerIP {
		if r := spammer.do(t, "POST", "/auth/browser", ""); r.code != http.StatusCreated {
			t.Fatalf("attempt %d = %d", i+1, r.code)
		}
	}
	if r := spammer.do(t, "POST", "/auth/browser", ""); r.code != http.StatusTooManyRequests {
		t.Fatalf("over per-IP limit = %d", r.code)
	}
	// общий лимит: с разных адресов не больше loginPerTotal
	for i := loginPerIP; i < loginPerTotal; i++ {
		if r := e.browser("10.1.0."+strconv.Itoa(i)).do(t, "POST", "/auth/browser", ""); r.code != http.StatusCreated {
			t.Fatalf("attempt from new IP %d = %d", i, r.code)
		}
	}
	if r := e.browser("10.2.0.1").do(t, "POST", "/auth/browser", ""); r.code != http.StatusTooManyRequests {
		t.Fatalf("over total limit = %d", r.code)
	}
	if len(e.bot.sent) != loginPerTotal {
		t.Fatalf("bot messages = %d, want %d", len(e.bot.sent), loginPerTotal)
	}
	// окно сдвинулось — снова можно
	e.now = e.now.Add(loginWindow + time.Second)
	if r := spammer.do(t, "POST", "/auth/browser", ""); r.code != http.StatusCreated {
		t.Fatalf("after window = %d", r.code)
	}
}

func TestBrowserLoginBotDown(t *testing.T) {
	e := newEnv(t)
	e.bot.fail = true
	if r := e.browser("1.2.3.4").do(t, "POST", "/auth/browser", ""); r.code != http.StatusBadGateway || r.set[loginCookie] != nil {
		t.Fatalf("bot down = %d, cookies %v", r.code, r.set)
	}
}

func TestLogoutAndSliding(t *testing.T) {
	e := newEnv(t)
	b := e.loggedIn(t)

	// в течение часа сессия не продлевается, после — продлевается и cookie обновляется
	if r := b.do(t, "GET", "/api/me", ""); r.set[sessionCookie] != nil {
		t.Fatal("session touched too early")
	}
	e.now = e.now.Add(29 * 24 * time.Hour)
	r := b.do(t, "GET", "/api/me", "")
	if r.code != http.StatusOK || r.set[sessionCookie] == nil {
		t.Fatalf("sliding: %d, cookie %v", r.code, r.set[sessionCookie])
	}
	e.now = e.now.Add(29 * 24 * time.Hour) // 58 дней от входа, но 29 от последнего визита
	if r := b.do(t, "GET", "/api/me", ""); r.code != http.StatusOK {
		t.Fatalf("session expired despite activity: %d", r.code)
	}

	if r := b.do(t, "POST", "/auth/logout", ""); r.code != http.StatusNoContent {
		t.Fatalf("logout = %d", r.code)
	}
	b.cookies = map[string]*http.Cookie{} // на случай если браузер не удалил cookie — проверим сервер
	stale := e.browser("10.0.0.1")
	if r := stale.do(t, "GET", "/api/me", ""); r.code != http.StatusUnauthorized {
		t.Fatalf("after logout = %d", r.code)
	}
}

func TestSessionExpiresWithoutActivity(t *testing.T) {
	e := newEnv(t)
	b := e.loggedIn(t)
	e.now = e.now.Add(31 * 24 * time.Hour)
	if r := b.do(t, "GET", "/api/me", ""); r.code != http.StatusUnauthorized {
		t.Fatalf("stale session = %d", r.code)
	}
}

func TestValidateTrack(t *testing.T) {
	e := newEnv(t)
	b := e.loggedIn(t)
	cases := map[string]string{
		"not a link":                           "invalid_url",
		"github.com/x/y":                       "not_found",
		"github.com/MegaRoks/grain-ui":         "not_a_track",
		"github.com/MegaRoks/other":            "not_a_track",
		" github.com/megaroks/interview-prep ": "ok",
	}
	for in, want := range cases {
		r := b.do(t, "POST", "/api/tracks/validate", `{"url":"`+in+`"}`)
		if r.code != http.StatusOK || r.obj["status"] != want {
			t.Errorf("validate %q = %d %v, want %s", in, r.code, r.obj, want)
		}
	}
	r := b.do(t, "POST", "/api/tracks/validate", `{"url":"github.com/megaroks/interview-prep"}`)
	if r.obj["owner"] != "MegaRoks" || r.obj["repo"] != "Interview-prep" || r.obj["title"] != "Подготовка к собеседованиям" {
		t.Fatalf("ok result must carry canonical repo and title: %v", r.obj)
	}
	if r := b.do(t, "POST", "/api/tracks/validate", `{"link":"x"}`); r.code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d, want 400", r.code)
	}
}

func TestTrackLifecycle(t *testing.T) {
	e := newEnv(t)
	b := e.loggedIn(t)

	r := b.do(t, "POST", "/api/tracks", `{"url":"https://github.com/megaroks/interview-prep"}`)
	if r.code != http.StatusCreated || r.obj["owner"] != "MegaRoks" || r.obj["repo"] != "Interview-prep" || r.obj["color"] != "teal" {
		t.Fatalf("create = %d %v", r.code, r.obj)
	}
	if r := b.do(t, "POST", "/api/tracks", `{"url":"github.com/MegaRoks/Interview-prep"}`); r.code != http.StatusConflict || r.obj["status"] != "duplicate" {
		t.Fatalf("duplicate = %d %v", r.code, r.obj)
	}
	if r := b.do(t, "POST", "/api/tracks", `{"url":"github.com/MegaRoks/grain-ui"}`); r.code != http.StatusUnprocessableEntity {
		t.Fatalf("not a track = %d", r.code)
	}
	if r := b.do(t, "POST", "/api/tracks", `{"url":"github.com/MegaRoks/grain-ui","color":"purple"}`); r.code != http.StatusBadRequest {
		t.Fatalf("bad color = %d", r.code)
	}

	r = b.do(t, "GET", "/api/tracks", "")
	if r.code != http.StatusOK || len(r.arr) != 1 {
		t.Fatalf("list = %d %v", r.code, r.arr)
	}
	track := r.arr[0].(map[string]any)
	status := track["status"].(map[string]any)
	items := track["items"].(map[string]any)
	if track["title"] != "Подготовка к собеседованиям" || items["done"] != float64(25) || items["total"] != float64(312) {
		t.Fatalf("track progress: %v", track)
	}
	if status["state"] != "streak" || status["streak"] != float64(1) || status["daysSinceLastActivity"] != float64(1) {
		t.Fatalf("status is not recomputed for today: %v", status)
	}

	id := strconv.Itoa(int(track["id"].(float64)))
	if r := b.do(t, "DELETE", "/api/tracks/"+id, ""); r.code != http.StatusNoContent {
		t.Fatalf("delete = %d", r.code)
	}
	if r := b.do(t, "DELETE", "/api/tracks/"+id, ""); r.code != http.StatusNotFound {
		t.Fatalf("second delete = %d", r.code)
	}
}

// Если репозиторий трека пропал, список всё равно отдаётся, а у трека стоит код ошибки.
func TestListTracksKeepsBrokenTrack(t *testing.T) {
	e := newEnv(t)
	e.store.CreateTrack(context.Background(), store.Track{Owner: "MegaRoks", Repo: "deleted", Color: "blue"})
	r := e.loggedIn(t).do(t, "GET", "/api/tracks", "")
	if r.code != http.StatusOK || len(r.arr) != 1 || r.arr[0].(map[string]any)["error"] != "not_found" {
		t.Fatalf("list = %d %v", r.code, r.arr)
	}
}
