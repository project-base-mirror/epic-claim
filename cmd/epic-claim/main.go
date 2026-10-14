package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"github.com/project-base-mirror/epic-claim/internal/claimer"
	"github.com/project-base-mirror/epic-claim/internal/config"
	"github.com/project-base-mirror/epic-claim/internal/epic"
	"github.com/project-base-mirror/epic-claim/internal/notify"
	"github.com/project-base-mirror/epic-claim/internal/scheduler"
	"github.com/project-base-mirror/epic-claim/internal/store"
	"github.com/project-base-mirror/epic-claim/internal/web"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "config file path")
	flag.Parse()
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.OpenSQLite(cfg.Database)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	ec := epic.NewClient(cfg.Locale, cfg.Country)
	sch := &scheduler.Scheduler{
		Interval: cfg.Schedule.CheckInterval, AccountDelay: cfg.Schedule.AccountDelay,
		Epic: ec, Store: st,
		Claimer:  &claimer.Exec{Command: cfg.Claimer.Command},
		Notifier: &notify.Telegram{BotToken: cfg.Notify.Telegram.BotToken, ChatID: cfg.Notify.Telegram.ChatID},
	}
	go sch.Run(ctx)

	srv := web.New(cfg.Listen, ec)
	go func() {
		log.Printf("listening on %s", cfg.Listen)
		if err := srv.ListenAndServe(); err != nil {
			log.Printf("http: %v", err)
		}
	}()
	<-ctx.Done()
	srv.Shutdown(context.Background())
}
