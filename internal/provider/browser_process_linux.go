//go:build linux

package provider

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

type linuxBrowserProcessPolicy struct{}

func newBrowserProcessPolicy() browserProcessPolicy {
	return linuxBrowserProcessPolicy{}
}

func (linuxBrowserProcessPolicy) Configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func (linuxBrowserProcessPolicy) TerminateGroup(cmd *exec.Cmd, grace time.Duration) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	termErr := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	if errors.Is(termErr, syscall.ESRCH) {
		return nil
	}
	if termErr != nil {
		return fmt.Errorf("terminate browser process group: %w", termErr)
	}
	if grace > 0 {
		timer := time.NewTimer(grace)
		<-timer.C
	}
	killErr := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if killErr != nil && !errors.Is(killErr, syscall.ESRCH) {
		return fmt.Errorf("kill browser process group: %w", killErr)
	}
	return nil
}
