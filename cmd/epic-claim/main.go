package main

import (
	"context"
	"flag"
	"github.com/project-base-mirror/epic-claim/internal/claimer"
	"github.com/project-base-mirror/epic-claim/internal/config"
	"github.com/project-base-mirror/epic-claim/internal/epic"
	"github.com/project-base-mirror/epic-claim/internal/login"
	"github.com/project-base-mirror/epic-claim/internal/secret"
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
	// The API token is optional (scripts); people log in with username/password.
	token := os.Getenv("EPIC_ADMIN_TOKEN")
	if token != "" && len(token) < 32 {
		log.Fatal("EPIC_ADMIN_TOKEN, when set, must contain at least 32 characters")
	}
	var box *secret.Box
	if k := os.Getenv("EPIC_SECRET_KEY"); k != "" {
		key, err := secret.ParseKey(k)
		if err != nil {
			log.Fatal(err)
		}
		if box, err = secret.New(key); err != nil {
			log.Fatal(err)
		}
	} else {
		log.Print("EPIC_SECRET_KEY not set: Epic credentials cannot be stored or used for web login")
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
	runner := &login.Runner{Command: cfg.Login.Command, Timeout: cfg.Login.Timeout, Store: st}
	srv := web.New(cfg.Listen, sch, token, web.Options{Auth: st, Secrets: box, Login: runner, AllowRegistrationDefault: cfg.AllowRegistration})
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
