package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestParseDefaults(t *testing.T) {
	c, err := parse(env(map[string]string{"TELEGRAM_BOT_TOKEN": "123:abc", "TELEGRAM_ALLOWED_USER_ID": "42"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != "127.0.0.1:8090" || c.DBPath != "dashboard.db" || c.InitDataTTL != 10*time.Minute || c.SessionTTL != 30*24*time.Hour || c.Origin != "https://dashboard.apisky.org" || c.Location.String() != "Europe/Moscow" {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if c.AllowedUserID != 42 {
		t.Fatalf("AllowedUserID = %d", c.AllowedUserID)
	}
}

func TestParseReportsAllErrors(t *testing.T) {
	_, err := parse(env(map[string]string{"TELEGRAM_ALLOWED_USER_ID": "-1", "INITDATA_TTL": "soon", "SESSION_TTL": "0s", "DASHBOARD_TZ": "Mars/Base"}))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"TELEGRAM_BOT_TOKEN", "TELEGRAM_ALLOWED_USER_ID", "INITDATA_TTL", "SESSION_TTL", "DASHBOARD_TZ"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}
