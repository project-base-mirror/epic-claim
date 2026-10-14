package web

import (
	"context"
	"github.com/project-base-mirror/epic-claim/internal/epic"
	"github.com/project-base-mirror/epic-claim/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type mockService struct {
	saved             store.Account
	schedule          store.Schedule
	err               error
	triggered         int
	retryID, resumeID int64
}

func (m *mockService) Accounts(context.Context) ([]store.Account, error) {
	return []store.Account{{ID: 1, Name: "主账户"}}, m.err
}
func (m *mockService) SaveAccount(_ context.Context, a store.Account) (store.Account, error) {
	m.saved = a
	return a, m.err
}
func (m *mockService) Jobs(context.Context) ([]store.Job, error)           { return []store.Job{}, m.err }
func (m *mockService) GetSchedule(context.Context) (store.Schedule, error) { return m.schedule, m.err }
func (m *mockService) SetSchedule(_ context.Context, s store.Schedule) error {
	m.schedule = s
	return m.err
}
func (m *mockService) Trigger(context.Context) (int, error)           { m.triggered++; return 2, m.err }
func (m *mockService) Retry(_ context.Context, id int64) error        { m.retryID = id; return m.err }
func (m *mockService) Resume(_ context.Context, id int64) error       { m.resumeID = id; return m.err }
func (m *mockService) FreeGames(context.Context) ([]epic.Game, error) { return []epic.Game{}, m.err }
func req(h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestAuthenticationAndHeaders(t *testing.T) {
	h := New(":0", &mockService{}, "test-admin").Handler
	for _, p := range []string{"/api/status", "/api/accounts", "/api/history", "/api/freegames", "/api/unknown"} {
		if w := req(h, "GET", p, "", ""); w.Code != 401 {
			t.Errorf("%s unauth=%d", p, w.Code)
		}
		if w := req(h, "GET", p, "wrong", ""); w.Code != 401 {
			t.Errorf("%s wrong=%d", p, w.Code)
		}
	}
	for _, p := range []string{"/", "/app.js", "/app.css", "/healthz"} {
		w := req(h, "GET", p, "", "")
		if w.Code != 200 {
			t.Errorf("%s=%d", p, w.Code)
		}
		if w.Header().Get("Content-Security-Policy") == "" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Error("missing security headers")
		}
	}
	w := req(h, "GET", "/api/accounts", "test-admin", "")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := req(New(":0", &mockService{}, "").Handler, "GET", "/api/status", "x", ""); w.Code != 401 {
		t.Fatal("empty token must fail closed")
	}
}
func TestMutations(t *testing.T) {
	m := &mockService{}
	h := New(":0", m, "secret").Handler
	cases := []struct {
		method, path, body string
		code               int
	}{{"POST", "/api/accounts", `{"name":"主账户","disabled":false}`, 200}, {"PUT", "/api/accounts/7", `{"name":"备用账户","disabled":true}`, 200}, {"PUT", "/api/accounts/no", `{"name":"x"}`, 400}, {"PUT", "/api/accounts/0", `{"name":"x"}`, 400}, {"POST", "/api/accounts", `{"name":""}`, 400}, {"POST", "/api/accounts", `{"name":"x","password":"no"}`, 400}, {"POST", "/api/accounts", `{"name":"x"}{}`, 400}, {"PUT", "/api/schedule", `{"enabled":true,"interval_seconds":3600}`, 200}, {"PUT", "/api/schedule", `{"interval_seconds":0}`, 400}, {"POST", "/api/trigger", "", 200}, {"POST", "/api/jobs/3/retry", "", 200}, {"POST", "/api/jobs/4/resume", "", 200}}
	for _, c := range cases {
		if w := req(h, c.method, c.path, "secret", c.body); w.Code != c.code {
			t.Errorf("%s %s=%d want %d %s", c.method, c.path, w.Code, c.code, w.Body.String())
		}
	}
	if m.saved.ID != 7 || !m.saved.Disabled || m.triggered != 1 || m.retryID != 3 || m.resumeID != 4 || m.schedule.IntervalSeconds != 3600 {
		t.Fatalf("wrong mutation: %+v", m)
	}
}
func TestRequestBoundaries(t *testing.T) {
	m := &mockService{}
	h := New(":0", m, "secret").Handler
	if w := req(h, "POST", "/api/accounts", "secret", `{"name":"`+strings.Repeat("x", 17000)+`"}`); w.Code != 400 {
		t.Fatal("unbounded JSON", w.Code)
	}
	r := httptest.NewRequest("POST", "/api/trigger", nil)
	r.Header.Set("Authorization", "Bearer secret")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 || m.triggered != 0 {
		t.Fatal("cross site mutation accepted")
	}
	if w := req(h, "POST", "/api/accounts", "secret", ""); w.Code != 415 {
		t.Fatal("content type not enforced", w.Code)
	}
	m.err = store.ErrConflict
	if w := req(h, "POST", "/api/jobs/1/retry", "secret", ""); w.Code != 409 {
		t.Fatal("conflict status", w.Code)
	}
	m.err = store.ErrNotFound
	if w := req(h, "PUT", "/api/accounts/999", "secret", `{"name":"x"}`); w.Code != 404 {
		t.Fatal("not found status", w.Code)
	}
}
