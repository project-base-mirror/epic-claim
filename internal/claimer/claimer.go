// Package claimer drives an external claimer worker.
package claimer

import (
	"context"
	"encoding/json"
	"os/exec"

	"github.com/project-base-mirror/epic-claim/internal/epic"
	"github.com/project-base-mirror/epic-claim/internal/store"
)

type Request struct {
	Email    string      `json:"email"`
	Password string      `json:"password"`
	TOTP     string      `json:"totp,omitempty"`
	Proxy    string      `json:"proxy,omitempty"`
	Games    []epic.Game `json:"games"`
}

type Result struct {
	OfferID string            `json:"offer_id"`
	Status  store.ClaimStatus `json:"status"`
	Message string            `json:"message"`
}

// Claimer is implemented by exec- and HTTP-based worker adapters.
type Claimer interface {
	Claim(ctx context.Context, req Request) ([]Result, error)
}

// Exec runs a worker command, writing the request as JSON to stdin and
// reading []Result JSON from stdout. TODO: timeouts, stderr logging, adapters
// for existing tools (epic-freebies-helper, epicgames-freegames-node).
type Exec struct{ Command []string }

func (e *Exec) Claim(ctx context.Context, req Request) ([]Result, error) {
	in, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, e.Command[0], e.Command[1:]...)
	cmd.Stdin = bytesReader(in)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var res []Result
	return res, json.Unmarshal(out, &res)
}
