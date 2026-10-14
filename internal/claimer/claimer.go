// Package claimer drives a bounded, fail-closed external browser worker.
package claimer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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
	Command        []string
	Timeout        time.Duration
	MaxOutputBytes int64
}

var statuses = map[store.ClaimStatus]string{
	"unsupported": "Current page does not expose verified offer/checkout evidence. Adapter review is required before resuming.",
	"claimed":     "Ownership verified after checkout.", "owned": "Already in library.",
	"failed": "The browser could not safely complete this offer.", "unconfirmed": "Checkout outcome is unconfirmed; ownership was not verified.",
	"login_required": "Sign in manually in this account's browser profile.", "captcha_required": "Complete the challenge manually.",
	"two_factor_required": "Complete two-factor authentication manually.", "terms_required": "Review and accept any terms manually.",
	"price_changed": "A final total of exactly zero could not be verified.",
}

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
