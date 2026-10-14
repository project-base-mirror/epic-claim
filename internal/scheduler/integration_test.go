package scheduler_test

import (
	"context"
	"encoding/json"
	"github.com/project-base-mirror/epic-claim/internal/claimer"
	"github.com/project-base-mirror/epic-claim/internal/epic"
	"github.com/project-base-mirror/epic-claim/internal/scheduler"
	"github.com/project-base-mirror/epic-claim/internal/store"
	"github.com/project-base-mirror/epic-claim/internal/web"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type catalog struct{}

func (catalog) CurrentFreeGames(context.Context) ([]epic.Game, error) {
	return []epic.Game{{ID: "offer", Namespace: "namespace", Title: "Fixture", StartDate: time.Now().Add(-time.Hour), EndDate: time.Now().Add(time.Hour)}}, nil
}

type worker struct{}

func (worker) Claim(_ context.Context, r claimer.Request) ([]claimer.Result, error) {
	return []claimer.Result{{OfferID: r.Games[0].ID, Namespace: r.Games[0].Namespace, Status: store.StatusOwned}}, nil
}
func TestDashboardToDurableQueue(t *testing.T) {
	st, e := store.OpenSQLite(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	service := &scheduler.Scheduler{Store: st, Epic: catalog{}, Claimer: worker{}}
	server := web.New(":0", service, "test-only-token")
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer test-only-token")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		server.Handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body)
		}
		return w
	}
	request("POST", "/api/accounts", `{"name":"Fixture account","disabled":false}`)
	request("POST", "/api/trigger", "")
	request("POST", "/api/trigger", "")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { service.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		var jobs []store.Job
		if e = json.Unmarshal(request("GET", "/api/history", "").Body.Bytes(), &jobs); e != nil {
			t.Fatal(e)
		}
		if len(jobs) != 1 {
			t.Fatalf("dedup failed: %d", len(jobs))
		}
		if jobs[0].Status == store.StatusOwned {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("queue never completed")
}
