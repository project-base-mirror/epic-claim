// Package claimer drives a bounded, fail-closed external browser worker.
package claimer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/project-base-mirror/epic-claim/internal/epic"
	"github.com/project-base-mirror/epic-claim/internal/store"
)

type Request struct {
	AccountID int64       `json:"account_id"`
	Games     []epic.Game `json:"games"`
}
type Result struct {
	OfferID   string            `json:"offer_id"`
	Namespace string            `json:"namespace"`
	Status    store.ClaimStatus `json:"status"`
	Message   string            `json:"message"`
}
type Claimer interface {
	Claim(context.Context, Request) ([]Result, error)
}
type Exec struct {
	// Env returns extra environment (browser route) for an account's worker.
	Env            func(ctx context.Context, accountID int64) []string
	Command        []string
	Timeout        time.Duration
	MaxOutputBytes int64
}

// Statuses is the Chinese, actionable message for each worker outcome.
var Statuses = map[store.ClaimStatus]string{
	"unsupported":         "商店页面结构无法识别，未提交任何订单。请在账户管理点击“去验证”，在弹窗中查看并手动领取。",
	"claimed":             "领取成功，已确认在库中。",
	"owned":               "已在库中，无需领取。",
	"failed":              "浏览器未能安全完成操作（未下单），会自动重试。",
	"unconfirmed":         "已点击下单但未能确认结果，请到 Epic 游戏库核对后再点“恢复”。",
	"login_required":      "Epic 登录已失效：请在账户管理点击“去验证”重新登录，登录后会自动领取。",
	"captcha_required":    "需要你完成验证才能领取：请在账户管理点击“去验证”，在弹窗中完成验证后会自动继续领取。",
	"two_factor_required": "需要二次验证：请在账户管理点击“去验证”。",
	"terms_required":      "Epic 要求同意条款：请点击“去验证”，在弹窗中自行确认后会继续领取。",
	"price_changed":       "价格不是 0 或需要付款方式，已停止，未下单。",
}
var statuses = Statuses

// Claim never returns partial results. Invalid output and process errors are
// intentionally generic: browser diagnostics may contain credentials or tokens.
func (e *Exec) Claim(ctx context.Context, req Request) ([]Result, error) {
	if len(e.Command) == 0 || strings.TrimSpace(e.Command[0]) == "" {
		return nil, errors.New("worker command is not configured")
	}
	if req.AccountID <= 0 || len(req.Games) > 100 {
		return nil, errors.New("invalid worker request")
	}
	expected := map[string]bool{}
	for _, g := range req.Games {
		k := g.Namespace + "\x00" + g.ID
		if g.ID == "" || g.Namespace == "" || strings.ContainsAny(g.ID+g.Namespace, "\x00\r\n") || expected[k] {
			return nil, errors.New("invalid offer identity")
		}
		expected[k] = true
	}
	if len(req.Games) == 0 {
		return []Result{}, nil
	}
	timeout := e.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	limit := e.MaxOutputBytes
	if limit <= 0 {
		limit = 1 << 20
	}
	if limit > 16<<20 {
		limit = 16 << 20
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	in, err := json.Marshal(req)
	if err != nil {
		return nil, errors.New("cannot encode worker request")
	}
	cmd := exec.CommandContext(ctx, e.Command[0], e.Command[1:]...)
	if e.Env != nil {
		cmd.Env = append(os.Environ(), e.Env(ctx, req.AccountID)...)
	}
	configureProcess(cmd)
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdin = bytes.NewReader(in)
	out := &boundedBuffer{limit: limit}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	if err = cmd.Run(); err != nil {
		if ctx.Err() != nil {
			killProcessGroup(cmd)
		}
		if ctx.Err() != nil {
			return nil, errors.New("worker timed out or was canceled")
		}
		return nil, errors.New("worker failed")
	}
	if out.exceeded {
		return nil, errors.New("worker output exceeded limit")
	}
	var results []Result
	dec := json.NewDecoder(bytes.NewReader(out.b))
	dec.DisallowUnknownFields()
	if dec.Decode(&results) != nil {
		return nil, errors.New("invalid worker response")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return nil, errors.New("invalid worker response")
	}
	if len(results) != len(expected) {
		return nil, errors.New("incomplete worker response")
	}
	seen := map[string]bool{}
	for i, r := range results {
		k := r.Namespace + "\x00" + r.OfferID
		message, ok := statuses[r.Status]
		if !expected[k] || seen[k] || !ok {
			return nil, errors.New("invalid worker result")
		}
		seen[k] = true
		results[i].Message = message
	}
	return results, nil
}

type boundedBuffer struct {
	b        []byte
	limit    int64
	exceeded bool
}

func (w *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := int(w.limit) - len(w.b)
	if n > remaining {
		w.exceeded = true
		p = p[:remaining]
	}
	w.b = append(w.b, p...)
	return n, nil
}
