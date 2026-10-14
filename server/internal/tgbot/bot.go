// Package tgbot — бот дашборда: запросы на вход из браузера и служебные команды.
//
// Бот разговаривает только с одним пользователем (AllowedUserID): запросы на вход
// уходят ему в личный чат, а кнопки и команды от любых других id игнорируются.
// Апдейты забираются long polling (getUpdates), поэтому бот не открывает своих эндпоинтов.
package tgbot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/MegaRoks/dashboard/server/internal/store"
)

const pollTimeout = 50 // секунд, long polling getUpdates

type Bot struct {
	Token         string
	BaseURL       string // https://api.telegram.org
	HTTP          *http.Client
	AllowedUserID int64
	Store         *store.Store
	Log           *slog.Logger
	Now           func() time.Time
}

func New(token string, allowedUserID int64, st *store.Store, log *slog.Logger) *Bot {
	return &Bot{
		Token:         token,
		BaseURL:       "https://api.telegram.org",
		HTTP:          &http.Client{Timeout: (pollTimeout + 20) * time.Second},
		AllowedUserID: allowedUserID,
		Store:         st,
		Log:           log,
		Now:           time.Now,
	}
}

// NotifyLogin присылает владельцу запрос на вход с кнопками «Подтвердить» и «Это не я».
func (b *Bot) NotifyLogin(ctx context.Context, req store.LoginRequest) error {
	text := fmt.Sprintf("🔐 Вход в дашборд в браузере\n\nКод: %s\nУстройство: %s\nIP: %s\n\nПодтверждайте, только если код совпадает с кодом на экране.",
		req.Code, DescribeUserAgent(req.UserAgent), req.IP)
	return b.call(ctx, "sendMessage", map[string]any{
		"chat_id": b.AllowedUserID,
		"text":    text,
		"reply_markup": map[string]any{"inline_keyboard": [][]map[string]string{{
			{"text": "✅ Подтвердить", "callback_data": "login:approve:" + req.ID},
			{"text": "⛔ Это не я", "callback_data": "login:deny:" + req.ID},
		}}},
	}, nil)
}

// Run забирает апдейты, пока не отменён ctx.
func (b *Bot) Run(ctx context.Context) {
	var offset int64
	for ctx.Err() == nil {
		var updates []update
		err := b.call(ctx, "getUpdates", map[string]any{
			"offset": offset, "timeout": pollTimeout, "allowed_updates": []string{"message", "callback_query"},
		}, &updates)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			// 409 — у бота настроен webhook или его опрашивает другой процесс
			b.Log.Error("telegram getUpdates failed", "err", err)
			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
			}
			continue
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			b.handle(ctx, u)
		}
	}
}

type user struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type message struct {
	MessageID int64 `json:"message_id"`
	From      *user `json:"from"`
	Chat      struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	} `json:"chat"`
	Text string `json:"text"`
}

type callbackQuery struct {
	ID      string   `json:"id"`
	From    user     `json:"from"`
	Message *message `json:"message"`
	Data    string   `json:"data"`
}

type update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *message       `json:"message"`
	CallbackQuery *callbackQuery `json:"callback_query"`
}

func (b *Bot) handle(ctx context.Context, u update) {
	switch {
	case u.CallbackQuery != nil:
		b.onCallback(ctx, *u.CallbackQuery)
	case u.Message != nil:
		b.onMessage(ctx, *u.Message)
	}
}

