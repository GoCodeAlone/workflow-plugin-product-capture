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

func TestLinuxBrowserProcessPolicyBacksOffWhileWaitingForKilledGroup(t *testing.T) {
	identities := []linuxProcessIdentity{
		{processGroupID: 10, startTime: "100"},
		{processGroupID: 10, startTime: "100"},
	}
	var signals []syscall.Signal
	liveChecks := 0
	var sleeps []time.Duration
	policy := linuxBrowserProcessPolicy{
		readIdentity: func(int) (linuxProcessIdentity, bool, error) {
			identity := identities[0]
			identities = identities[1:]
			return identity, true, nil
		},
		groupHasLiveMembers: func(int) (bool, error) {
			liveChecks++
			return liveChecks <= 4, nil
		},
		signalGroup: func(_ int, signal syscall.Signal) error {
			signals = append(signals, signal)
			return nil
		},
		sleep: func(duration time.Duration) { sleeps = append(sleeps, duration) },
	}

	if err := policy.TerminateGroup(&exec.Cmd{Process: &os.Process{Pid: 10}}, time.Second); err != nil {
		t.Fatalf("TerminateGroup: %v", err)
	}
	if !reflect.DeepEqual(signals, []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL}) {
		t.Fatalf("signals = %v, want TERM then KILL", signals)
	}
	if liveChecks != 5 {
		t.Fatalf("live group checks = %d, want 5", liveChecks)
	}
	wantSleeps := []time.Duration{time.Second, 10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond, 80 * time.Millisecond}
	if !reflect.DeepEqual(sleeps, wantSleeps) {
		t.Fatalf("sleeps = %v, want TERM grace then KILL backoff %v", sleeps, wantSleeps)
	}
}

func TestLinuxBrowserProcessPolicyRejectsGroupThatSurvivesKill(t *testing.T) {
	identities := []linuxProcessIdentity{
		{processGroupID: 10, startTime: "100"},
		{processGroupID: 10, startTime: "100"},
	}
	policy := linuxBrowserProcessPolicy{
		readIdentity: func(int) (linuxProcessIdentity, bool, error) {
			identity := identities[0]
			identities = identities[1:]
			return identity, true, nil
		},
		groupHasLiveMembers: func(int) (bool, error) { return true, nil },
		signalGroup:         func(int, syscall.Signal) error { return nil },
	}

	err := policy.TerminateGroup(&exec.Cmd{Process: &os.Process{Pid: 10}}, 0)
	if err == nil || !strings.Contains(err.Error(), "survived SIGKILL") {
		t.Fatalf("TerminateGroup error = %v, want survived SIGKILL", err)
	}
}

func TestLinuxProcessDisappearanceErrors(t *testing.T) {
	for _, err := range []error{
		os.ErrNotExist,
		&os.PathError{Op: "read", Path: "/proc/10/stat", Err: syscall.ESRCH},
	} {
		if !linuxProcessDisappeared(err) {
			t.Errorf("linuxProcessDisappeared(%v) = false, want true", err)
		}
	}
	if linuxProcessDisappeared(&os.PathError{Op: "read", Path: "/proc/10/stat", Err: syscall.EPERM}) {
		t.Fatal("permission error was treated as process disappearance")
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
