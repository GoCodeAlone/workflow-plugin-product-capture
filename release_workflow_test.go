package productcapture_test

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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
	if strings.Contains(config, `go run ./cmd/release-prep --tag "{{ .Tag }}" --write`) {
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

func TestStagingProofWorkflowUsesBoundedControlClient(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/staging-proof.yml")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	for _, want := range []string{
		"github.ref == 'refs/heads/main'",
		"runs-on: ubuntu-latest",
		"environment: workflow-compute-staging",
		"timeout-minutes: 120",
		"cancel-in-progress: false",
		"go run ./cmd/product-capture-staging-proof",
		"WORKFLOW_COMPUTE_TASK_TOKEN: ${{ secrets.WORKFLOW_COMPUTE_TASK_TOKEN }}",
		"SERVER_URL: ${{ vars.WORKFLOW_COMPUTE_SERVER_URL }}",
		"--provider-image-ref \"$PROVIDER_IMAGE_REF\"",
		"--worker-id \"$WORKER_ID\"",
		"--artifact-timeout 5m",
		"product-capture-staging-proof.json",
		"product-capture-staging-proof.log",
		"head -c 65536",
	} {
		if !strings.Contains(workflow, want) {
			t.Fatalf("staging proof workflow missing %q", want)
		}
	}
	for _, forbidden := range []string{
		"docker save", "podman save", "oci.tar", "agent-artifacts", "provider-package", "campaign",
		"inputs.server_url",
		`--server "${{ inputs.server_url }}"`,
		`--provider-image-ref "${{ inputs.provider_image_ref }}"`,
		`--worker-id "${{ inputs.worker_id }}"`,
		`--product-url "${{ inputs.product_url }}"`,
	} {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("staging proof workflow contains forbidden transfer path %q", forbidden)
		}
	}
	assertWorkflowUsesPinnedActions(t, ".github/workflows/staging-proof.yml", workflow)
}

func TestStagingProofDocsRequireMinimumScopedCredential(t *testing.T) {
	data, err := os.ReadFile("docs/buymywishlist-live-usage.md")
	if err != nil {
		t.Fatal(err)
	}
	docs := string(data)
	for _, scope := range []string{"`agent:read`", "`task:read`", "`task:write`"} {
		if !strings.Contains(docs, scope) {
			t.Fatalf("staging proof docs missing required credential scope %s", scope)
		}
	}
	if !strings.Contains(docs, "PRODUCT_CAPTURE_BROWSER_DIAGNOSTIC_ALLOWED_ORIGINS=https://<diagnostic-host>") {
		t.Fatal("staging proof docs omit the required browser diagnostic origin allowlist")
	}
}

