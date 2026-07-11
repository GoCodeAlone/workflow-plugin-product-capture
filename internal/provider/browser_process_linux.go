//go:build linux

package provider

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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

func (linuxBrowserProcessPolicy) OwnsListeningPort(pid, port int) bool {
	if pid <= 0 || port <= 0 || port > 65535 {
		return false
	}
	inodes := listeningSocketInodes(port)
	if len(inodes) == 0 {
		return false
	}
	for processID := range browserProcessTree(pid) {
		entries, err := os.ReadDir(filepath.Join("/proc", strconv.Itoa(processID), "fd"))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			target, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(processID), "fd", entry.Name()))
			if err != nil || !strings.HasPrefix(target, "socket:[") || !strings.HasSuffix(target, "]") {
				continue
			}
			if _, ok := inodes[strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")]; ok {
				return true
			}
		}
	}
	return false
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

func browserProcessTree(root int) map[int]struct{} {
	tree := map[int]struct{}{root: {}}
	queue := []int{root}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		childrenPath := fmt.Sprintf("/proc/%d/task/%d/children", current, current)
		data, err := os.ReadFile(childrenPath)
		if err != nil {
			continue
		}
		for _, field := range strings.Fields(string(data)) {
			child, err := strconv.Atoi(field)
			if err != nil {
				continue
			}
			if _, seen := tree[child]; seen {
				continue
			}
			tree[child] = struct{}{}
			queue = append(queue, child)
		}
	}
	return tree
}

func listeningSocketInodes(port int) map[string]struct{} {
	inodes := make(map[string]struct{})
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 10 || fields[3] != "0A" {
				continue
			}
			separator := strings.LastIndexByte(fields[1], ':')
			if separator < 0 {
				continue
			}
			parsedPort, err := strconv.ParseInt(fields[1][separator+1:], 16, 32)
			if err == nil && int(parsedPort) == port {
				inodes[fields[9]] = struct{}{}
			}
		}
	}
	return inodes
}
