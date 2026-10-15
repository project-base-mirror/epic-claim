package web

import (
	"strings"
	"testing"

	"github.com/project-base-mirror/epic-claim/internal/scheduler"
	"github.com/project-base-mirror/epic-claim/internal/store"
)

func TestPageRoutes(t *testing.T) {
	st, _ := store.OpenSQLite(":memory:")
	defer st.Close()
	h := New(":0", &scheduler.Scheduler{Store: st}, "", Options{Auth: st}).Handler
	anon := &client{h: h}
	paths := []string{"/", "/games", "/accounts", "/jobs", "/schedule", "/settings", "/accounts/"}
	for _, p := range paths {
		w := anon.do("GET", p, "")
		if w.Code != 302 || !strings.HasPrefix(w.Header().Get("Location"), "/login") {
			t.Errorf("anon %s = %d %s", p, w.Code, w.Header().Get("Location"))
		}
	}
	if w := anon.do("GET", "/accounts", ""); w.Header().Get("Location") != "/login?next=%2Faccounts" {
		t.Error("next param", w.Header().Get("Location"))
	}
	if w := anon.do("GET", "/login", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `id="login-form"`) {
		t.Error("login page", w.Code)
	}
	u := &client{h: h}
	u.do("POST", "/auth/register", `{"username":"tester","password":"throwaway pass 123"}`)
	for _, p := range paths {
		w := u.do("GET", p, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `data-page="accounts"`) || w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("auth %s = %d", p, w.Code)
		}
	}
	for _, p := range []string{"/app.js", "/app.css"} {
		if w := anon.do("GET", p, ""); w.Code != 200 {
			t.Errorf("asset %s = %d", p, w.Code)
		}
	}
	if w := u.do("GET", "/nope", ""); w.Code != 404 {
		t.Error("unknown route", w.Code)
	}
}
