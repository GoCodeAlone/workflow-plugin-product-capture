//go:build darwin

package provider

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestUnixBrowserProcessPolicyTerminatesDedicatedProcessTree(t *testing.T) {
	marker := t.TempDir() + "/child.pid"
	cmd := exec.Command("sh", "-c", `sleep 30 & child=$!; printf '%s\n' "$child" >"$1"; trap '' TERM INT; while :; do sleep 1; done`, "browser-parent", marker)
	policy := newBrowserProcessPolicy()
	policy.Configure(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	var childPID int
	deadline := time.Now().Add(2 * time.Second)
	for childPID == 0 && time.Now().Before(deadline) {
		data, err := os.ReadFile(marker)
		if err == nil {
			childPID, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID <= 0 {
		t.Fatal("browser child PID was not published")
	}
	if err := policy.TerminateGroup(cmd, 50*time.Millisecond); err != nil {
		t.Fatalf("TerminateGroup: %v", err)
	}
	_ = cmd.Wait()

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(childPID, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("browser child %d survived process-tree cleanup", childPID)
}
