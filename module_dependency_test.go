package productcapture

import (
	"os"
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
	for _, path := range []string{
		"internal/plugin/sign.go",
		"internal/plugin/step.go",
		"internal/plugin/plugin_test.go",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(data), "github.com/GoCodeAlone/workflow-compute/pkg/protocol") {
			t.Fatalf("%s imports private workflow-compute protocol package", path)
		}
	}
	if _, err := os.Stat("internal/plugin/client.go"); err == nil {
		t.Fatal("product-capture must use compute-core protocol.Client instead of a duplicate local compute client")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat internal/plugin/client.go: %v", err)
	}
}
