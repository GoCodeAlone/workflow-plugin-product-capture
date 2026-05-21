package productcapture_test

import (
	"os"
	"strings"
	"testing"
)

func TestReleaseWorkflowUsesGlobalDispatchToken(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	if !strings.Contains(workflow, "token: ${{ secrets.REPO_DISPATCH_TOKEN }}") {
		t.Fatal("release workflow must use globally configured REPO_DISPATCH_TOKEN for repository_dispatch")
	}
	if strings.Contains(workflow, "REGISTRY_PAT") {
		t.Fatal("release workflow must not reference stale REGISTRY_PAT secret")
	}
}
