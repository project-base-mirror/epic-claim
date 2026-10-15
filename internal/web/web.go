// Package web serves the authenticated administration API and embedded dashboard.
package web

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/project-base-mirror/epic-claim/internal/epic"
	"github.com/project-base-mirror/epic-claim/internal/login"
	"github.com/project-base-mirror/epic-claim/internal/store"
)

//go:embed static/*
var assets embed.FS

type Service interface {
	Accounts(context.Context) ([]store.Account, error)
	SaveAccount(context.Context, store.Account) (store.Account, error)
	Jobs(context.Context) ([]store.Job, error)
	GetSchedule(context.Context) (store.Schedule, error)
	SetSchedule(context.Context, store.Schedule) error
	Trigger(context.Context) (int, error)
	Retry(context.Context, int64) error
	Resume(context.Context, int64) error
	FreeGames(context.Context) ([]epic.Game, error)
}

func New(addr string, service Service, adminToken string, options ...Options) *http.Server {
	var opt *Options
	if len(options) > 0 && options[0].Auth != nil {
		opt = &options[0]
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	api := http.NewServeMux()
	api.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		v, e := service.GetSchedule(r.Context())
		respond(w, map[string]any{"schedule": v}, e)
	})
	api.HandleFunc("GET /api/accounts", func(w http.ResponseWriter, r *http.Request) {
		v, e := service.Accounts(r.Context())
		if opt != nil && opt.Login != nil {
			for i := range v {
				v[i].LoginRunning = opt.Login.Running(v[i].ID)
				// Always show the current wording (older rows may hold outdated text).
				if msg, ok := login.Messages[v[i].SessionStatus]; ok {
					v[i].LoginMessage = msg
				}
			}
		}
		respond(w, v, e)
	})
	save := func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name     string `json:"name"`
			Disabled bool   `json:"disabled"`
			Email    string `json:"email"`
			Password string `json:"password"`
			TOTP     string `json:"totp_secret"`
			Clear    bool   `json:"clear_credentials"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Name = strings.TrimSpace(in.Name)
		if len(in.Name) == 0 || len(in.Name) > 100 {
			fail(w, 400, "账户名称需要 1–100 个字节")
			return
		}
		var id int64
		if r.PathValue("id") != "" {
			var ok bool
			id, ok = positiveID(w, r)
			if !ok {
				return
			}
		}
		hasCreds := in.Email != "" || in.Password != "" || in.TOTP != "" || in.Clear
		if hasCreds && opt == nil {
			fail(w, 400, "此服务未启用网页凭据管理")
			return
		}
		if hasCreds && !in.Clear && opt.Secrets == nil {
			fail(w, 503, "服务器未配置 EPIC_SECRET_KEY，无法保存 Epic 凭据")
			return
		}
		v, e := service.SaveAccount(r.Context(), store.Account{ID: id, Name: in.Name, Disabled: in.Disabled})
		if e == nil && hasCreds {
			if code, msg := opt.saveCredentials(r.Context(), v.ID, in.Email, in.Password, in.TOTP, in.Clear); code != 0 {
				fail(w, code, msg+(map[bool]string{true: "（账户名称已保存）", false: ""}[id == 0]))
				return
			}
			if list, err := service.Accounts(r.Context()); err == nil {
				for _, a := range list {
					if a.ID == v.ID {
						v = a
					}
				}
			}
		}
		respond(w, v, e)
	}
	api.HandleFunc("POST /api/accounts", save)
	api.HandleFunc("PUT /api/accounts/{id}", save)
	api.HandleFunc("GET /api/history", func(w http.ResponseWriter, r *http.Request) { v, e := service.Jobs(r.Context()); respond(w, v, e) })
	api.HandleFunc("GET /api/freegames", func(w http.ResponseWriter, r *http.Request) { v, e := service.FreeGames(r.Context()); respond(w, v, e) })
	api.HandleFunc("POST /api/trigger", func(w http.ResponseWriter, r *http.Request) {
		n, e := service.Trigger(r.Context())
		respond(w, map[string]any{"queued": n}, e)
	})
	api.HandleFunc("POST /api/jobs/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		id, ok := positiveID(w, r)
		if ok {
			respond(w, map[string]bool{"ok": true}, service.Retry(r.Context(), id))
		}
	})
	api.HandleFunc("POST /api/jobs/{id}/resume", func(w http.ResponseWriter, r *http.Request) {
		id, ok := positiveID(w, r)
		if ok {
			respond(w, map[string]bool{"ok": true}, service.Resume(r.Context(), id))
		}
	})
	api.HandleFunc("PUT /api/schedule", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Enabled         bool  `json:"enabled"`
			IntervalSeconds int64 `json:"interval_seconds"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.IntervalSeconds < 60 || in.IntervalSeconds > 604800 {
			fail(w, 400, "检查间隔必须介于 1 分钟和 7 天之间")
			return
		}
		respond(w, map[string]bool{"ok": true}, service.SetSchedule(r.Context(), store.Schedule{Enabled: in.Enabled, IntervalSeconds: in.IntervalSeconds}))
	})
	if opt != nil {
		opt.apiRoutes(api)
		opt.routes(mux)
	}
	expected := sha256.Sum256([]byte(adminToken))
	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			fail(w, 403, "不允许跨站请求")
			return
		}
		var p *principal
		if auth := r.Header.Get("Authorization"); auth != "" {
			// Optional API token for scripts. Not cookie-based, so no CSRF token needed.
			got := sha256.Sum256([]byte(strings.TrimPrefix(auth, "Bearer ")))
			if adminToken == "" || !strings.HasPrefix(auth, "Bearer ") || subtle.ConstantTimeCompare(got[:], expected[:]) != 1 {
				w.Header().Set("WWW-Authenticate", "Bearer")
				fail(w, 401, "API 令牌无效或未配置")
				return
			}
			p = &principal{user: store.User{Username: "api-token", IsAdmin: true}, token: true}
		} else if opt != nil {
			p = opt.session(r)
			if p == nil {
				fail(w, 401, "请先登录")
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(p.csrf)) != 1 {
				fail(w, 403, "CSRF 校验失败，请刷新页面")
				return
			}
		} else {
			w.Header().Set("WWW-Authenticate", "Bearer")
			fail(w, 401, "管理员令牌无效或未配置")
			return
		}
		api.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, p)))
	}))
	sub, _ := fs.Sub(assets, "static")
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		http.FileServer(http.FS(sub)).ServeHTTP(w, r)
	}))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; connect-src 'self' wss://"+r.Host+" ws://"+r.Host+"; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		mux.ServeHTTP(w, r)
	})
	return &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 45 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
}
func positiveID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if e != nil || id < 1 {
		fail(w, 400, "无效的编号")
		return 0, false
	}
	return id, true
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		fail(w, 415, "请使用 application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		fail(w, 400, "请求 JSON 无效或过大")
		return false
	}
	if e := d.Decode(&struct{}{}); !errors.Is(e, io.EOF) {
		fail(w, 400, "仅允许一个 JSON 对象")
		return false
	}
	return true
}
func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
func respond(w http.ResponseWriter, v any, e error) {
	if e != nil {
		if errors.Is(e, store.ErrNotFound) {
			fail(w, 404, "记录不存在")
		} else if errors.Is(e, store.ErrConflict) {
			fail(w, 409, "当前任务状态不支持此操作，请刷新后重试")
		} else {
			fail(w, 500, "操作未完成，请检查任务状态后重试")
		}
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}
