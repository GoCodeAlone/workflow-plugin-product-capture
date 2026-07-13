//go:build aix || dragonfly || freebsd || illumos || netbsd || openbsd || solaris

package provider

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

type unsupportedUnixBrowserProcessPolicy struct{}

func newBrowserProcessPolicy() browserProcessPolicy {
	return unsupportedUnixBrowserProcessPolicy{}
}

func (unsupportedUnixBrowserProcessPolicy) Configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func (unsupportedUnixBrowserProcessPolicy) TerminateGroup(cmd *exec.Cmd, grace time.Duration) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	if grace > 0 {
		time.Sleep(grace)
	}
	if err := syscall.Kill(-cmd.Process.Pid, 0); errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return errors.New("browser process group requires stable start identity before forced cleanup on this platform")
}
