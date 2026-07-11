//go:build !linux

package provider

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"syscall"
	"time"
)

type otherBrowserProcessPolicy struct{}

func newBrowserProcessPolicy() browserProcessPolicy {
	return otherBrowserProcessPolicy{}
}

func (otherBrowserProcessPolicy) Configure(*exec.Cmd) {}

func (otherBrowserProcessPolicy) OwnsListeningPort(pid, port int) bool {
	if pid <= 0 || port <= 0 || port > 65535 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	defer func() { _ = process.Release() }()
	if runtime.GOOS == "windows" {
		return true
	}
	return process.Signal(syscall.Signal(0)) == nil
}

func (otherBrowserProcessPolicy) TerminateGroup(cmd *exec.Cmd, grace time.Duration) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if runtime.GOOS == "windows" {
		if err := exec.Command("taskkill", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F").Run(); err == nil {
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
		timer := time.NewTimer(grace)
		<-timer.C
	}
	killErr := cmd.Process.Kill()
	if errors.Is(killErr, os.ErrProcessDone) {
		return nil
	}
	return killErr
}
