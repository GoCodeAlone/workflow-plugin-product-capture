//go:build linux

package provider

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type linuxProcessIdentity struct {
	processGroupID int
	startTime      string
	state          byte
}

type linuxBrowserProcessPolicy struct {
	readIdentity        func(int) (linuxProcessIdentity, bool, error)
	groupExists         func(int) (bool, error)
	groupHasLiveMembers func(int) (bool, error)
	signalGroup         func(int, syscall.Signal) error
	sleep               func(time.Duration)
}

const (
	processGroupExitInitialPollInterval = 10 * time.Millisecond
	processGroupExitMaxPollInterval     = 100 * time.Millisecond
)

func newBrowserProcessPolicy() browserProcessPolicy {
	return linuxBrowserProcessPolicy{}
}

func (linuxBrowserProcessPolicy) Configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func (p linuxBrowserProcessPolicy) TerminateGroup(cmd *exec.Cmd, grace time.Duration) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	processID := cmd.Process.Pid
	identity, exists, err := p.readProcessIdentity(processID)
	if err != nil {
		return fmt.Errorf("read browser process-group identity: %w", err)
	}
	if !exists {
		return p.requireExitedProcessGroup(processID)
	}
	if identity.processGroupID != processID {
		return errors.New("browser command is not the leader of its dedicated process group")
	}
	termErr := p.signalProcessGroup(processID, syscall.SIGTERM)
	if errors.Is(termErr, syscall.ESRCH) {
		return nil
	}
	if termErr != nil {
		return fmt.Errorf("terminate browser process group: %w", termErr)
	}
	if grace > 0 {
		p.sleepFor(grace)
	}
	current, exists, err := p.readProcessIdentity(processID)
	if err != nil {
		return fmt.Errorf("recheck browser process-group identity: %w", err)
	}
	if !exists {
		return p.requireExitedProcessGroup(processID)
	}
	if current.processGroupID != processID || current.startTime != identity.startTime {
		return errors.New("browser process-group leader identity changed before SIGKILL")
	}
	killErr := p.signalProcessGroup(processID, syscall.SIGKILL)
	if killErr != nil && !errors.Is(killErr, syscall.ESRCH) {
		return fmt.Errorf("kill browser process group: %w", killErr)
	}
	if errors.Is(killErr, syscall.ESRCH) {
		return nil
	}
	return p.waitForExitedProcessGroup(processID, grace)
}

func (p linuxBrowserProcessPolicy) readProcessIdentity(processID int) (linuxProcessIdentity, bool, error) {
	if p.readIdentity != nil {
		return p.readIdentity(processID)
	}
	return readLinuxProcessIdentity(processID)
}

func (p linuxBrowserProcessPolicy) processGroupExists(processID int) (bool, error) {
	if p.groupExists != nil {
		return p.groupExists(processID)
	}
	err := syscall.Kill(-processID, 0)
	switch {
	case err == nil, errors.Is(err, syscall.EPERM):
		return true, nil
	case errors.Is(err, syscall.ESRCH):
		return false, nil
	default:
		return false, err
	}
}

func (p linuxBrowserProcessPolicy) requireExitedProcessGroup(processID int) error {
	exists, err := p.processGroupExists(processID)
	if err != nil {
		return fmt.Errorf("inspect browser process group: %w", err)
	}
	if exists {
		return errors.New("browser process-group leader identity is unavailable while the group remains live")
	}
	return nil
}

func (p linuxBrowserProcessPolicy) processGroupHasLiveMembers(processGroupID int) (bool, error) {
	if p.groupHasLiveMembers != nil {
		return p.groupHasLiveMembers(processGroupID)
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		processID, err := strconv.Atoi(entry.Name())
		if err != nil || processID <= 0 {
			continue
		}
		identity, exists, err := readLinuxProcessIdentity(processID)
		if err != nil {
			return false, err
		}
		if exists && identity.processGroupID == processGroupID && identity.state != 'Z' && identity.state != 'X' && identity.state != 'x' {
			return true, nil
		}
	}
	return false, nil
}

func (p linuxBrowserProcessPolicy) waitForExitedProcessGroup(processGroupID int, grace time.Duration) error {
	deadline := time.Now().Add(grace)
	pollInterval := processGroupExitInitialPollInterval
	for {
		live, err := p.processGroupHasLiveMembers(processGroupID)
		if err != nil {
			return fmt.Errorf("inspect browser process group after SIGKILL: %w", err)
		}
		if !live {
			return nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return errors.New("browser process group survived SIGKILL")
		}
		p.sleepFor(min(pollInterval, remaining))
		pollInterval = min(2*pollInterval, processGroupExitMaxPollInterval)
	}
}

func (p linuxBrowserProcessPolicy) signalProcessGroup(processID int, signal syscall.Signal) error {
	if p.signalGroup != nil {
		return p.signalGroup(processID, signal)
	}
	return syscall.Kill(-processID, signal)
}

func (p linuxBrowserProcessPolicy) sleepFor(duration time.Duration) {
	if p.sleep != nil {
		p.sleep(duration)
		return
	}
	time.Sleep(duration)
}

func readLinuxProcessIdentity(processID int) (linuxProcessIdentity, bool, error) {
	var malformed error
	for range 3 {
		data, err := os.ReadFile("/proc/" + strconv.Itoa(processID) + "/stat")
		if err != nil {
			if linuxProcessDisappeared(err) {
				return linuxProcessIdentity{}, false, nil
			}
			return linuxProcessIdentity{}, false, err
		}
		close := bytes.LastIndexByte(data, ')')
		if close < 0 || close+2 > len(data) {
			malformed = errors.New("malformed procfs stat")
			continue
		}
		fields := strings.Fields(string(data[close+2:]))
		if len(fields) < 20 || len(fields[0]) != 1 || fields[19] == "" {
			malformed = errors.New("malformed procfs stat")
			continue
		}
		parentProcessID, parentErr := strconv.Atoi(fields[1])
		processGroupID, groupErr := strconv.Atoi(fields[2])
		sessionID, sessionErr := strconv.Atoi(fields[3])
		if parentErr != nil || groupErr != nil || sessionErr != nil || parentProcessID < 0 || processGroupID < 0 || sessionID < 0 || !decimalDigits(fields[19]) {
			malformed = errors.New("malformed procfs process identity")
			continue
		}
		return linuxProcessIdentity{processGroupID: processGroupID, startTime: fields[19], state: fields[0][0]}, true, nil
	}
	return linuxProcessIdentity{}, false, malformed
}

func linuxProcessDisappeared(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH)
}

func decimalDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
