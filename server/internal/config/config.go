// Package config читает настройки сервиса из переменных окружения.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
	_ "time/tzdata" // часовые пояса внутри бинарника: не зависим от tzdata на сервере
)

type Config struct {
	Addr          string         // адрес HTTP-сервера; за nginx — только localhost
	DBPath        string         // файл SQLite
	BotToken      string         // токен бота: им подписан initData
	AllowedUserID int64          // единственный Telegram user id, которому отдаются данные
	GitHubToken   string         // fine-grained PAT: Contents и Metadata на чтение
	InitDataTTL   time.Duration  // максимальный возраст initData: он нужен только в момент входа
	SessionTTL    time.Duration  // срок сессии, продлевается при использовании
	Origin        string         // адрес дашборда: cookie и проверка Origin привязаны к нему
	Location      *time.Location // часовой пояс для «сегодня» и серий
}

func FromEnv() (Config, error) { return parse(os.Getenv) }

func parse(getenv func(string) string) (Config, error) {
	c := Config{
		Addr:        or(getenv("DASHBOARD_ADDR"), "127.0.0.1:8090"),
		DBPath:      or(getenv("DASHBOARD_DB"), "dashboard.db"),
		BotToken:    getenv("TELEGRAM_BOT_TOKEN"),
		GitHubToken: getenv("GITHUB_TOKEN"),
		Origin:      or(getenv("DASHBOARD_ORIGIN"), "https://dashboard.apisky.org"),
	}
	var errs []error
	if c.BotToken == "" {
		errs = append(errs, errors.New("TELEGRAM_BOT_TOKEN is required"))
	}
	id, err := strconv.ParseInt(getenv("TELEGRAM_ALLOWED_USER_ID"), 10, 64)
	if err != nil || id <= 0 {
		errs = append(errs, errors.New("TELEGRAM_ALLOWED_USER_ID must be a positive integer"))
	}
	c.AllowedUserID = id
	c.InitDataTTL = duration(getenv, "INITDATA_TTL", "10m", &errs)
	c.SessionTTL = duration(getenv, "SESSION_TTL", "720h", &errs)
	loc, err := time.LoadLocation(or(getenv("DASHBOARD_TZ"), "Europe/Moscow"))
	if err != nil {
		errs = append(errs, fmt.Errorf("DASHBOARD_TZ: %w", err))
	}
	c.Location = loc
	return c, errors.Join(errs...)
}

func duration(getenv func(string) string, key, def string, errs *[]error) time.Duration {
	d, err := time.ParseDuration(or(getenv(key), def))
	if err != nil || d <= 0 {
		*errs = append(*errs, fmt.Errorf("%s: invalid duration %q", key, getenv(key)))
	}
	return d
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
