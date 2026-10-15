package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/project-base-mirror/epic-claim/internal/netcfg"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/project-base-mirror/epic-claim/internal/login"
	"github.com/project-base-mirror/epic-claim/internal/secret"
	"github.com/project-base-mirror/epic-claim/internal/store"
	"golang.org/x/crypto/bcrypt"
)

const sessionCookie = "epic_session"
const sessionTTL = 14 * 24 * time.Hour

// AuthStore is implemented by *store.SQLite.
type AuthStore interface {
	UserCount(context.Context) (int, error)
	CreateUser(ctx context.Context, username, hash string, allowOpen bool) (store.User, error)
	UserByName(context.Context, string) (store.User, error)
	CreateSession(ctx context.Context, tokenHash string, userID int64, csrf string, expires time.Time) error
	SessionByHash(context.Context, string) (store.Session, error)
	DeleteSession(context.Context, string) error
	Setting(context.Context, string) (string, bool, error)
	SetSetting(context.Context, string, string) error
	SetCredentials(ctx context.Context, id int64, blob []byte, emailHint string) error
	Credentials(context.Context, int64) ([]byte, error)
	ClaimableJobs(context.Context, int64) ([]store.Job, error)
	AccountRoute(context.Context, int64) (string, error)
	SetAccountRoute(context.Context, int64, string) error
}

type LoginRunner interface {
	Start(int64, login.Credentials) error
	SubmitCode(int64, string) error
	Running(int64) bool
	Subscribe(int64) (<-chan login.Event, func(), bool)
	Send(int64, map[string]any) error
}

// Options enables web-user authentication and Epic credential management.
type Options struct {
	Auth                     AuthStore
	Secrets                  *secret.Box // nil: credentials cannot be stored
	Login                    LoginRunner
	AllowRegistrationDefault bool
	NetTestCommand           []string // 测试连接 worker; nil disables the button
}

type principal struct {
	user  store.User
	csrf  string
	token bool
}

type ctxKey struct{}

func current(r *http.Request) *principal { p, _ := r.Context().Value(ctxKey{}).(*principal); return p }

var usernameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,32}$`)

// dummyHash equalizes timing for unknown usernames.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("timing-equalizer"), bcrypt.DefaultCost)

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
func hashToken(t string) string { h := sha256.Sum256([]byte(t)); return hex.EncodeToString(h[:]) }

func secureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func clientIP(r *http.Request) string {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() || strings.HasPrefix(host, "172.") {
		if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
			return v
		}
	}
	return host
}

type limiter struct {
	mu sync.Mutex
	m  map[string][]time.Time
}

// allow permits 10 failed attempts per key per 15 minutes.
func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := time.Now().Add(-15 * time.Minute)
	v := l.m[key][:0]
	for _, t := range l.m[key] {
		if t.After(cut) {
			v = append(v, t)
		}
	}
	l.m[key] = v
	return len(v) < 10
}
func (l *limiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.m[key] = append(l.m[key], time.Now())
}

func (o *Options) registrationOpen(ctx context.Context) (open, hasUsers bool, err error) {
	n, err := o.Auth.UserCount(ctx)
	if err != nil {
		return false, false, err
	}
	if n == 0 {
		return true, false, nil
	}
	v, ok, err := o.Auth.Setting(ctx, "allow_registration")
	if err != nil {
		return false, true, err
	}
	if !ok {
		return o.AllowRegistrationDefault, true, nil
	}
	return v == "true", true, nil
}

func (o *Options) session(r *http.Request) *principal {
	c, err := r.Cookie(sessionCookie)
	if err != nil || len(c.Value) < 20 || len(c.Value) > 100 {
		return nil
	}
	s, err := o.Auth.SessionByHash(r.Context(), hashToken(c.Value))
	if err != nil {
		return nil
	}
	return &principal{user: s.User, csrf: s.CSRF}
}

func (o *Options) startSession(w http.ResponseWriter, r *http.Request, u store.User) error {
	tok, csrf := randomToken(), randomToken()
	exp := time.Now().Add(sessionTTL)
	if err := o.Auth.CreateSession(r.Context(), hashToken(tok), u.ID, csrf, exp); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: tok, Path: "/", Expires: exp, MaxAge: int(sessionTTL / time.Second), HttpOnly: true, Secure: secureRequest(r), SameSite: http.SameSiteStrictMode})
	return nil
}

func sameOriginOnly(w http.ResponseWriter, r *http.Request) bool {
	if s := r.Header.Get("Sec-Fetch-Site"); s == "cross-site" || s == "same-site" {
		fail(w, 403, "不允许跨站请求")
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		if i := strings.Index(origin, "://"); i < 0 || origin[i+3:] != r.Host {
			fail(w, 403, "不允许跨站请求")
			return false
		}
	}
	return true
}

func (o *Options) routes(mux *http.ServeMux) {
	lim := &limiter{m: map[string][]time.Time{}}
	mux.HandleFunc("GET /auth/state", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		open, has, err := o.registrationOpen(r.Context())
		if err != nil {
			fail(w, 500, "无法读取账户状态")
			return
		}
		out := map[string]any{"registration_open": open, "has_users": has, "user": nil}
		if p := o.session(r); p != nil {
			out["user"] = p.user
			out["csrf"] = p.csrf
		}
		respond(w, out, nil)
	})
	type creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	mux.HandleFunc("POST /auth/register", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var in creds
		if !sameOriginOnly(w, r) || !decode(w, r, &in) {
			return
		}
		if !usernameRE.MatchString(in.Username) {
			fail(w, 400, "用户名需为 3–32 位字母、数字、点、下划线或连字符")
			return
		}
		if len(in.Password) < 10 || len(in.Password) > 72 {
			fail(w, 400, "密码长度需为 10–72 个字节")
			return
		}
		open, _, err := o.registrationOpen(r.Context())
		if err != nil {
			fail(w, 500, "注册失败")
			return
		}
		if !open {
			fail(w, 403, "注册已关闭，请联系管理员开启")
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), 12)
		if err != nil {
			fail(w, 500, "注册失败")
			return
		}
		// CreateUser re-checks "first user" atomically; later users need the switch.
		u, err := o.Auth.CreateUser(r.Context(), in.Username, string(hash), open)
		switch {
		case errors.Is(err, store.ErrExists):
			fail(w, 409, "用户名已存在")
			return
		case errors.Is(err, store.ErrRegistrationClosed):
			fail(w, 403, "注册已关闭")
			return
		case err != nil:
			fail(w, 500, "注册失败")
			return
		}
		if err = o.startSession(w, r, u); err != nil {
			fail(w, 500, "注册成功但无法创建会话，请登录")
			return
		}
		respond(w, map[string]any{"user": u}, nil)
	})
	mux.HandleFunc("POST /auth/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var in creds
		if !sameOriginOnly(w, r) || !decode(w, r, &in) {
			return
		}
		key := clientIP(r)
		if !lim.allow(key) {
			fail(w, 429, "失败次数过多，请 15 分钟后再试")
			return
		}
		u, err := o.Auth.UserByName(r.Context(), in.Username)
		hash := []byte(u.PasswordHash)
		if err != nil {
			hash = dummyHash
		}
		if bcrypt.CompareHashAndPassword(hash, []byte(in.Password)) != nil || err != nil {
			lim.fail(key)
			fail(w, 401, "用户名或密码错误")
			return
		}
		if err = o.startSession(w, r, u); err != nil {
			fail(w, 500, "无法创建会话")
			return
		}
		respond(w, map[string]any{"user": u}, nil)
	})
	mux.HandleFunc("POST /auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if !sameOriginOnly(w, r) {
			return
		}
		if p := o.session(r); p != nil {
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(p.csrf)) != 1 {
				fail(w, 403, "CSRF 校验失败，请刷新页面")
				return
			}
			c, _ := r.Cookie(sessionCookie)
			_ = o.Auth.DeleteSession(r.Context(), hashToken(c.Value))
		}
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: secureRequest(r), SameSite: http.SameSiteStrictMode})
		respond(w, map[string]bool{"ok": true}, nil)
	})
}

// apiRoutes registers session-only extensions on the authenticated API mux.
func (o *Options) apiRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/settings", func(w http.ResponseWriter, r *http.Request) {
		open, _, err := o.registrationOpen(r.Context())
		respond(w, map[string]any{"allow_registration": open, "credentials_enabled": o.Secrets != nil, "is_admin": current(r).user.IsAdmin}, err)
	})
	api.HandleFunc("PUT /api/settings", func(w http.ResponseWriter, r *http.Request) {
		if !current(r).user.IsAdmin {
			fail(w, 403, "仅管理员可以修改设置")
			return
		}
		var in struct {
			AllowRegistration bool `json:"allow_registration"`
		}
		if !decode(w, r, &in) {
			return
		}
		respond(w, map[string]bool{"ok": true}, o.Auth.SetSetting(r.Context(), "allow_registration", strconv.FormatBool(in.AllowRegistration)))
	})
	api.HandleFunc("GET /api/network", func(w http.ResponseWriter, r *http.Request) {
		g, err := netcfg.Load(r.Context(), o.Auth)
		respond(w, map[string]any{"global": g, "effective": netcfg.Resolve(g, ""), "default_proxy": netcfg.DefaultProxy, "can_test": o.NetTestCommand != nil}, err)
	})
	api.HandleFunc("PUT /api/network", func(w http.ResponseWriter, r *http.Request) {
		if !current(r).user.IsAdmin {
			fail(w, 403, "仅管理员可以修改设置")
			return
		}
		var g netcfg.Global
		if !decode(w, r, &g) {
			return
		}
		if err := netcfg.Save(r.Context(), o.Auth, g); err != nil {
			fail(w, 400, err.Error())
			return
		}
		respond(w, map[string]any{"effective": netcfg.Resolve(g, "")}, nil)
	})
	api.HandleFunc("POST /api/network/test", func(w http.ResponseWriter, r *http.Request) {
		if !current(r).user.IsAdmin || o.NetTestCommand == nil {
			fail(w, 403, "不可用")
			return
		}
		var in struct {
			Global  *netcfg.Global `json:"global"`
			Account string         `json:"account_route"`
		}
		if r.ContentLength > 0 && !decode(w, r, &in) {
			return
		}
		g, err := netcfg.Load(r.Context(), o.Auth)
		if err != nil {
			respond(w, nil, err)
			return
		}
		if in.Global != nil {
			g = *in.Global
		}
		if netcfg.ValidAccountRoute(in.Account) != nil || (g.Route == "proxy" && netcfg.ValidProxy(g.ProxyURL) != nil) {
			fail(w, 400, "代理地址格式不正确")
			return
		}
		eff := netcfg.Resolve(g, in.Account)
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, o.NetTestCommand[0], o.NetTestCommand[1:]...)
		cmd.Env = append(os.Environ(), eff.Env()...)
		out, err := cmd.Output()
		var res map[string]any
		if err != nil || json.Unmarshal(out, &res) != nil {
			fail(w, 502, "测试未能完成（浏览器启动失败或超时）")
			return
		}
		res["route"] = eff
		respond(w, res, nil)
	})
	api.HandleFunc("PUT /api/accounts/{id}/route", func(w http.ResponseWriter, r *http.Request) {
		id, ok := positiveID(w, r)
		if !ok {
			return
		}
		var in struct {
			Route string `json:"route"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Route = strings.TrimSpace(in.Route)
		if err := netcfg.ValidAccountRoute(in.Route); err != nil {
			fail(w, 400, err.Error())
			return
		}
		respond(w, map[string]bool{"ok": true}, o.Auth.SetAccountRoute(r.Context(), id, in.Route))
	})
	api.HandleFunc("POST /api/accounts/{id}/login", func(w http.ResponseWriter, r *http.Request) {
		id, ok := positiveID(w, r)
		if !ok {
			return
		}
		if o.Secrets == nil || o.Login == nil {
			fail(w, 503, "服务器未配置 EPIC_SECRET_KEY，无法使用网页登录")
			return
		}
		var opts struct {
			Interactive bool `json:"interactive"`
		}
		if r.ContentLength > 0 && !decode(w, r, &opts) {
			return
		}
		blob, err := o.Auth.Credentials(r.Context(), id)
		if err != nil {
			respond(w, nil, err)
			return
		}
		var c login.Credentials
		if blob == nil && !opts.Interactive {
			fail(w, 400, "该账户尚未保存 Epic 邮箱和密码，请点击“编辑”填写，或使用“手动登录”")
			return
		}
		if blob != nil {
			plain, err := o.Secrets.Open(blob, aad(id))
			if err != nil {
				fail(w, 500, "无法解密已保存的凭据，请点击“编辑”重新填写密码")
				return
			}
			_ = json.Unmarshal(plain, &c)
		}
		c.Interactive = opts.Interactive
		// Pending free games are claimed in the same live session after login.
		if jobs, err := o.Auth.ClaimableJobs(r.Context(), id); err == nil {
			for _, j := range jobs {
				c.Games = append(c.Games, j.Game)
			}
		}
		if err = o.Login.Start(id, c); errors.Is(err, login.ErrBusy) {
			fail(w, 409, "该账户正在登录中")
			return
		} else if err != nil {
			fail(w, 500, "无法启动登录 Worker")
			return
		}
		w.WriteHeader(http.StatusAccepted)
		respond(w, map[string]bool{"ok": true}, nil)
	})
	// Fallback: import a logged-in session exported from the user's own browser.
	// The cookies go straight to a live session (which verifies the login and claims);
	// they are not stored in the database.
	api.HandleFunc("POST /api/accounts/{id}/cookies", func(w http.ResponseWriter, r *http.Request) {
		id, ok := positiveID(w, r)
		if !ok || o.Login == nil {
			return
		}
		var in struct {
			Text string `json:"text"`
		}
		if !decode(w, r, &in) {
			return
		}
		cookies, err := login.ParseCookies(in.Text)
		if err != nil {
			fail(w, 400, "没有识别到 epicgames.com 的 Cookie。请粘贴 Cookie-Editor 导出的 JSON 或 cookies.txt（Netscape）内容")
			return
		}
		c := login.Credentials{Interactive: true, Cookies: cookies}
		if jobs, err := o.Auth.ClaimableJobs(r.Context(), id); err == nil {
			for _, j := range jobs {
				c.Games = append(c.Games, j.Game)
			}
		}
		if err = o.Login.Start(id, c); errors.Is(err, login.ErrBusy) {
			fail(w, 409, "该账户已有进行中的登录窗口，请先取消")
			return
		} else if err != nil {
			fail(w, 500, "无法启动登录 Worker")
			return
		}
		w.WriteHeader(http.StatusAccepted)
		respond(w, map[string]int{"cookies": len(cookies)}, nil)
	})
	api.HandleFunc("POST /api/accounts/{id}/live", func(w http.ResponseWriter, r *http.Request) {
		id, ok := positiveID(w, r)
		if !ok || o.Login == nil {
			return
		}
		var in struct {
			Action string `json:"action"`
		}
		if !decode(w, r, &in) {
			return
		}
		msg := map[string]any{}
		switch in.Action {
		case "done", "cancel", "reload", "reset":
			msg[in.Action] = true
		case "fill_email", "fill_password":
			// The worker types the stored value itself; it never reaches the browser client.
			msg["fill"] = strings.TrimPrefix(in.Action, "fill_")
		default:
			fail(w, 400, "未知操作")
			return
		}
		if err := o.Login.Send(id, msg); err != nil {
			fail(w, 409, "该账户当前没有进行中的登录窗口")
			return
		}
		respond(w, map[string]bool{"ok": true}, nil)
	})
	api.HandleFunc("GET /api/accounts/{id}/stream", func(w http.ResponseWriter, r *http.Request) {
		id, ok := positiveID(w, r)
		if !ok || o.Login == nil {
			return
		}
		o.stream(w, r, id)
	})
	api.HandleFunc("POST /api/accounts/{id}/code", func(w http.ResponseWriter, r *http.Request) {
		id, ok := positiveID(w, r)
		if !ok || o.Login == nil {
			return
		}
		var in struct {
			Code string `json:"code"`
		}
		if !decode(w, r, &in) {
			return
		}
		if err := o.Login.SubmitCode(id, strings.TrimSpace(in.Code)); errors.Is(err, login.ErrNotWaiting) && o.Login.Running(id) {
			// Live-view logins also accept codes typed into the form.
			if e := o.Login.Send(id, map[string]any{"code": strings.TrimSpace(in.Code)}); e == nil {
				respond(w, map[string]bool{"ok": true}, nil)
				return
			}
			fail(w, 409, "当前没有等待验证码的登录")
			return
		} else if err != nil {
			fail(w, 409, "当前没有等待验证码的登录，或验证码格式不正确")
			return
		}
		respond(w, map[string]bool{"ok": true}, nil)
	})
}

