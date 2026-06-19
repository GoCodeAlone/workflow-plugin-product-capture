//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package provider

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-product-capture/internal/snapshot"
)

func TestWriteSnapshotOverridesRestrictiveUmaskForHostReadableArtifact(t *testing.T) {
	oldUmask := syscall.Umask(0o077)
	t.Cleanup(func() {
		syscall.Umask(oldUmask)
	})

	path := filepath.Join(t.TempDir(), ProductJSONArtifact)
	err := writeSnapshot(path, snapshot.Snapshot{
		Provider:                 "browser_capture",
		URL:                      "https://www.amazon.com/dp/B08H75RTZ8",
		Title:                    "Xbox Series X",
		CapturedAt:               time.Unix(0, 0).UTC(),
		RequiresUserConfirmation: true,
	})
	if err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	assertFileMode(t, path, 0o644)
}
