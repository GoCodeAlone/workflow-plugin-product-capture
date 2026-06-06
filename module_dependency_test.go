package productcapture_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductCaptureUsesPublicComputeCoreSDK(t *testing.T) {
	goMod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	text := string(goMod)
	if strings.Contains(text, "github.com/GoCodeAlone/workflow-compute") {
		t.Fatal("product-capture must not depend on private workflow-compute; use workflow-plugin-compute-core")
	}
	if !strings.Contains(text, "github.com/GoCodeAlone/workflow-plugin-compute-core") {
		t.Fatal("product-capture must consume the public workflow-plugin-compute-core SDK")
	}
	err = filepath.WalkDir("internal/plugin", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), "github.com/GoCodeAlone/workflow-compute/pkg/protocol") {
			t.Fatalf("%s imports private workflow-compute protocol package", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan internal/plugin: %v", err)
	}
	if _, err := os.Stat("internal/plugin/client.go"); err == nil {
		t.Fatal("product-capture must use compute-core protocol.Client instead of a duplicate local compute client")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat internal/plugin/client.go: %v", err)
	}
}
