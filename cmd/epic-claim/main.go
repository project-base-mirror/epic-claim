package main

import (
	"context"
	"flag"
	"github.com/project-base-mirror/epic-claim/internal/claimer"
	"github.com/project-base-mirror/epic-claim/internal/config"
	"github.com/project-base-mirror/epic-claim/internal/epic"
	"github.com/project-base-mirror/epic-claim/internal/scheduler"
	"github.com/project-base-mirror/epic-claim/internal/store"
	"github.com/project-base-mirror/epic-claim/internal/web"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	path := flag.String("config", "config.yaml", "config file")
	flag.Parse()
	cfg, err := config.Load(*path)
	if err != nil {
		log.Fatal(err)
	}
	token := os.Getenv("EPIC_ADMIN_TOKEN")
	if len(token) < 32 {
		log.Fatal("EPIC_ADMIN_TOKEN must contain at least 32 characters")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	st, err := store.OpenSQLite(cfg.Database, cfg.Schedule.CheckInterval)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()
	ec := epic.NewClient(cfg.Locale, cfg.Country)
	ec.Endpoint = cfg.EpicEndpoint
	sch := &scheduler.Scheduler{Epic: ec, Store: st, Claimer: &claimer.Exec{Command: cfg.Claimer.Command, Timeout: cfg.Claimer.Timeout}, AccountDelay: cfg.Schedule.AccountDelay}
	done := make(chan struct{})
	go func() { defer close(done); sch.Run(ctx) }()
	srv := web.New(cfg.Listen, sch, token)
	go func() {
		log.Printf("admin listening on %s", cfg.Listen)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Print("HTTP server stopped unexpectedly")
			stop()
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	<-done
}