func TestBuyMyWishlistDocsUseVerifiedHTMLBound(t *testing.T) {
	const verifiedMaxHTMLBytes = 10 << 20

	data, err := os.ReadFile("docs/buymywishlist-live-usage.md")
	if err != nil {
		t.Fatal(err)
	}
	docs := strings.ReplaceAll(string(data), "\r\n", "\n")
	_, workflowSection, found := strings.Cut(docs, "## Workflow Step")
	if !found {
		t.Fatal("BuyMyWishlist live-usage docs missing Workflow Step section")
	}
	_, workflowYAML, found := strings.Cut(workflowSection, "```yaml\n")
	if !found {
		t.Fatal("BuyMyWishlist Workflow Step section missing YAML block")
	}
	workflowYAML, _, found = strings.Cut(workflowYAML, "\n```")
	if !found {
		t.Fatal("BuyMyWishlist Workflow Step YAML block is not closed")
	}
	var workflow struct {
		Steps []struct {
			Type   string `yaml:"type"`
			Config struct {
				MaxHTMLBytes int64 `yaml:"max_html_bytes"`
			} `yaml:"config"`
		} `yaml:"steps"`
	}
	if err := yaml.Unmarshal([]byte(workflowYAML), &workflow); err != nil {
		t.Fatalf("parse BuyMyWishlist Workflow Step YAML: %v", err)
	}
	var captureStepCount int
	var documentedMaxHTMLBytes int64
	for _, step := range workflow.Steps {
		if step.Type == "step.product_capture" {
			captureStepCount++
			documentedMaxHTMLBytes = step.Config.MaxHTMLBytes
		}
	}
	if captureStepCount != 1 {
		t.Fatalf("BuyMyWishlist product-capture step count = %d, want 1", captureStepCount)
	}
	if documentedMaxHTMLBytes != verifiedMaxHTMLBytes {
		t.Fatalf("BuyMyWishlist max_html_bytes = %d, want %d", documentedMaxHTMLBytes, verifiedMaxHTMLBytes)
	}

	schemaData, err := os.ReadFile("schemas/product-capture-operation-input.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]struct {
			Maximum int64 `json:"maximum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schemaData, &schema); err != nil {
		t.Fatalf("parse product-capture operation schema: %v", err)
	}
	if got := schema.Properties["max_html_bytes"].Maximum; got != verifiedMaxHTMLBytes {
		t.Fatalf("max_html_bytes schema maximum = %d, want %d", got, verifiedMaxHTMLBytes)
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
		"xvfb",
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

func TestReleaseWorkflowRetainsFailedConformanceReport(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	runtimeStart := strings.Index(workflow, "  runtime-image:")
	publishStart := strings.Index(workflow, "  publish-release:")
	if runtimeStart < 0 || publishStart <= runtimeStart {
		t.Fatal("release workflow runtime-image job boundaries are missing")
	}
	runtimeJob := workflow[runtimeStart:publishStart]
	uploadStart := strings.Index(runtimeJob, "name: Upload redacted conformance report")
	pushStart := strings.Index(runtimeJob, "name: Push exact tested browser image")
	if uploadStart < 0 || pushStart <= uploadStart {
		t.Fatal("failed conformance report upload must run before promotion in runtime-image")
	}
	uploadStep := runtimeJob[uploadStart:pushStart]
	for _, want := range []string{
		"always() && hashFiles('conformance.json') != ''",
		"actions/upload-artifact@b7c566a772e6b6bfb58ed0dc250532a479d7789f",
		"name: browser-runtime-conformance-${{ github.run_id }}-${{ github.run_attempt }}",
		"path: conformance.json",
		"if-no-files-found: error",
	} {
		if !strings.Contains(uploadStep, want) {
			t.Errorf("release workflow missing failed-conformance evidence %q", want)
		}
	}
	conformanceIndex := strings.Index(runtimeJob, "name: Run exact candidate browser conformance")
	if conformanceIndex < 0 || uploadStart < conformanceIndex {
		t.Fatal("failed conformance report upload must run after conformance and before promotion")
	}
}

func TestReleaseWorkflowRequiresManagedProfileCrashRecoveryBeforePromotion(t *testing.T) {
	conformanceRun := releaseRuntimeStepRun(t, "Run exact candidate browser conformance")
	for _, required := range []string{
		`.profile_persistence.startup_crash_injected == true`,
		`.profile_persistence.seed_cookie_observed == true`,
		`.profile_persistence.crash_injected == true`,
		`.profile_persistence.recovered_after_crash == true`,
		`.profile_persistence.cookie_persisted == true`,
	} {
		if !strings.Contains(conformanceRun, required) {
			t.Errorf("release conformance step must require %q", required)
		}
	}
}

func TestReleaseWorkflowBindsPromotedDigestToTestedLocalImage(t *testing.T) {
	promotionRun := releaseRuntimeStepRun(t, "Push exact tested browser image")
	for _, required := range []string{
		`docker image inspect --format '{{.Id}}' "$CANDIDATE"`,
		`docker image inspect --format '{{range .RepoDigests}}{{println .}}{{end}}' "$CANDIDATE"`,
		`mapfile -t pushed_refs`,
		`provider_image_ref="${pushed_refs[0]}"`,
		`digest="${provider_image_ref#*@}"`,
		`docker buildx imagetools inspect "${repository}@${digest}"`,
		`if [ "$remote_digest" != "$digest" ]`,
	} {
		if !strings.Contains(promotionRun, required) {
			t.Errorf("release promotion step missing tested-image digest binding %q", required)
		}
	}
	if strings.Contains(promotionRun, `imagetools inspect "$CANDIDATE"`) {
		t.Fatal("release promotion resolves a mutable tag after conformance")
	}
}

func releaseRuntimeStepRun(t *testing.T, stepName string) string {
	t.Helper()
	data, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatalf("release workflow is not valid YAML: %v", err)
	}
	for _, step := range workflow.Jobs["runtime-image"].Steps {
		if step.Name == stepName {
			if strings.TrimSpace(step.Run) == "" {
				t.Fatalf("release runtime step %q has no executable run body", stepName)
			}
			return step.Run
		}
	}
	t.Fatalf("release runtime step %q is missing", stepName)
	return ""
}

func TestReleaseWorkflowVerifiesNativeAMD64RunnerBeforeConformance(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	var parsed yaml.Node
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("release workflow is not valid YAML: %v", err)
	}
	workflow := string(data)
	runtimeStart := strings.Index(workflow, "  runtime-image:")
	publishStart := strings.Index(workflow, "  publish-release:")
	if runtimeStart < 0 || publishStart <= runtimeStart {
		t.Fatal("release workflow runtime-image job boundaries are missing")
	}
	runtimeJob := workflow[runtimeStart:publishStart]
	verifyStart := strings.Index(runtimeJob, "name: Verify native amd64 release runner")
	conformanceStart := strings.Index(runtimeJob, "name: Run exact candidate browser conformance")
	if verifyStart < 0 || conformanceStart <= verifyStart {
		t.Fatal("native amd64 runner verification must run before exact candidate conformance")
	}
	verifyStep := runtimeJob[verifyStart:conformanceStart]
	for _, required := range []string{
		"set -euo pipefail",
		`test "$(uname -m)" = "x86_64"`,
		"test ! -e /run/rosetta/rosetta",
	} {
		if !strings.Contains(verifyStep, required) {
			t.Errorf("native amd64 runner verification missing %q", required)
		}
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