func (b *Bot) onCallback(ctx context.Context, q callbackQuery) {
	if q.From.ID != b.AllowedUserID {
		b.Log.Warn("callback from stranger ignored", "user_id", q.From.ID, "username", q.From.Username)
		b.answer(ctx, q.ID, "")
		return
	}
	action, id, ok := strings.Cut(strings.TrimPrefix(q.Data, "login:"), ":")
	status := map[string]store.LoginStatus{"approve": store.LoginApproved, "deny": store.LoginDenied}[action]
	if !strings.HasPrefix(q.Data, "login:") || !ok || status == "" {
		b.answer(ctx, q.ID, "")
		return
	}

	req, err := b.Store.DecideLogin(ctx, id, status, b.Now())
	var text, toast string
	switch {
	case errors.Is(err, store.ErrNotFound):
		text, toast = "⌛ Запрос на вход устарел или уже обработан", "Запрос устарел"
	case err != nil:
		b.Log.Error("decide login failed", "err", err)
		b.answer(ctx, q.ID, "Ошибка, попробуйте ещё раз")
		return
	case status == store.LoginApproved:
		text, toast = fmt.Sprintf("✅ Вход подтверждён\nКод: %s · %s · %s", req.Code, DescribeUserAgent(req.UserAgent), req.IP), "Вход подтверждён"
	default:
		b.Log.Warn("login denied by owner", "ip", req.IP, "user_agent", req.UserAgent)
		text, toast = fmt.Sprintf("⛔ Вход отклонён\nКод: %s · %s · %s\n\nЕсли кто-то пытается войти без вас — /logout_all завершит все сессии.", req.Code, DescribeUserAgent(req.UserAgent), req.IP), "Вход отклонён"
	}
	if q.Message != nil {
		b.callLogged(ctx, "editMessageText", map[string]any{"chat_id": q.Message.Chat.ID, "message_id": q.Message.MessageID, "text": text})
	}
	b.answer(ctx, q.ID, toast)
}

func (b *Bot) onMessage(ctx context.Context, m message) {
	// Чужим не отвечаем вовсе: бот не должен подтверждать, что он что-то умеет
	if m.From == nil || m.From.ID != b.AllowedUserID || m.Chat.Type != "private" {
		return
	}
	cmd, _, _ := strings.Cut(strings.TrimSpace(m.Text), " ")
	cmd, _, _ = strings.Cut(cmd, "@") // /start@bot_name в групповом формате
	switch cmd {
	case "/start", "/help":
		b.reply(ctx, m.Chat.ID, "Бот личного дашборда: сюда приходят запросы на вход из браузера.\n\n/logout_all — завершить все сессии на всех устройствах")
	case "/logout_all":
		n, err := b.Store.DeleteAllSessions(ctx)
		if err != nil {
			b.Log.Error("logout all failed", "err", err)
			b.reply(ctx, m.Chat.ID, "Не удалось завершить сессии, попробуйте ещё раз")
			return
		}
		b.Log.Warn("all sessions revoked by owner", "count", n)
		b.reply(ctx, m.Chat.ID, fmt.Sprintf("Завершено сессий: %d. Войти заново можно через Telegram или с подтверждением здесь.", n))
	}
}

func (b *Bot) reply(ctx context.Context, chatID int64, text string) {
	b.callLogged(ctx, "sendMessage", map[string]any{"chat_id": chatID, "text": text})
}

func (b *Bot) answer(ctx context.Context, queryID, text string) {
	b.callLogged(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": queryID, "text": text})
}

func (b *Bot) callLogged(ctx context.Context, method string, params map[string]any) {
	if err := b.call(ctx, method, params, nil); err != nil {
		b.Log.Error("telegram call failed", "method", method, "err", err)
	}
}

// call вызывает метод Bot API; result — куда разобрать поле result (nil — не нужно).
func (b *Bot) call(ctx context.Context, method string, params map[string]any, result any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.BaseURL+"/bot"+b.Token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.HTTP.Do(req)
	if err != nil {
		// в тексте ошибки URL с токеном — не пишем его в логи
		return fmt.Errorf("telegram %s: request failed", method)
	}
	defer resp.Body.Close()
	var out struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("telegram %s: %s: %w", method, resp.Status, err)
	}
	if !out.OK {
		return fmt.Errorf("telegram %s: %s: %s", method, resp.Status, out.Description)
	}
	if result != nil {
		return json.Unmarshal(out.Result, result)
	}
	return nil
}
