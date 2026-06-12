package productcapture_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

var pinnedActionRef = regexp.MustCompile(`^(-\s*)?uses:\s+\S+@[0-9a-f]{40}(\s+#\s+\S+)?$`)

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
	if strings.Contains(workflow, "go run ./cmd/release-prep --tag \"${{ github.ref_name }}\" --write") {
		t.Fatal("release workflow must not dirty tracked plugin.json before GoReleaser starts")
	}
	if !strings.Contains(workflow, "WFCTL_BIN: ${{ runner.temp }}/wfctl-bin/wfctl") {
		t.Fatal("release workflow must pass wfctl path into GoReleaser hooks")
	}
	assertWorkflowUsesPinnedActions(t, ".github/workflows/release.yml", workflow)
}

func TestGoReleaserPreparesReleaseManifestInsideLifecycle(t *testing.T) {
	data, err := os.ReadFile(".goreleaser.yml")
	if err != nil {
		t.Fatal(err)
	}
	config := string(data)
	for _, want := range []string{
		`go run ./cmd/release-prep --tag "{{ .Tag }}"`,
		`"{{ .Env.WFCTL_BIN }} plugin validate-contract --for-publish --tag {{ .Tag }} ."`,
	} {
		if !strings.Contains(config, want) {
			t.Fatalf(".goreleaser.yml missing release hook %q", want)
		}
	}
	if strings.Contains(config, "--write") {
		t.Fatal(".goreleaser.yml must check committed release metadata instead of rewriting plugin.json during release")
	}
}

func TestCIWorkflowChecksReleaseManifest(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	if !strings.Contains(workflow, "go run ./cmd/release-prep") {
		t.Fatal("CI workflow must check plugin.json release metadata consistency")
	}
	assertWorkflowUsesPinnedActions(t, ".github/workflows/ci.yml", workflow)
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

func TestRuntimeImageRunsProductCaptureProviderEntrypoint(t *testing.T) {
	data, err := os.ReadFile("docker/product-capture-browser/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(data)
	for _, want := range []string{
		"COPY docker/product-capture-browser/product-capture-provider /usr/local/bin/product-capture-provider",
		"ENTRYPOINT [\"/usr/local/bin/product-capture-provider\"]",
	} {
		if !strings.Contains(dockerfile, want) {
			t.Fatalf("runtime image Dockerfile missing %q", want)
		}
	}
}

func TestReleaseWorkflowBuildsRuntimeProviderBinaryBeforeImage(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	for _, want := range []string{
		"name: Configure private Go modules for runtime image",
		"name: Build product capture provider binary",
		"CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o docker/product-capture-browser/product-capture-provider ./cmd/product-capture-provider",
	} {
		if !strings.Contains(workflow, want) {
			t.Fatalf("release workflow missing %q", want)
		}
	}
	buildProviderIndex := strings.Index(workflow, "name: Build product capture provider binary")
	buildImageIndex := strings.Index(workflow, "name: Build and push product capture browser image")
	if buildProviderIndex < 0 || buildImageIndex < 0 || buildProviderIndex > buildImageIndex {
		t.Fatal("release workflow must build the provider binary before building the runtime image")
	}
}

func assertWorkflowUsesPinnedActions(t *testing.T, path, workflow string) {
	t.Helper()
	for _, line := range strings.Split(workflow, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "- uses:") && !strings.HasPrefix(trimmed, "uses:") {
			continue
		}
		if !pinnedActionRef.MatchString(trimmed) {
			t.Fatalf("%s action reference must be pinned to a commit SHA: %s", path, trimmed)
		}
	}
}
