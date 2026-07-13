//go:build linux

package provider

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLinuxBrowserProcessPolicyRejectsReusedLeaderBeforeKill(t *testing.T) {
	identities := []linuxProcessIdentity{
		{processGroupID: 10, startTime: "100"},
		{processGroupID: 10, startTime: "200"},
	}
	var signals []syscall.Signal
	policy := linuxBrowserProcessPolicy{
		readIdentity: func(int) (linuxProcessIdentity, bool, error) {
			identity := identities[0]
			identities = identities[1:]
			return identity, true, nil
		},
		signalGroup: func(_ int, signal syscall.Signal) error {
			signals = append(signals, signal)
			return nil
		},
		sleep: func(time.Duration) {},
	}

	err := policy.TerminateGroup(&exec.Cmd{Process: &os.Process{Pid: 10}}, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("TerminateGroup error = %v, want identity changed", err)
	}
	if !reflect.DeepEqual(signals, []syscall.Signal{syscall.SIGTERM}) {
		t.Fatalf("signals = %v, want TERM only", signals)
	}
}

func TestLinuxBrowserProcessPolicyRejectsMissingLeaderWithLiveGroup(t *testing.T) {
	reads := 0
	var signals []syscall.Signal
	policy := linuxBrowserProcessPolicy{
		readIdentity: func(int) (linuxProcessIdentity, bool, error) {
			reads++
			if reads == 1 {
				return linuxProcessIdentity{processGroupID: 10, startTime: "100"}, true, nil
			}
			return linuxProcessIdentity{}, false, nil
		},
		groupExists: func(int) (bool, error) { return true, nil },
		signalGroup: func(_ int, signal syscall.Signal) error {
			signals = append(signals, signal)
			return nil
		},
		sleep: func(time.Duration) {},
	}

	err := policy.TerminateGroup(&exec.Cmd{Process: &os.Process{Pid: 10}}, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "identity is unavailable") {
		t.Fatalf("TerminateGroup error = %v, want unavailable identity", err)
	}
	if !reflect.DeepEqual(signals, []syscall.Signal{syscall.SIGTERM}) {
		t.Fatalf("signals = %v, want TERM only", signals)
	}
}

func TestLinuxBrowserProcessPolicyAcceptsMissingLeaderAfterGroupExit(t *testing.T) {
	reads := 0
	var signals []syscall.Signal
	policy := linuxBrowserProcessPolicy{
		readIdentity: func(int) (linuxProcessIdentity, bool, error) {
			reads++
			if reads == 1 {
				return linuxProcessIdentity{processGroupID: 10, startTime: "100"}, true, nil
			}
			return linuxProcessIdentity{}, false, nil
		},
		groupExists: func(int) (bool, error) { return false, nil },
		signalGroup: func(_ int, signal syscall.Signal) error {
			signals = append(signals, signal)
			return nil
		},
		sleep: func(time.Duration) {},
	}

	if err := policy.TerminateGroup(&exec.Cmd{Process: &os.Process{Pid: 10}}, time.Millisecond); err != nil {
		t.Fatalf("TerminateGroup: %v", err)
	}
	if !reflect.DeepEqual(signals, []syscall.Signal{syscall.SIGTERM}) {
		t.Fatalf("signals = %v, want TERM only", signals)
	}
}

func TestCleanupBrowserCommandAfterErrorNeverSignalsReapedCommand(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	policy := linuxBrowserProcessPolicy{
		readIdentity: func(int) (linuxProcessIdentity, bool, error) {
			return linuxProcessIdentity{}, false, errors.New("reaped command identity was read")
		},
		signalGroup: func(int, syscall.Signal) error {
			return errors.New("reaped command was signaled")
		},
	}
	previousPolicy := browserProcesses
	browserProcesses = policy
	t.Cleanup(func() { browserProcesses = previousPolicy })
	if err := cleanupBrowserCommandAfterError(context.Background(), cmd); err != nil {
		t.Fatalf("cleanupBrowserCommandAfterError: %v", err)
	}
}
