// Command initdata печатает подписанный initData, чтобы войти локально без Telegram:
//
//	curl -c jar -H "Origin: http://localhost:8090" -d "{\"initData\":\"$(go run ./cmd/initdata)\"}" localhost:8090/auth/telegram
//	curl -b jar localhost:8090/api/me
//
// Берёт TELEGRAM_BOT_TOKEN и TELEGRAM_ALLOWED_USER_ID из окружения; -user подставляет другой id.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/MegaRoks/dashboard/server/internal/tgauth"
)

func main() {
	envID, _ := strconv.ParseInt(os.Getenv("TELEGRAM_ALLOWED_USER_ID"), 10, 64)
	id := flag.Int64("user", envID, "Telegram user id")
	flag.Parse()
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" || *id == 0 {
		fmt.Fprintln(os.Stderr, "нужны TELEGRAM_BOT_TOKEN и TELEGRAM_ALLOWED_USER_ID (или -user)")
		os.Exit(1)
	}
	user, _ := json.Marshal(tgauth.User{ID: *id, FirstName: "Dev"})
	fmt.Println(tgauth.Sign(url.Values{
		"auth_date": {strconv.FormatInt(time.Now().Unix(), 10)},
		"user":      {string(user)},
	}, token))
}
