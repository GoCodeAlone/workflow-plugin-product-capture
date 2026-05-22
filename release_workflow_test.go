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
	if !strings.Contains(workflow, "docker/product-capture-browser/Dockerfile") ||
		!strings.Contains(workflow, "ghcr.io/gocodealone/workflow-plugin-product-capture/product-capture-browser:${{ github.ref_name }}") ||
		!strings.Contains(workflow, "steps.build.outputs.digest") {
		t.Fatal("release workflow must publish the product-capture browser runtime image and report its digest")
	}
	if !strings.Contains(workflow, "needs: [release, runtime-image]") {
		t.Fatal("registry notification must wait for the runtime image publish")
	}
}

func TestRuntimeImageInstallsChromeAndPlaywrightWithoutBundledBrowser(t *testing.T) {
	data, err := os.ReadFile("docker/product-capture-browser/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(data)
	for _, want := range []string{
		"google-chrome-stable",
		"npm install -g playwright@",
		"PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1",
	} {
		if !strings.Contains(dockerfile, want) {
			t.Fatalf("runtime image Dockerfile missing %q", want)
		}
	}
}
