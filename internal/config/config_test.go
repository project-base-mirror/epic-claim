package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultsAndValidation(t *testing.T) {
	c, e := Load(filepath.Join(t.TempDir(), "missing"))
	if e != nil || c.Listen != "127.0.0.1:8080" || c.Claimer.Timeout != 5*time.Minute {
		t.Fatalf("%+v %v", c, e)
	}
	for _, raw := range []string{"locale: zh-CN\n", "schedule:\n  check_interval: 0s\n", "claimer:\n  type: http\n", "unknown_key: true\n", "schedule:\n  account_delay: -1s\n"} {
		p := filepath.Join(t.TempDir(), "config.yaml")
		os.WriteFile(p, []byte(raw), 0600)
		if _, e := Load(p); e == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}