func aad(id int64) []byte { return []byte("epic-account:" + strconv.FormatInt(id, 10)) }

var totpRE = regexp.MustCompile(`^[A-Z2-7]{16,128}=*$`)

// saveCredentials validates and encrypts optional Epic credentials for an account.
func (o *Options) saveCredentials(ctx context.Context, id int64, email, password, totp string, clear bool) (int, string) {
	if clear {
		if err := o.Auth.SetCredentials(ctx, id, nil, ""); err != nil {
			return 500, "无法清除凭据"
		}
		return 0, ""
	}
	if email == "" && password == "" && totp == "" {
		return 0, ""
	}
	if o.Secrets == nil {
		return 503, "服务器未配置 EPIC_SECRET_KEY，无法保存 Epic 凭据"
	}
	email = strings.TrimSpace(email)
	at := strings.LastIndex(email, "@")
	if at < 1 || at == len(email)-1 || len(email) > 254 || strings.ContainsAny(email, " \r\n\t") {
		return 400, "请输入有效的 Epic 邮箱"
	}
	if password == "" || len(password) > 256 {
		return 400, "请输入 Epic 密码（修改邮箱或 TOTP 时也需重新输入密码）"
	}
	totp = strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(totp, " ", ""), "-", ""))
	if totp != "" && !totpRE.MatchString(totp) {
		return 400, "TOTP 密钥应为 Base32（A–Z、2–7）"
	}
	plain, _ := json.Marshal(login.Credentials{Email: email, Password: password, TOTPSecret: totp})
	blob, err := o.Secrets.Seal(plain, aad(id))
	if err != nil {
		return 500, "加密失败"
	}
	hint := email[:1] + "***" + email[at:]
	if err = o.Auth.SetCredentials(ctx, id, blob, hint); err != nil {
		return 500, "无法保存凭据"
	}
	return 0, ""
}
