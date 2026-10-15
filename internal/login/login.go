// Package login runs the headless Epic login worker for one account at a time
// and relays verification codes typed into the web UI to the waiting worker.
package login

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/project-base-mirror/epic-claim/internal/epic"
)

// Credentials is the decrypted payload. It is only ever written to the worker's stdin.
type Credentials struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	TOTPSecret string `json:"totp_secret,omitempty"`
	// Interactive opens the live view immediately (manual login).
	Interactive bool `json:"interactive,omitempty"`
	// Games to claim in the same live session once logged in.
	Games []epic.Game `json:"games,omitempty"`
}

type Store interface {
	SetLoginStatus(ctx context.Context, id int64, status, message string) error
}

// ClaimRecorder persists claim results reported by a live session (optional).
type ClaimRecorder interface {
	RecordClaim(ctx context.Context, accountID int64, namespace, offerID, status, msg string) error
}

// Statuses the worker may report. Anything else is treated as "error".
var Messages = map[string]string{
	"logging_in":          "正在后台登录 Epic…",
	"interactive":         "实时窗口已打开，请在弹窗中完成登录。",
	"challenge":           "Epic 需要人机验证，请点击“去验证”在弹窗中完成。",
	"code_required":       "Epic 已发送验证码，请点击“输入验证码”。",
	"code_submitted":      "验证码已提交，正在等待 Epic 确认…",
	"checking":            "正在确认登录状态…",
	"claiming":            "已登录，正在弹窗中领取免费游戏…",
	"claim_challenge":     "领取时需要人机验证：请在弹窗中完成，完成后会自动继续。",
	"claim_login":         "领取时发现未登录：请在弹窗中登录。",
	"claim_terms":         "Epic 要求同意条款：请在弹窗中自行确认（点 Accept/同意）后会继续领取。",
	"not_logged_in":       "尚未检测到登录成功，请在弹窗中继续完成登录。",
	"ready":               "已登录，浏览器会话已保存。",
	"captcha_required":    "需要完成人机验证，请点击“去验证”。",
	"two_factor_required": "需要二次验证，请点击“去验证”在弹窗中完成。",
	"login_failed":        "Epic 拒绝了邮箱或密码，请点击“编辑”更新密码后重试。",
	"cancelled":           "已取消登录。",
	"profile_busy":        "该账户的浏览器正被另一个任务（登录或领取）使用，请稍后再点击“去验证”。",
	"error":               "登录未完成，请点击“去验证”在弹窗中手动完成。",
	"timeout":             "超过 10 分钟未完成，已关闭浏览器。请点击“去验证”重新开始。",
}

// Interactive reports whether a status means the live view is useful right now.
func Interactive(status string) bool {
	return status == "interactive" || status == "challenge" || status == "code_required" || status == "not_logged_in" || status == "claim_challenge" || status == "claim_login" || status == "claim_terms"
}

var codeRE = regexp.MustCompile(`^[A-Za-z0-9]{4,10}$`)

type run struct {
	stdin   io.WriteCloser
	waiting bool
	mu      sync.Mutex
	frame   []byte // latest JPEG
	subs    map[chan Event]struct{}
}

// Event is pushed to live-view subscribers: either a JPEG frame or a status.
type Event struct {
	Frame  []byte
	Status string
}

func (rn *run) publish(e Event) {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	if e.Frame != nil {
		rn.frame = e.Frame
	}
	for c := range rn.subs {
		select {
		case c <- e:
		default: // slow viewer: drop frames rather than block the worker
		}
	}
}

type Runner struct {
	// ClaimMessage validates a worker claim status and returns its message.
	ClaimMessage func(status string) (string, bool)
	Command      []string
	Timeout      time.Duration
	Store        Store
	mu           sync.Mutex
	runs         map[int64]*run
}

var ErrBusy = errors.New("a login is already running for this account")
var ErrNotWaiting = errors.New("the login is not waiting for a code")

func (r *Runner) Running(id int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.runs[id]
	return ok
}

