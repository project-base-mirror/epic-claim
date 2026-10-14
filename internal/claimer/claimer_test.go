package claimer

import (
	"context"
	"fmt"
	"github.com/project-base-mirror/epic-claim/internal/epic"
	"os"
	"strings"
	"testing"
	"time"
)

func TestWorkerHelper(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "--worker-test" {
		return
	}
	mode := os.Args[len(os.Args)-1]
	switch mode {
	case "ok":
		fmt.Print(`[{"offer_id":"o","namespace":"n","status":"claimed","message":"secret-token"}]`)
	case "unknown":
		fmt.Print(`[{"offer_id":"x","namespace":"n","status":"claimed"}]`)
	case "missing":
		fmt.Print(`[]`)
	case "duplicate":
		fmt.Print(`[{"offer_id":"o","namespace":"n","status":"claimed"},{"offer_id":"o","namespace":"n","status":"claimed"}]`)
	case "bad-status":
		fmt.Print(`[{"offer_id":"o","namespace":"n","status":"success"}]`)
	case "extra":
		fmt.Print(`[{"offer_id":"o","namespace":"n","status":"claimed"}] {}`)
	case "malformed":
		fmt.Print(`no`)
	case "big":
		fmt.Print(strings.Repeat("x", 2000))
	case "slow":
		time.Sleep(time.Second)
	case "error":
		fmt.Fprint(os.Stderr, "secret-token")
		os.Exit(1)
	}
	os.Exit(0)
}
func TestExecValidation(t *testing.T) {
	req := Request{AccountID: 1, Games: []epic.Game{{ID: "o", Namespace: "n"}}}
	for _, mode := range []string{"ok", "unknown", "missing", "duplicate", "bad-status", "extra", "malformed", "big", "slow", "error"} {
		t.Run(mode, func(t *testing.T) {
			e := Exec{Command: []string{os.Args[0], "-test.run=^TestWorkerHelper$", "--", "--worker-test", mode}, Timeout: 3 * time.Second, MaxOutputBytes: 1024}
			if mode == "slow" {
				e.Timeout = 100 * time.Millisecond
			}
			res, err := e.Claim(context.Background(), req)
			if mode == "ok" {
				if err != nil || len(res) != 1 || strings.Contains(res[0].Message, "secret") {
					t.Fatalf("unsafe success: %v %v", res, err)
				}
			} else if err == nil || res != nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe failure: %v %v", res, err)
			}
		})
	}
}
func TestInvalidRequestAndEmptyCommand(t *testing.T) {
	for _, e := range []Exec{{}, {Command: []string{""}}} {
		if _, err := e.Claim(context.Background(), Request{}); err == nil {
			t.Fatal("empty command accepted")
		}
	}
	e := Exec{Command: []string{"not-run"}}
	for _, req := range []Request{{AccountID: 0}, {AccountID: 1, Games: []epic.Game{{ID: "o"}}}, {AccountID: 1, Games: []epic.Game{{ID: "o", Namespace: "n"}, {ID: "o", Namespace: "n"}}}} {
		if _, err := e.Claim(context.Background(), req); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
}
