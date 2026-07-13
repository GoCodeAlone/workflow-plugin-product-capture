//go:build plan9 || js || wasip1

package provider

import (
	"errors"
	"os"
	"os/exec"
	"time"
)

type unsupportedNonUnixBrowserProcessPolicy struct{}

func newBrowserProcessPolicy() browserProcessPolicy {
	return unsupportedNonUnixBrowserProcessPolicy{}
}

func (unsupportedNonUnixBrowserProcessPolicy) Configure(*exec.Cmd) {}

func (unsupportedNonUnixBrowserProcessPolicy) TerminateGroup(cmd *exec.Cmd, _ time.Duration) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}
