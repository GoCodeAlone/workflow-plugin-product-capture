//go:build !linux

package provider

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"time"
)

type otherBrowserProcessPolicy struct{}

func newBrowserProcessPolicy() browserProcessPolicy {
	return otherBrowserProcessPolicy{}
}

func (otherBrowserProcessPolicy) Configure(*exec.Cmd) {}

func (otherBrowserProcessPolicy) TerminateGroup(cmd *exec.Cmd, grace time.Duration) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if runtime.GOOS == "windows" {
		if err := runBoundedBrowserCleanupCommand(grace, "taskkill", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F"); err == nil {
			return nil
		}
		killErr := cmd.Process.Kill()
		if errors.Is(killErr, os.ErrProcessDone) {
			return nil
		}
		return killErr
	}
	interruptErr := cmd.Process.Signal(os.Interrupt)
	if errors.Is(interruptErr, os.ErrProcessDone) {
		return nil
	}
	if grace > 0 {
		time.Sleep(grace)
	}
	killErr := cmd.Process.Kill()
	if errors.Is(killErr, os.ErrProcessDone) {
		return nil
	}
	return killErr
}
