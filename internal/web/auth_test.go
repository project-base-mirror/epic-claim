package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/project-base-mirror/epic-claim/internal/login"
	"github.com/project-base-mirror/epic-claim/internal/scheduler"
	"github.com/project-base-mirror/epic-claim/internal/secret"
	"github.com/project-base-mirror/epic-claim/internal/store"
)

type fakeLogin struct{ started login.Credentials }

func (f *fakeLogin) Start(_ int64, c login.Credentials) error { f.started = c; return nil }
func (f *fakeLogin) SubmitCode(int64, string) error           { return nil }
func (f *fakeLogin) Running(int64) bool                       { return false }

type client struct {
	h      http.Handler
	cookie *http.Cookie
	csrf   string
}

func (c *client) do(method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if c.cookie != nil {
		r.AddCookie(c.cookie)
	}
	if c.csrf != "" {
		r.Header.Set("X-CSRF-Token", c.csrf)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	c.h.ServeHTTP(w, r)
	for _, ck := range w.Result().Cookies() {
		if ck.Name == sessionCookie {
			c.cookie = ck
		}
	}
	return w
}

func TestWebUserFlow(t *testing.T) {
	st, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	key, _ := secret.ParseKey("MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=")
	box, _ := secret.New(key)
	fl := &fakeLogin{}
	svc := &scheduler.Scheduler{Store: st}
	h := New(":0", svc, "", Options{Auth: st, Secrets: box, Login: fl}).Handler
	a := &client{h: h}
	if w := a.do("GET", "/api/accounts", ""); w.Code != 401 {
		t.Fatal("anonymous api", w.Code)
	}
	if w := a.do("GET", "/auth/state", ""); !strings.Contains(w.Body.String(), `"registration_open":true`) {
		t.Fatal(w.Body.String())
	}
	if w := a.do("POST", "/auth/register", `{"username":"admin","password":"short"}`); w.Code != 400 {
		t.Fatal("weak password", w.Code)
	}
	if w := a.do("POST", "/auth/register", `{"username":"admin","password":"correct horse battery"}`, "Origin", "https://evil.example"); w.Code != 403 {
		t.Fatal("cross-origin register", w.Code)
	}
	w := a.do("POST", "/auth/register", `{"username":"admin","password":"correct horse battery"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"is_admin":true`) || a.cookie == nil || !a.cookie.HttpOnly || a.cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("first admin", w.Code, w.Body.String())
	}
	// Second user: registration is closed by default.
	b := &client{h: h}
	if w := b.do("POST", "/auth/register", `{"username":"bob","password":"correct horse battery"}`); w.Code != 403 {
		t.Fatal("registration should be closed", w.Code)
	}
	if w := b.do("POST", "/auth/login", `{"username":"admin","password":"wrong password!"}`); w.Code != 401 {
		t.Fatal("bad login", w.Code)
	}
	// Session: state exposes csrf, writes need it.
	st2 := a.do("GET", "/auth/state", "")
	i := strings.Index(st2.Body.String(), `"csrf":"`)
	a.csrf = st2.Body.String()[i+8 : i+8+43]
	csrf := a.csrf
	a.csrf = ""
	if w := a.do("POST", "/api/accounts", `{"name":"main"}`); w.Code != 403 {
		t.Fatal("missing csrf accepted", w.Code)
	}
	a.csrf = csrf
	w = a.do("POST", "/api/accounts", `{"name":"main","email":"someone@example.com","password":"p@ss","totp_secret":"jbsw y3dp ehpk 3pxp"}`)
	if w.Code != 200 || strings.Contains(w.Body.String(), "p@ss") || !strings.Contains(w.Body.String(), `"email_hint":"s***@example.com"`) {
		t.Fatal("save creds", w.Code, w.Body.String())
	}
	blob, _ := st.Credentials(t.Context(), 1)
	if strings.Contains(string(blob), "p@ss") || strings.Contains(string(blob), "someone") {
		t.Fatal("credentials stored in plaintext")
	}
	if w := a.do("POST", "/api/accounts/1/login", ""); w.Code != 202 || fl.started.Password != "p@ss" || fl.started.TOTPSecret != "JBSWY3DPEHPK3PXP" {
		t.Fatal("login start", w.Code, fl.started.Email)
	}
	// Admin opens registration.
	if w := a.do("PUT", "/api/settings", `{"allow_registration":true}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := b.do("POST", "/auth/register", `{"username":"bob","password":"correct horse battery"}`); w.Code != 200 || strings.Contains(w.Body.String(), `"is_admin":true`) {
		t.Fatal("second user", w.Code, w.Body.String())
	}
	if w := a.do("POST", "/auth/logout", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := a.do("GET", "/api/accounts", ""); w.Code != 401 {
		t.Fatal("session survived logout", w.Code)
	}
}
