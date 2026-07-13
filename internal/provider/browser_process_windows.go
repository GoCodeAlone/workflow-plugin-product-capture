//go:build windows

package provider

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"

	"golang.org/x/sys/windows"
)

type windowsProcessIdentity struct {
	creationTime int64
}

type windowsBrowserProcessPolicy struct{}

func newBrowserProcessPolicy() browserProcessPolicy {
	return windowsBrowserProcessPolicy{}
}

func (windowsBrowserProcessPolicy) Configure(*exec.Cmd) {}

func (windowsBrowserProcessPolicy) TerminateGroup(cmd *exec.Cmd, grace time.Duration) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	processID := cmd.Process.Pid
	identity, exists, err := readWindowsProcessIdentity(processID)
	if err != nil {
		return fmt.Errorf("read browser process-tree identity: %w", err)
	}
	if !exists {
		return nil
	}
	current, exists, err := readWindowsProcessIdentity(processID)
	if err != nil {
		return fmt.Errorf("recheck browser process-tree identity: %w", err)
	}
	if !exists {
		return nil
	}
	if current.creationTime != identity.creationTime {
		return errors.New("browser process-tree leader identity changed before taskkill")
	}
	treeErr := runBoundedBrowserCleanupCommand(grace, "taskkill", "/PID", strconv.Itoa(processID), "/T", "/F")
	if treeErr == nil {
		return nil
	}
	killErr := cmd.Process.Kill()
	if errors.Is(killErr, os.ErrProcessDone) {
		killErr = nil
	}
	if killErr == nil {
		return fmt.Errorf("terminate browser process tree: %w", treeErr)
	}
	return errors.Join(fmt.Errorf("terminate browser process tree: %w", treeErr), fmt.Errorf("kill browser parent: %w", killErr))
}

func readWindowsProcessIdentity(processID int) (windowsProcessIdentity, bool, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(processID))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return windowsProcessIdentity{}, false, nil
	}
	if err != nil {
		return windowsProcessIdentity{}, false, err
	}
	defer windows.CloseHandle(handle)
	var creationTime, exitTime, kernelTime, userTime windows.Filetime
	if err := windows.GetProcessTimes(handle, &creationTime, &exitTime, &kernelTime, &userTime); err != nil {
		return windowsProcessIdentity{}, false, err
	}
	return windowsProcessIdentity{creationTime: creationTime.Nanoseconds()}, true, nil
}
