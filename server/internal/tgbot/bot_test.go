package tgbot

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MegaRoks/dashboard/server/internal/store"
)

const owner = 42

var t0 = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

type sent struct {
	method string
	params map[string]any
}

// fakeTelegram записывает вызовы Bot API и отвечает ok.
type fakeTelegram struct {
	mu    sync.Mutex
	calls []sent
}

func (f *fakeTelegram) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var p map[string]any
	json.NewDecoder(r.Body).Decode(&p)
	f.mu.Lock()
	f.calls = append(f.calls, sent{r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], p})
	f.mu.Unlock()
	io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
}

func (f *fakeTelegram) methods() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		out = append(out, c.method)
	}
	return out
}

func newBot(t *testing.T) (*Bot, *fakeTelegram, *store.Store) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	tg := &fakeTelegram{}
	srv := httptest.NewServer(tg)
	t.Cleanup(srv.Close)
	b := New("123:token", owner, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	b.BaseURL, b.Now = srv.URL, func() time.Time { return t0 }
	return b, tg, st
}

func pendingLogin(t *testing.T, st *store.Store, id string) {
	t.Helper()
	err := st.CreateLogin(context.Background(), store.LoginRequest{ID: id, Code: "4821", Status: store.LoginPending,
		UserAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Chrome/141.0 Safari/537.36", IP: "1.2.3.4",
		CreatedAt: t0, ExpiresAt: t0.Add(5 * time.Minute)}, "secret")
	if err != nil {
		t.Fatal(err)
	}
}

func status(t *testing.T, st *store.Store, id string) store.LoginStatus {
	req, err := st.LoginForBrowser(context.Background(), id, "secret")
	if err != nil {
		t.Fatal(err)
	}
	return req.Status
}

func TestNotifyLoginGoesOnlyToOwner(t *testing.T) {
	b, tg, _ := newBot(t)
	err := b.NotifyLogin(context.Background(), store.LoginRequest{ID: "req1", Code: "4821", IP: "1.2.3.4",
		UserAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Chrome/141.0 Safari/537.36"})
	if err != nil {
		t.Fatal(err)
	}
	c := tg.calls[0]
	text, _ := c.params["text"].(string)
	if c.method != "sendMessage" || c.params["chat_id"] != float64(owner) {
		t.Fatalf("call = %s %v", c.method, c.params)
	}
	for _, want := range []string{"4821", "Chrome · macOS", "1.2.3.4"} {
		if !strings.Contains(text, want) {
			t.Errorf("message %q lacks %q", text, want)
		}
	}
	if kb, _ := json.Marshal(c.params["reply_markup"]); !strings.Contains(string(kb), "login:approve:req1") || !strings.Contains(string(kb), "login:deny:req1") {
		t.Fatalf("keyboard = %s", kb)
	}
}

func callback(from int64, data string) update {
	return update{CallbackQuery: &callbackQuery{ID: "q", From: user{ID: from}, Data: data,
		Message: &message{MessageID: 7, Chat: struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		}{owner, "private"}}}}
}

func TestCallbackDecidesLogin(t *testing.T) {
	b, tg, st := newBot(t)
	pendingLogin(t, st, "req1")
	pendingLogin(t, st, "req2")
	ctx := context.Background()

	b.handle(ctx, callback(owner, "login:approve:req1"))
	if s := status(t, st, "req1"); s != store.LoginApproved {
		t.Fatalf("req1 = %s, want approved", s)
	}
	if m := tg.methods(); len(m) != 2 || m[0] != "editMessageText" || m[1] != "answerCallbackQuery" {
		t.Fatalf("calls = %v", m)
	}

	b.handle(ctx, callback(owner, "login:deny:req2"))
	if s := status(t, st, "req2"); s != store.LoginDenied {
		t.Fatalf("req2 = %s, want denied", s)
	}

	// повторное нажатие не меняет решение
	b.handle(ctx, callback(owner, "login:deny:req1"))
	if s := status(t, st, "req1"); s != store.LoginApproved {
		t.Fatalf("req1 after second press = %s", s)
	}
}

func TestStrangerCannotApprove(t *testing.T) {
	b, tg, st := newBot(t)
	pendingLogin(t, st, "req1")
	b.handle(context.Background(), callback(7, "login:approve:req1"))
	if s := status(t, st, "req1"); s != store.LoginPending {
		t.Fatalf("stranger changed status to %s", s)
	}
	if m := tg.methods(); len(m) != 1 || m[0] != "answerCallbackQuery" {
		t.Fatalf("calls = %v; stranger must only get a silent answer", m)
	}
}

func cmd(from int64, chatType, text string) update {
	m := &message{From: &user{ID: from}, Text: text}
	m.Chat.ID, m.Chat.Type = from, chatType
	return update{Message: m}
}

func TestCommands(t *testing.T) {
	b, tg, st := newBot(t)
	ctx := context.Background()
	st.CreateSession(ctx, store.Session{UserID: owner, Via: "browser", CreatedAt: t0, LastSeenAt: t0, ExpiresAt: t0.Add(time.Hour)}, "tok")

	b.handle(ctx, cmd(7, "private", "/logout_all"))
	b.handle(ctx, cmd(owner, "group", "/logout_all"))
	if _, err := st.SessionByToken(ctx, "tok", t0); err != nil {
		t.Fatalf("stranger or group revoked sessions: %v", err)
	}
	if len(tg.methods()) != 0 {
		t.Fatalf("bot answered a stranger: %v", tg.methods())
	}

	b.handle(ctx, cmd(owner, "private", "/logout_all"))
	if _, err := st.SessionByToken(ctx, "tok", t0); err == nil {
		t.Fatal("session survived /logout_all")
	}
	if text := tg.calls[0].params["text"].(string); !strings.Contains(text, "Завершено сессий: 1") {
		t.Fatalf("reply = %q", text)
	}
}

func TestDescribeUserAgent(t *testing.T) {
	cases := map[string]string{
		"Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Version/18.0 Mobile/15E148 Safari/604.1": "Safari · iOS",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/141.0 Safari/537.36 Edg/141.0":                   "Edge · Windows",
		"Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0":                                              "Firefox · Linux",
		"curl/8.7.1": "curl",
		"":           "неизвестное устройство",
	}
	for ua, want := range cases {
		if got := DescribeUserAgent(ua); got != want {
			t.Errorf("DescribeUserAgent(%q) = %q, want %q", ua, got, want)
		}
	}
}
