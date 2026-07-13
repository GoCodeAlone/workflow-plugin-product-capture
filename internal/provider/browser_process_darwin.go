//go:build darwin

package provider

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

type unixProcessIdentity struct {
	processGroupID int
	startTime      int64
}

type unixBrowserProcessPolicy struct{}

func newBrowserProcessPolicy() browserProcessPolicy {
	return unixBrowserProcessPolicy{}
}

func (unixBrowserProcessPolicy) Configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func (unixBrowserProcessPolicy) TerminateGroup(cmd *exec.Cmd, grace time.Duration) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	processID := cmd.Process.Pid
	identity, exists, err := readUnixProcessIdentity(processID)
	if err != nil {
		return fmt.Errorf("read browser process-group identity: %w", err)
	}
	if !exists {
		return requireExitedUnixProcessGroup(processID)
	}
	if identity.processGroupID != processID {
		return errors.New("browser command is not the leader of its dedicated process group")
	}
	if err := syscall.Kill(-processID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("terminate browser process group: %w", err)
	}
	if grace > 0 {
		time.Sleep(grace)
	}
	current, exists, err := readUnixProcessIdentity(processID)
	if err != nil {
		return fmt.Errorf("recheck browser process-group identity: %w", err)
	}
	if !exists {
		return requireExitedUnixProcessGroup(processID)
	}
	if current.processGroupID != processID || current.startTime != identity.startTime {
		return errors.New("browser process-group leader identity changed before SIGKILL")
	}
	if err := syscall.Kill(-processID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill browser process group: %w", err)
	}
	return nil
}

func readUnixProcessIdentity(processID int) (unixProcessIdentity, bool, error) {
	process, err := unix.SysctlKinfoProc("kern.proc.pid", processID)
	if errors.Is(err, syscall.ESRCH) || errors.Is(err, syscall.ENOENT) {
		return unixProcessIdentity{}, false, nil
	}
	if err != nil {
		return unixProcessIdentity{}, false, err
	}
	if process == nil || process.Proc.P_pid != int32(processID) {
		return unixProcessIdentity{}, false, nil
	}
	startTime := process.Proc.P_starttime.Sec*1_000_000 + int64(process.Proc.P_starttime.Usec)
	return unixProcessIdentity{processGroupID: int(process.Eproc.Pgid), startTime: startTime}, true, nil
}

func requireExitedUnixProcessGroup(processID int) error {
	err := syscall.Kill(-processID, 0)
	switch {
	case errors.Is(err, syscall.ESRCH):
		return nil
	case err == nil, errors.Is(err, syscall.EPERM):
		return errors.New("browser process-group leader is unavailable while the group remains live")
	default:
		return fmt.Errorf("inspect browser process group: %w", err)
	}
}