// Start launches the worker asynchronously.
func (r *Runner) Start(id int64, c Credentials) error {
	if len(r.Command) == 0 {
		return errors.New("login worker is not configured")
	}
	r.mu.Lock()
	if r.runs == nil {
		r.runs = map[int64]*run{}
	}
	if _, ok := r.runs[id]; ok {
		r.mu.Unlock()
		return ErrBusy
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	cmd := exec.CommandContext(ctx, r.Command[0], r.Command[1:]...)
	cmd.WaitDelay = 3 * time.Second
	configureProcess(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		r.mu.Unlock()
		cancel()
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		r.mu.Unlock()
		cancel()
		return err
	}
	stderr := &tail{max: 2048}
	cmd.Stderr = stderr
	if err = cmd.Start(); err != nil {
		r.mu.Unlock()
		cancel()
		return errors.New("cannot start login worker")
	}
	rn := &run{stdin: stdin, subs: map[chan Event]struct{}{}}
	r.runs[id] = rn
	r.mu.Unlock()
	r.set(id, "logging_in")
	payload, _ := json.Marshal(struct {
		AccountID int64 `json:"account_id"`
		Credentials
	}{id, c})
	go func() {
		defer cancel()
		_, _ = stdin.Write(append(payload, '\n'))
		last := ""
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64<<10), 8<<20)
		for sc.Scan() {
			var ev struct {
				Status string `json:"status"`
				Frame  []byte `json:"frame"` // base64 in JSON
				Claim  *struct {
					OfferID   string `json:"offer_id"`
					Namespace string `json:"namespace"`
					Status    string `json:"status"`
				} `json:"claim"`
			}
			if json.Unmarshal(sc.Bytes(), &ev) != nil {
				continue
			}
			if ev.Frame != nil {
				rn.publish(Event{Frame: ev.Frame})
				continue
			}
			if ev.Claim != nil {
				if rec, ok := r.Store.(ClaimRecorder); ok && r.ClaimMessage != nil {
					if msg, ok := r.ClaimMessage(ev.Claim.Status); ok {
						ctx, c := context.WithTimeout(context.Background(), 5*time.Second)
						if err := rec.RecordClaim(ctx, id, ev.Claim.Namespace, ev.Claim.OfferID, ev.Claim.Status, msg); err != nil {
							log.Printf("login: cannot record claim result for account %d", id)
						}
						c()
					}
				}
				rn.publish(Event{Status: "claim_" + ev.Claim.Status})
				continue
			}
			if _, ok := Messages[ev.Status]; !ok {
				ev.Status = "error"
			}
			rn.mu.Lock()
			rn.waiting = ev.Status == "code_required"
			rn.mu.Unlock()
			last = ev.Status
			r.set(id, ev.Status)
			rn.publish(Event{Status: ev.Status})
		}
		werr := cmd.Wait()
		if werr != nil || stderr.Len() > 0 {
			// The worker writes only fixed, credential-free diagnostics to stderr.
			log.Printf("login worker account %d exited (%v): %s", id, werr, strings.TrimSpace(stderr.String()))
		}
		killProcessGroup(cmd)
		_ = stdin.Close()
		final := last
		if ctx.Err() == context.DeadlineExceeded {
			final = "timeout"
		} else if final == "" || final == "logging_in" || final == "code_submitted" || final == "checking" || Interactive(final) {
			final = "error"
		}
		if final != last {
			r.set(id, final)
		}
		rn.publish(Event{Status: final})
		rn.mu.Lock()
		for c := range rn.subs {
			close(c)
		}
		rn.subs = nil
		rn.mu.Unlock()
		r.mu.Lock()
		delete(r.runs, id)
		r.mu.Unlock()
	}()
	return nil
}

// SubmitCode forwards a verification code to a worker in state code_required.
func (r *Runner) SubmitCode(id int64, code string) error {
	if !codeRE.MatchString(code) {
		return errors.New("invalid code format")
	}
	r.mu.Lock()
	rn := r.runs[id]
	r.mu.Unlock()
	if rn == nil {
		return ErrNotWaiting
	}
	rn.mu.Lock()
	defer rn.mu.Unlock()
	if !rn.waiting {
		return ErrNotWaiting
	}
	b, _ := json.Marshal(map[string]string{"code": code})
	if _, err := rn.stdin.Write(append(b, '\n')); err != nil {
		return ErrNotWaiting
	}
	rn.waiting = false
	r.set(id, "code_submitted")
	return nil
}

// Subscribe returns live-view events for a running login (latest frame first).
func (r *Runner) Subscribe(id int64) (<-chan Event, func(), bool) {
	r.mu.Lock()
	rn := r.runs[id]
	r.mu.Unlock()
	if rn == nil {
		return nil, nil, false
	}
	c := make(chan Event, 8)
	rn.mu.Lock()
	defer rn.mu.Unlock()
	if rn.subs == nil {
		return nil, nil, false
	}
	if rn.frame != nil {
		c <- Event{Frame: rn.frame}
	}
	rn.subs[c] = struct{}{}
	return c, func() {
		rn.mu.Lock()
		defer rn.mu.Unlock()
		if _, ok := rn.subs[c]; ok {
			delete(rn.subs, c)
			close(c)
		}
	}, true
}

// Send forwards a validated control message (input/done/cancel) to the worker.
func (r *Runner) Send(id int64, msg map[string]any) error {
	r.mu.Lock()
	rn := r.runs[id]
	r.mu.Unlock()
	if rn == nil {
		return ErrNotWaiting
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	rn.mu.Lock()
	_, err = rn.stdin.Write(append(b, '\n'))
	rn.mu.Unlock()
	if err == nil && msg["cancel"] == true {
		// Reflect the cancel immediately; the worker confirms when it exits.
		r.set(id, "cancelled")
	}
	return err
}

func (r *Runner) set(id int64, status string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Store.SetLoginStatus(ctx, id, status, Messages[status]); err != nil {
		log.Print("login: cannot persist status for account " + strconv.FormatInt(id, 10))
	}
}

// tail keeps the last max bytes written (worker stderr).
type tail struct {
	mu  sync.Mutex
	b   []byte
	max int
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = t.b[len(t.b)-t.max:]
	}
	return len(p), nil
}
func (t *tail) Len() int       { t.mu.Lock(); defer t.mu.Unlock(); return len(t.b) }
func (t *tail) String() string { t.mu.Lock(); defer t.mu.Unlock(); return string(t.b) }
