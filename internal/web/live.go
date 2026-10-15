package web

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

// stream relays the login browser's live view over a WebSocket. It sits behind
// the /api session middleware; additionally the CSRF token must be passed as a
// query parameter and the Origin must match the Host (cross-site WebSocket
// hijacking protection; checked by websocket.Accept).
func (o *Options) stream(w http.ResponseWriter, r *http.Request, id int64) {
	p := current(r)
	if p == nil || (!p.token && subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("csrf")), []byte(p.csrf)) != 1) {
		fail(w, 403, "CSRF 校验失败，请刷新页面")
		return
	}
	events, cancel, ok := o.Login.Subscribe(id)
	if !ok {
		fail(w, 409, "该账户当前没有进行中的登录窗口")
		return
	}
	defer cancel()
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})
	c, err := websocket.Accept(w, r, nil) // nil options: same-origin only
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(4 << 10)
	ctx, stop := context.WithTimeout(r.Context(), 20*time.Minute)
	defer stop()
	go func() {
		defer stop()
		for {
			_, b, err := c.Read(ctx)
			if err != nil {
				return
			}
			var in struct {
				Type string  `json:"type"`
				X    float64 `json:"x"`
				Y    float64 `json:"y"`
				DY   float64 `json:"dy"`
				Key  string  `json:"key"`
				Text string  `json:"text"`
				Ctrl bool    `json:"ctrl"`
				Meta bool    `json:"meta"`
			}
			if json.Unmarshal(b, &in) != nil || len(in.Key) > 32 || len(in.Text) > 512 {
				continue
			}
			switch in.Type {
			case "move", "down", "up", "click", "wheel", "key", "text":
				_ = o.Login.Send(id, map[string]any{"input": in})
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			c.Close(websocket.StatusNormalClosure, "")
			return
		case e, ok := <-events:
			if !ok {
				c.Close(websocket.StatusNormalClosure, "login finished")
				return
			}
			wctx, wc := context.WithTimeout(ctx, 10*time.Second)
			if e.Frame != nil {
				err = c.Write(wctx, websocket.MessageBinary, e.Frame)
			} else {
				b, _ := json.Marshal(map[string]string{"status": e.Status})
				err = c.Write(wctx, websocket.MessageText, b)
			}
			wc()
			if err != nil {
				return
			}
		}
	}
}
