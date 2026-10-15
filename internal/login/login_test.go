package login

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type memStore struct {
	mu sync.Mutex
	h  []string
}

func (m *memStore) SetLoginStatus(_ context.Context, _ int64, s, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.h = append(m.h, s)
	return nil
}
func (m *memStore) last() string { m.mu.Lock(); defer m.mu.Unlock(); return m.h[len(m.h)-1] }

// Fake worker: asks for a code, and reports ready only if the right code arrives.
const fake = `#!/bin/sh
read creds
case "$creds" in *'"password":"pw"'*) ;; *) echo '{"status":"login_failed"}'; exit 0;; esac
echo '{"status":"code_required"}'
read code
case "$code" in *123456*) echo '{"status":"ready"}';; *) echo '{"status":"login_failed"}';; esac
`

func wait(t *testing.T, m *memStore, want string) {
	for i := 0; i < 200; i++ {
		if m.last() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("status %q, want %q", m.last(), want)
}

func TestCodeRelay(t *testing.T) {
	p := filepath.Join(t.TempDir(), "w.sh")
	os.WriteFile(p, []byte(fake), 0o700)
	m := &memStore{}
	r := &Runner{Command: []string{p}, Store: m, Timeout: 5 * time.Second}
	if err := r.SubmitCode(1, "123456"); err == nil {
		t.Fatal("code accepted without a run")
	}
	if err := r.Start(1, Credentials{Email: "a@example.com", Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(1, Credentials{}); err != ErrBusy {
		t.Fatal("concurrent start allowed")
	}
	wait(t, m, "code_required")
	if err := r.SubmitCode(1, "12 34"); err == nil {
		t.Fatal("bad code format accepted")
	}
	if err := r.SubmitCode(1, "123456"); err != nil {
		t.Fatal(err)
	}
	wait(t, m, "ready")
	for i := 0; i < 100 && r.Running(1); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if r.Running(1) {
		t.Fatal("run not cleaned up")
	}
}

func TestWrongPasswordAndCrash(t *testing.T) {
	p := filepath.Join(t.TempDir(), "w.sh")
	os.WriteFile(p, []byte(fake), 0o700)
	m := &memStore{}
	r := &Runner{Command: []string{p}, Store: m}
	r.Start(2, Credentials{Email: "a@example.com", Password: "nope"})
	wait(t, m, "login_failed")
	m2 := &memStore{}
	r2 := &Runner{Command: []string{"/bin/false"}, Store: m2}
	r2.Start(3, Credentials{})
	wait(t, m2, "error")
}

func TestCancelReflectsImmediately(t *testing.T) {
	p := filepath.Join(t.TempDir(), "w.sh")
	os.WriteFile(p, []byte(fake), 0o700)
	m := &memStore{}
	r := &Runner{Command: []string{p}, Store: m, Timeout: 5 * time.Second}
	r.Start(4, Credentials{Password: "pw"})
	wait(t, m, "code_required")
	if err := r.Send(4, map[string]any{"cancel": true}); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	got := m.h[len(m.h)-1]
	m.mu.Unlock()
	if got != "cancelled" {
		t.Fatalf("status after cancel = %q", got)
	}
}

func TestParseCookies(t *testing.T) {
	j := `[{"domain":".epicgames.com","name":"EPIC_SESSION_AP","value":"x","path":"/","secure":true,"httpOnly":true},{"domain":".google.com","name":"g","value":"y"}]`
	c, err := ParseCookies(j)
	if err != nil || len(c) != 1 || c[0].Name != "EPIC_SESSION_AP" || !c[0].HTTPOnly {
		t.Fatalf("json: %+v %v", c, err)
	}
	n := "# Netscape HTTP Cookie File\n#HttpOnly_.epicgames.com\tTRUE\t/\tTRUE\t1893456000\tEPIC_EG1\tz\nexample.com\tFALSE\t/\tFALSE\t0\ta\tb\n"
	c, err = ParseCookies(n)
	if err != nil || len(c) != 1 || c[0].Name != "EPIC_EG1" || c[0].Expires != 1893456000 {
		t.Fatalf("netscape: %+v %v", c, err)
	}
	if _, err = ParseCookies("[]"); err == nil {
		t.Fatal("empty accepted")
	}
}
