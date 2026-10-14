package store

import (
	"context"
	"encoding/json"
	"github.com/project-base-mirror/epic-claim/internal/epic"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func game() epic.Game {
	return epic.Game{ID: "offer", Namespace: "namespace", Title: "Game", StartDate: time.Now().Add(-time.Hour), EndDate: time.Now().Add(time.Hour)}
}
func setup(t *testing.T) (*SQLite, Account) {
	t.Helper()
	s, e := OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	a, e := s.SaveAccount(context.Background(), Account{Name: "Test"})
	if e != nil {
		t.Fatal(e)
	}
	return s, a
}
func TestConcurrentDedup(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.Enqueue(ctx, a.ID, game()); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	jobs, e := s.Jobs(ctx)
	if e != nil || len(jobs) != 1 {
		t.Fatalf("%v %+v", e, jobs)
	}
	j, e := s.Take(ctx)
	if e != nil || j == nil {
		t.Fatal(e)
	}
	if second, _ := s.Take(ctx); second != nil {
		t.Fatal("duplicate execution")
	}
}
func TestBlockedRequiresResume(t *testing.T) {
	for _, status := range []ClaimStatus{StatusUnsupported, StatusLoginRequired, StatusCaptchaRequired, StatusTwoFactorRequired, StatusTermsRequired, StatusPriceChanged, StatusUnconfirmed} {
		t.Run(string(status), func(t *testing.T) {
			s, a := setup(t)
			ctx := context.Background()
			s.Enqueue(ctx, a.ID, game())
			j, _ := s.Take(ctx)
			if e := s.Finish(ctx, *j, status, "paused", time.Now()); e != nil {
				t.Fatal(e)
			}
			s.Enqueue(ctx, a.ID, game())
			if got, _ := s.Take(ctx); got != nil {
				t.Fatal("blocked auto retry")
			}
			if e := s.Requeue(ctx, j.ID, false); e == nil {
				t.Fatal("ordinary retry bypassed blocked state")
			}
			if e := s.Requeue(ctx, j.ID, true); e != nil {
				t.Fatal(e)
			}
			if got, _ := s.Take(ctx); got == nil {
				t.Fatal("resume did not queue")
			}
		})
	}
}
func TestRestartReconciliationAndLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, e := OpenSQLite(path)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	a, _ := s.SaveAccount(ctx, Account{Name: "Account"})
	s.Enqueue(ctx, a.ID, game())
	s.Take(ctx)
	if second, e := OpenSQLite(path); e == nil {
		second.Close()
		t.Fatal("second process permitted")
	}
	s.Close()
	s, e = OpenSQLite(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	jobs, _ := s.Jobs(ctx)
	if jobs[0].Status != StatusUnconfirmed {
		t.Fatalf("unsafe restart: %+v", jobs)
	}
	if j, _ := s.Take(ctx); j != nil {
		t.Fatal("replayed ambiguous order")
	}
}
func TestDisabledExpiredAndScheduleValidation(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	a.Disabled = true
	s.SaveAccount(ctx, a)
	if ok, _ := s.Enqueue(ctx, a.ID, game()); ok {
		t.Fatal("disabled queued")
	}
	a.Disabled = false
	s.SaveAccount(ctx, a)
	g := game()
	g.EndDate = time.Now().Add(-time.Second)
	if ok, _ := s.Enqueue(ctx, a.ID, g); ok {
		t.Fatal("expired queued")
	}
	if e := s.SetSchedule(ctx, Schedule{IntervalSeconds: 0}); e == nil {
		t.Fatal("invalid interval")
	}
	v, _ := s.GetSchedule(ctx)
	if v.Enabled {
		t.Fatal("schedule enabled without opt in")
	}
}
func TestCompletedNeverRequeued(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	s.Enqueue(ctx, a.ID, game())
	j, _ := s.Take(ctx)
	s.Finish(ctx, *j, StatusClaimed, "verified", time.Now())
	if ok, _ := s.Enqueue(ctx, a.ID, game()); ok {
		t.Fatal("claimed duplicated")
	}
	if e := s.Requeue(ctx, j.ID, true); e == nil {
		t.Fatal("claimed resumed")
	}
}

func TestLaterPromotionRefreshPreservesHumanGates(t *testing.T) {
	for _, status := range []ClaimStatus{StatusPending, StatusUnsupported, StatusTwoFactorRequired, StatusFailed, StatusUnconfirmed, StatusLoginRequired, StatusCaptchaRequired, StatusTermsRequired, StatusPriceChanged, StatusClaimed, StatusOwned, StatusRunning} {
		t.Run(string(status), func(t *testing.T) {
			s, a := setup(t)
			ctx := context.Background()
			s.Enqueue(ctx, a.ID, game())
			j, _ := s.Take(ctx)
			old := game()
			old.StartDate = time.Now().Add(-72 * time.Hour)
			old.EndDate = time.Now().Add(-48 * time.Hour)
			raw, _ := json.Marshal(old)
			if _, e := s.db.Exec(`UPDATE jobs SET status=?,game=? WHERE id=?`, status, string(raw), j.ID); e != nil {
				t.Fatal(e)
			}
			fresh := game()
			added, e := s.Enqueue(ctx, a.ID, fresh)
			if e != nil {
				t.Fatal(e)
			}
			jobs, _ := s.Jobs(ctx)
			got := jobs[0]
			if status == StatusFailed {
				if !added || got.Status != StatusPending || got.Attempts != 0 {
					t.Fatalf("new promotion not revived: %+v", got)
				}
			} else if added || got.Status != status {
				t.Fatalf("status bypass: %+v", got)
			}
			unchanged := status == StatusClaimed || status == StatusOwned || status == StatusRunning
			if unchanged != got.Game.EndDate.Equal(old.EndDate) {
				t.Fatalf("wrong promotion refresh: %+v", got.Game)
			}
			if !unchanged && !got.Game.EndDate.Equal(fresh.EndDate) {
				t.Fatal("stale promotion retained")
			}
		})
	}
}

func TestRediscoveryDoesNotReviveSameOrOverlappingPromotion(t *testing.T) {
	for _, overlap := range []bool{false, true} {
		s, a := setup(t)
		ctx := context.Background()
		original := game()
		s.Enqueue(ctx, a.ID, original)
		j, _ := s.Take(ctx)
		s.Finish(ctx, *j, StatusFailed, "failed", time.Now())
		next := original
		if overlap {
			original.StartDate = time.Now().Add(-72 * time.Hour)
			original.EndDate = time.Now().Add(-time.Hour)
			raw, _ := json.Marshal(original)
			s.db.Exec(`UPDATE jobs SET game=? WHERE id=?`, string(raw), j.ID)
			next.StartDate = original.StartDate
			next.EndDate = time.Now().Add(time.Hour)
		}
		if queued, e := s.Enqueue(ctx, a.ID, next); e != nil || queued {
			t.Fatalf("invalid revival %v %v", queued, e)
		}
		jobs, _ := s.Jobs(ctx)
		if jobs[0].Status != StatusFailed || !jobs[0].Game.EndDate.Equal(original.EndDate) {
			t.Fatal("rediscovery changed failed job")
		}
	}
}
