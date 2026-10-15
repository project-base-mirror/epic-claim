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
	"sync"
	"time"
)

// Credentials is the decrypted payload. It is only ever written to the worker's stdin.
type Credentials struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	TOTPSecret string `json:"totp_secret,omitempty"`
}

type Store interface {
	SetLoginStatus(ctx context.Context, id int64, status, message string) error
}

// Statuses the worker may report. Anything else is treated as "error".
var Messages = map[string]string{
	"logging_in":          "正在后台登录 Epic…",
	"code_required":       "Epic 要求输入邮箱/短信验证码，请在控制台中提交验证码。",
	"code_submitted":      "验证码已提交，正在等待 Epic 确认…",
	"ready":               "已登录，浏览器会话已保存。",
	"captcha_required":    "Epic/Cloudflare 弹出了人机验证，无头浏览器无法自动完成。请按 README 手动登录一次。",
	"two_factor_required": "Epic 要求二次验证但未提供可用的 TOTP 密钥或验证码。",
	"login_failed":        "Epic 拒绝了邮箱或密码，请检查后重试。",
	"error":               "登录过程出错或页面结构无法识别，请稍后重试或手动登录。",
	"timeout":             "登录超时，请重试。",
}

var codeRE = regexp.MustCompile(`^[A-Za-z0-9]{4,10}$`)

type run struct {
	stdin   io.WriteCloser
	waiting bool
	mu      sync.Mutex
}

type Runner struct {
	Command []string
	Timeout time.Duration
	Store   Store
	mu      sync.Mutex
	runs    map[int64]*run
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
	if err = cmd.Start(); err != nil {
		r.mu.Unlock()
		cancel()
		return errors.New("cannot start login worker")
	}
	rn := &run{stdin: stdin}
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
		sc := bufio.NewScanner(io.LimitReader(stdout, 1<<20))
		for sc.Scan() {
			var ev struct {
				Status string `json:"status"`
			}
			if json.Unmarshal(sc.Bytes(), &ev) != nil {
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
		}
		_ = cmd.Wait()
		_ = stdin.Close()
		final := last
		if ctx.Err() == context.DeadlineExceeded {
			final = "timeout"
		} else if final == "" || final == "logging_in" || final == "code_required" || final == "code_submitted" {
			final = "error"
		}
		if final != last {
			r.set(id, final)
		}
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

func (r *Runner) set(id int64, status string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Store.SetLoginStatus(ctx, id, status, Messages[status]); err != nil {
		log.Print("login: cannot persist status for account " + strconv.FormatInt(id, 10))
	}
}
