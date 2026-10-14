// Package tgauth проверяет initData, который Telegram передаёт в Mini App.
// Алгоритм: https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app
package tgauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

var (
	ErrNoHash  = errors.New("tgauth: hash is missing")
	ErrBadHash = errors.New("tgauth: signature mismatch")
	ErrExpired = errors.New("tgauth: init data expired")
	ErrNoUser  = errors.New("tgauth: user is missing")
)

type User struct {
	ID           int64  `json:"id"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name,omitempty"`
	Username     string `json:"username,omitempty"`
	LanguageCode string `json:"language_code,omitempty"`
}

type InitData struct {
	User     User
	AuthDate time.Time
}

// Validate проверяет подпись initData токеном бота и его возраст на момент now.
func Validate(raw, botToken string, maxAge time.Duration, now time.Time) (InitData, error) {
	vals, err := url.ParseQuery(raw)
	if err != nil {
		return InitData{}, fmt.Errorf("tgauth: %w", err)
	}
	hash := vals.Get("hash")
	if hash == "" {
		return InitData{}, ErrNoHash
	}
	got, err := hex.DecodeString(hash)
	if err != nil || !hmac.Equal(got, signature(vals, botToken)) {
		return InitData{}, ErrBadHash
	}
	// Подпись не стареет, поэтому без срока перехваченный initData работал бы вечно
	sec, err := strconv.ParseInt(vals.Get("auth_date"), 10, 64)
	if err != nil {
		return InitData{}, ErrExpired
	}
	authDate := time.Unix(sec, 0)
	if now.Sub(authDate) > maxAge {
		return InitData{}, ErrExpired
	}
	var u User
	if err := json.Unmarshal([]byte(vals.Get("user")), &u); err != nil || u.ID == 0 {
		return InitData{}, ErrNoUser
	}
	return InitData{User: u, AuthDate: authDate}, nil
}

// Sign подписывает поля так же, как Telegram, и возвращает готовую строку initData.
// Нужен для тестов и локальной разработки без Telegram.
func Sign(vals url.Values, botToken string) string {
	v := url.Values{}
	for k, vs := range vals {
		if k != "hash" {
			v[k] = vs
		}
	}
	v.Set("hash", hex.EncodeToString(signature(v, botToken)))
	return v.Encode()
}

// signature: data-check-string — все поля, кроме hash, по алфавиту, «key=value» через \n;
// secret_key = HMAC_SHA256(key="WebAppData", msg=bot_token); hash = HMAC_SHA256(key=secret_key, msg=data-check-string).
func signature(vals url.Values, botToken string) []byte {
	keys := make([]string, 0, len(vals))
	for k := range vals {
		if k != "hash" {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	lines := make([]string, len(keys))
	for i, k := range keys {
		lines[i] = k + "=" + vals.Get(k)
	}
	secret := mac([]byte("WebAppData"), []byte(botToken))
	return mac(secret, []byte(strings.Join(lines, "\n")))
}

func mac(key, msg []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(msg)
	return h.Sum(nil)
}
