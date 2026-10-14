//go:build !linux

package claimer

import "os/exec"

func configureProcess(cmd *exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) {}
