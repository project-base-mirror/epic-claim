//go:build !linux

package login

import "os/exec"

func configureProcess(cmd *exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) {}
