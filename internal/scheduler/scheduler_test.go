package scheduler

import (
	"context"
	"errors"
	"github.com/project-base-mirror/epic-claim/internal/claimer"
	"github.com/project-base-mirror/epic-claim/internal/epic"
	"github.com/project-base-mirror/epic-claim/internal/store"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeDiscovery struct {
	games []epic.Game
	err   error
}

func (f fakeDiscovery) CurrentFreeGames(context.Context) ([]epic.Game, error) { return f.games, f.err }

type fakeClaimer struct {
	status store.ClaimStatus
	err    error
}

func (f fakeClaimer) Claim(_ context.Context, r claimer.Request) ([]claimer.Result, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []claimer.Result{{OfferID: r.Games[0].ID, Namespace: r.Games[0].Namespace, Status: f.status, Message: "test"}}, nil
}
func setup(t *testing.T) *Scheduler {
	t.Helper()
	st, e := store.OpenSQLite(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { st.Close() })
	st.SaveAccount(context.Background(), store.Account{Name: "Test"})
	return &Scheduler{Store: st, Epic: fakeDiscovery{games: []epic.Game{{ID: "offer", Namespace: "ns", Title: "Title", StartDate: time.Now().Add(-time.Hour), EndDate: time.Now().Add(time.Hour)}}}, Claimer: fakeClaimer{status: store.StatusClaimed}}
}
func TestConcurrentManualScheduledDedup(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	s.Store.SetSchedule(ctx, store.Schedule{Enabled: true, IntervalSeconds: 60})
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.Trigger(ctx); s.scheduleTick(ctx) }()
	}
	wg.Wait()
	jobs, _ := s.Jobs(ctx)
	if len(jobs) != 1 {
		t.Fatalf("duplicate jobs: %d", len(jobs))
	}
}
func TestAmbiguousFailureIsNeverRetried(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	s.Claimer = fakeClaimer{err: errors.New("timeout")}
	s.Trigger(ctx)
	j, _ := s.Store.Take(ctx)
	s.execute(ctx, *j)
	jobs, _ := s.Jobs(ctx)
	if jobs[0].Status != store.StatusUnconfirmed {
		t.Fatal(jobs[0])
	}
	if j, _ := s.Store.Take(ctx); j != nil {
		t.Fatal("ambiguous retry")
	}
}
func TestSafeFailureBackoffBounded(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	s.Claimer = fakeClaimer{status: store.StatusFailed}
	s.Trigger(ctx)
	j, _ := s.Store.Take(ctx)
	s.execute(ctx, *j)
	jobs, _ := s.Jobs(ctx)
	if jobs[0].Status != store.StatusPending || !jobs[0].NextAttempt.After(time.Now()) {
		t.Fatal(jobs[0])
	}
}
func TestHumanGatesNotAutomaticallyRetried(t *testing.T) {
	for _, status := range []store.ClaimStatus{store.StatusUnsupported, store.StatusCaptchaRequired, store.StatusLoginRequired, store.StatusTermsRequired, store.StatusTwoFactorRequired, store.StatusPriceChanged} {
		s := setup(t)
		ctx := context.Background()
		s.Claimer = fakeClaimer{status: status}
		s.Trigger(ctx)
		j, _ := s.Store.Take(ctx)
		s.execute(ctx, *j)
		jobs, _ := s.Jobs(ctx)
		if jobs[0].Status != status {
			t.Fatal(jobs[0])
		}
		if j, _ := s.Store.Take(ctx); j != nil {
			t.Fatal("human gate retried")
		}
	}
}
