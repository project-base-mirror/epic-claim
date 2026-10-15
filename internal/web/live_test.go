package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/project-base-mirror/epic-claim/internal/scheduler"
	"github.com/project-base-mirror/epic-claim/internal/store"
)

func TestLiveStreamAuthAndRelay(t *testing.T) {
	st, _ := store.OpenSQLite(":memory:")
	defer st.Close()
	fl := &fakeLogin{}
	h := New(":0", &scheduler.Scheduler{Store: st}, "", Options{Auth: st, Login: fl}).Handler
	srv := httptest.NewServer(h)
	defer srv.Close()
	a := &client{h: h}
	a.do("POST", "/auth/register", `{"username":"admin","password":"correct horse battery"}`)
	body := a.do("GET", "/auth/state", "").Body.String()
	i := strings.Index(body, `"csrf":"`)
	csrf := body[i+8 : i+8+43]
	ws := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/accounts/1/stream?csrf="
	hdr := func(origin string) *websocket.DialOptions {
		h := http.Header{"Cookie": {a.cookie.Name + "=" + a.cookie.Value}}
		if origin != "" {
			h.Set("Origin", origin)
		}
		return &websocket.DialOptions{HTTPHeader: h}
	}
	ctx := context.Background()
	if _, _, err := websocket.Dial(ctx, ws, nil); err == nil {
		t.Fatal("anonymous stream accepted")
	}
	if _, _, err := websocket.Dial(ctx, ws+"wrong", hdr("")); err == nil {
		t.Fatal("stream without csrf accepted")
	}
	if _, _, err := websocket.Dial(ctx, ws+csrf, hdr("https://evil.example")); err == nil {
		t.Fatal("cross-origin stream accepted")
	}
	c, _, err := websocket.Dial(ctx, ws+csrf, hdr(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	typ, b, err := c.Read(ctx)
	if err != nil || typ != websocket.MessageBinary || !strings.HasSuffix(string(b), "jpeg") {
		t.Fatal("frame", typ, err)
	}
	if _, b, err = c.Read(ctx); err != nil || !strings.Contains(string(b), "ready") {
		t.Fatal("status", string(b), err)
	}
	a.csrf = csrf
	if w := a.do("POST", "/api/accounts/1/live", `{"action":"done"}`); w.Code != 200 || len(fl.sent) != 1 || fl.sent[0]["done"] != true {
		t.Fatal("done relay", w.Code)
	}
	if w := a.do("POST", "/api/accounts/1/live", `{"action":"rm -rf"}`); w.Code != 400 {
		t.Fatal("bad action", w.Code)
	}
}
