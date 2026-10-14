// Package scheduler owns one durable, deduplicated queue for manual and scheduled claims.
package scheduler

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/project-base-mirror/epic-claim/internal/claimer"
	"github.com/project-base-mirror/epic-claim/internal/epic"
	"github.com/project-base-mirror/epic-claim/internal/store"
)

type Discovery interface {
	CurrentFreeGames(context.Context) ([]epic.Game, error)
}
type Scheduler struct {
	AccountDelay time.Duration
	Epic         Discovery
	Store        *store.SQLite
	Claimer      claimer.Claimer
	triggerMu    sync.Mutex
}

func (s *Scheduler) Accounts(ctx context.Context) ([]store.Account, error) {
	return s.Store.ListAccounts(ctx)
}
func (s *Scheduler) SaveAccount(ctx context.Context, a store.Account) (store.Account, error) {
	return s.Store.SaveAccount(ctx, a)
}
func (s *Scheduler) Jobs(ctx context.Context) ([]store.Job, error) { return s.Store.Jobs(ctx) }
func (s *Scheduler) FreeGames(ctx context.Context) ([]epic.Game, error) {
	return s.Epic.CurrentFreeGames(ctx)
}
func (s *Scheduler) GetSchedule(ctx context.Context) (store.Schedule, error) {
	return s.Store.GetSchedule(ctx)
}
func (s *Scheduler) SetSchedule(ctx context.Context, v store.Schedule) error {
	s.triggerMu.Lock()
	defer s.triggerMu.Unlock()
	v.NextRun = time.Now().UTC().Add(time.Duration(v.IntervalSeconds) * time.Second)
	return s.Store.SetSchedule(ctx, v)
}
func (s *Scheduler) Retry(ctx context.Context, id int64) error {
	return s.Store.Requeue(ctx, id, false)
}
func (s *Scheduler) Resume(ctx context.Context, id int64) error {
	return s.Store.Requeue(ctx, id, true)
}
func (s *Scheduler) Trigger(ctx context.Context) (int, error) {
	s.triggerMu.Lock()
	defer s.triggerMu.Unlock()
	return s.discover(ctx)
}
func (s *Scheduler) discover(ctx context.Context) (int, error) {
	if s.Claimer == nil {
		return 0, errors.New("claimer is not configured")
	}
	games, err := s.Epic.CurrentFreeGames(ctx)
	if err != nil {
		return 0, errors.New("free-offer discovery failed; retry later")
	}
	accounts, err := s.Store.ListAccounts(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, a := range accounts {
		if a.Disabled {
			continue
		}
		for _, g := range games {
			added, e := s.Store.Enqueue(ctx, a.ID, g)
			if e != nil {
				return count, e
			}
			if added {
				count++
			}
		}
	}
	return count, nil
}
func (s *Scheduler) scheduleTick(ctx context.Context) {
	s.triggerMu.Lock()
	defer s.triggerMu.Unlock()
	v, err := s.Store.GetSchedule(ctx)
	if err != nil || !v.Enabled || time.Now().Before(v.NextRun) {
		return
	}
	// Persist the next run before discovery; missed intervals coalesce into one check.
	v.NextRun = time.Now().UTC().Add(time.Duration(v.IntervalSeconds) * time.Second)
	if err = s.Store.SetSchedule(ctx, v); err != nil {
		log.Print("scheduler: cannot persist next run")
		return
	}
	if _, err = s.discover(ctx); err != nil {
		log.Print("scheduler: offer discovery failed")
	}
}
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	// One goroutine executes jobs. HTTP handlers only enqueue them.
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scheduleTick(ctx)
			j, err := s.Store.Take(ctx)
			if err != nil {
				log.Print("scheduler: queue read failed")
				continue
			}
			if j == nil {
				continue
			}
			s.execute(ctx, *j)
			if s.AccountDelay > 0 {
				timer := time.NewTimer(s.AccountDelay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}
	}
}
func (s *Scheduler) execute(ctx context.Context, j store.Job) {
	status := store.StatusUnconfirmed
	msg := "Worker did not return a verifiable result. Check ownership before resuming."
	next := time.Now().UTC()
	if !j.Game.EndDate.After(time.Now()) {
		status = store.StatusPriceChanged
		msg = "Promotion expired. No order submitted."
	} else if s.Claimer == nil {
		status = store.StatusFailed
		msg = "Claimer is not configured."
	} else {
		results, err := s.Claimer.Claim(ctx, claimer.Request{AccountID: j.AccountID, Games: []epic.Game{j.Game}})
		if err == nil && len(results) == 1 && results[0].OfferID == j.OfferID && results[0].Namespace == j.Namespace {
			status = results[0].Status
			msg = results[0].Message
			// Only worker-confirmed safe failures retry automatically. Ambiguous orders and all human gates stay paused.
			if status == store.StatusFailed && j.Attempts < 3 {
				status = store.StatusPending
				next = next.Add(time.Duration(1<<uint(j.Attempts-1)) * time.Minute)
			}
		}
	}
	// Persist outcome even if the parent was canceled during a browser operation.
	saveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Store.Finish(saveCtx, j, status, msg, next); err != nil {
		log.Print("scheduler: outcome persistence failed; restart reconciliation will require review")
	}
}
