// Package scheduler periodically checks free games and queues claims.
package scheduler

import (
	"context"
	"log"
	"time"

	"github.com/project-base-mirror/epic-claim/internal/claimer"
	"github.com/project-base-mirror/epic-claim/internal/epic"
	"github.com/project-base-mirror/epic-claim/internal/notify"
	"github.com/project-base-mirror/epic-claim/internal/store"
)

type Scheduler struct {
	Interval     time.Duration
	AccountDelay time.Duration
	Epic         *epic.Client
	Store        store.Store
	Claimer      claimer.Claimer
	Notifier     notify.Notifier
}

func (s *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	s.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick(ctx)
		}
	}
}

func (s *Scheduler) tick(ctx context.Context) {
	games, err := s.Epic.CurrentFreeGames(ctx)
	if err != nil {
		log.Printf("scheduler: fetch free games: %v", err)
		return
	}
	log.Printf("scheduler: %d free game(s) available", len(games))
	accounts, _ := s.Store.ListAccounts(ctx)
	for i, a := range accounts {
		if a.Disabled {
			continue
		}
		if i > 0 {
			time.Sleep(s.AccountDelay) // TODO: jitter, cancellable
		}
		// TODO: decrypt credentials, skip owned games, retry with backoff, record status, notify.
		_ = a
	}
}
