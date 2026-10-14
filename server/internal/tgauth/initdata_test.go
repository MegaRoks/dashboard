package tgauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"
	"testing"
	"time"
)

const token = "123456:test-token"

var now = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

func fields(authDate time.Time) url.Values {
	return url.Values{
		"auth_date": {strconv.FormatInt(authDate.Unix(), 10)},
		"query_id":  {"AAH"},
		"user":      {`{"id":42,"first_name":"Максим","username":"megaroks"}`},
	}
}

// Подпись посчитана по шагам из документации Telegram отдельно от signature(),
// чтобы ошибка в порядке ключа и сообщения HMAC не прошла незамеченной.
func TestSignMatchesTelegramAlgorithm(t *testing.T) {
	raw := Sign(fields(now), token)
	vals, _ := url.ParseQuery(raw)

	dataCheck := "auth_date=" + vals.Get("auth_date") + "\nquery_id=AAH\nuser=" + vals.Get("user")
	k := hmac.New(sha256.New, []byte("WebAppData"))
	k.Write([]byte(token))
	h := hmac.New(sha256.New, k.Sum(nil))
	h.Write([]byte(dataCheck))

	if want := hex.EncodeToString(h.Sum(nil)); vals.Get("hash") != want {
		t.Fatalf("hash = %s, want %s", vals.Get("hash"), want)
	}
}

func TestValidate(t *testing.T) {
	d, err := Validate(Sign(fields(now.Add(-time.Hour)), token), token, 24*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if d.User.ID != 42 || d.User.Username != "megaroks" || !d.AuthDate.Equal(now.Add(-time.Hour)) {
		t.Fatalf("unexpected init data: %+v", d)
	}
}

func TestValidateRejects(t *testing.T) {
	valid := Sign(fields(now), token)
	tampered, _ := url.ParseQuery(valid)
	tampered.Set("user", `{"id":1,"first_name":"Мэллори"}`)
	noUser := fields(now)
	noUser.Del("user")

	cases := []struct {
		name string
		raw  string
		tok  string
		want error
	}{
		{"no hash", "auth_date=1&user=%7B%7D", token, ErrNoHash},
		{"not hex", "auth_date=1&hash=zz", token, ErrBadHash},
		{"tampered user", tampered.Encode(), token, ErrBadHash},
		{"other bot", valid, "999:other", ErrBadHash},
		{"expired", Sign(fields(now.Add(-25*time.Hour)), token), token, ErrExpired},
		{"no auth_date", Sign(url.Values{"user": {`{"id":42}`}}, token), token, ErrExpired},
		{"no user", Sign(noUser, token), token, ErrNoUser},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Validate(c.raw, c.tok, 24*time.Hour, now); !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}
