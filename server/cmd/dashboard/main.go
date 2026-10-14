// Command dashboard — бэкенд личного дашборда (Telegram Mini App).
// Настройки — переменные окружения, см. server/.env.example.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/MegaRoks/dashboard/server/internal/config"
	"github.com/MegaRoks/dashboard/server/internal/github"
	"github.com/MegaRoks/dashboard/server/internal/httpapi"
	"github.com/MegaRoks/dashboard/server/internal/store"
	"github.com/MegaRoks/dashboard/server/internal/tgbot"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	if cfg.GitHubToken == "" {
		log.Warn("GITHUB_TOKEN is empty: only public repositories are available")
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	bot := tgbot.New(cfg.BotToken, cfg.AllowedUserID, st, log)
	go bot.Run(ctx)

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: httpapi.New(httpapi.Deps{
			Store:         st,
			GitHub:        github.New(cfg.GitHubToken),
			Bot:           bot,
			BotToken:      cfg.BotToken,
			AllowedUserID: cfg.AllowedUserID,
			InitDataTTL:   cfg.InitDataTTL,
			SessionTTL:    cfg.SessionTTL,
			Origin:        cfg.Origin,
			Location:      cfg.Location,
			Log:           log,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Addr, "origin", cfg.Origin, "db", cfg.DBPath, "tz", cfg.Location.String())
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
