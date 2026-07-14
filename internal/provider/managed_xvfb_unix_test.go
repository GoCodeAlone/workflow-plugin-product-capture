//go:build !windows

package provider

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestManagedXvfbScriptEscalatesAndReapsUncooperativeServer(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "xvfb.pid")
	xvfb := filepath.Join(dir, "Xvfb")
	if err := os.WriteFile(xvfb, []byte(`#!/bin/sh
printf '%s\n' "$$" > "$PRODUCT_CAPTURE_TEST_XVFB_PID_PATH"
printf '77\n' >&3
trap '' TERM
while :; do :; done
`), 0o700); err != nil {
		t.Fatal(err)
	}
	node := filepath.Join(dir, "node")
	if err := os.WriteFile(node, []byte("#!/bin/sh\nprintf '<html></html>'\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", managedXvfbScript, "product-capture-xvfb", "node", "ignored.js")
	cmd.Env = append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"PRODUCT_CAPTURE_TEST_XVFB_PID_PATH="+pidPath,
	)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatalf("start managed Xvfb command: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(pidPath); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stat fake Xvfb PID: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("fake Xvfb did not start within 2s; output=%s", output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cleanupStarted := time.Now()
	err := cmd.Wait()

	pidData, readErr := os.ReadFile(pidPath)
	if readErr != nil {
		t.Fatalf("read fake Xvfb PID: %v; output=%s", readErr, output.String())
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if parseErr != nil {
		t.Fatalf("parse fake Xvfb PID: %v", parseErr)
	}
	emergencyCleanupNeeded := true
	t.Cleanup(func() {
		if emergencyCleanupNeeded {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})

	if err != nil {
		t.Fatalf("managed Xvfb command: %v; output=%s", err, output.String())
	}
	if elapsed := time.Since(cleanupStarted); elapsed > 2*time.Second {
		t.Fatalf("managed Xvfb cleanup took %s after startup, want at most 2s", elapsed)
	}
	signalErr := syscall.Kill(pid, 0)
	if errors.Is(signalErr, syscall.ESRCH) {
		emergencyCleanupNeeded = false
	} else {
		t.Fatalf("managed Xvfb PID %d remains after cleanup: %v", pid, signalErr)
	}
}
