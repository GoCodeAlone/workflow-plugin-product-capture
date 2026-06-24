package provider

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	coreprotocol "github.com/GoCodeAlone/workflow-plugin-compute-core/protocol"
	"github.com/GoCodeAlone/workflow-plugin-product-capture/internal/snapshot"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestProviderContractAlignsWithWorkflowComputeGenericProviderABI(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "product-capture-provider.json"))
	if err != nil {
		t.Fatalf("read contract: %v", err)
	}

	var contract struct {
		ProtocolVersion        string   `json:"protocol_version"`
		ID                     string   `json:"id"`
		PluginID               string   `json:"plugin_id"`
		ProviderID             string   `json:"provider_id"`
		ContractID             string   `json:"contract_id"`
		Version                string   `json:"version"`
		DisplayName            string   `json:"display_name"`
		ConfigSchemaRef        string   `json:"config_schema_ref"`
		ConfigSchemaDigest     string   `json:"config_schema_digest"`
		OperatingModes         []string `json:"operating_modes"`
		WorkloadKinds          []string `json:"workload_kinds"`
		ExecutorProviders      []string `json:"executor_providers"`
		ExecutionSecurityTiers []string `json:"execution_security_tiers"`
		ProofTiers             []string `json:"proof_tiers"`
		NetworkModes           []string `json:"network_modes"`
		Operations             []struct {
			ID                 string   `json:"id"`
			InputSchemaRef     string   `json:"input_schema_ref"`
			InputSchemaDigest  string   `json:"input_schema_digest"`
			OutputSchemaRef    string   `json:"output_schema_ref"`
			OutputSchemaDigest string   `json:"output_schema_digest"`
			Artifacts          []string `json:"artifacts"`
		} `json:"operations"`
		RuntimeContract json.RawMessage `json:"runtime_contract"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&contract); err != nil {
		t.Fatalf("decode contract: %v", err)
	}

	if contract.ProtocolVersion != "compute.v1alpha1" {
		t.Fatalf("protocol_version = %q", contract.ProtocolVersion)
	}
	if contract.PluginID != ProviderName || contract.ProviderID != "browser" || contract.ContractID != "product-capture.browser.v1" || contract.Version != "v1.0.0" {
		t.Fatalf("provider identity drifted: %+v", contract)
	}
	if !containsString(contract.WorkloadKinds, WorkloadKind) {
		t.Fatalf("workload_kinds = %v, want %q", contract.WorkloadKinds, WorkloadKind)
	}
	if !containsString(contract.ExecutorProviders, ExecutorProvider) {
		t.Fatalf("executor_providers = %v, want %q", contract.ExecutorProviders, ExecutorProvider)
	}
	var capture *struct {
		ID                 string   `json:"id"`
		InputSchemaRef     string   `json:"input_schema_ref"`
		InputSchemaDigest  string   `json:"input_schema_digest"`
		OutputSchemaRef    string   `json:"output_schema_ref"`
		OutputSchemaDigest string   `json:"output_schema_digest"`
		Artifacts          []string `json:"artifacts"`
	}
	for i := range contract.Operations {
		if contract.Operations[i].ID == CaptureOperation {
			capture = &contract.Operations[i]
			break
		}
	}
	if capture == nil {
		t.Fatalf("missing %q operation in %+v", CaptureOperation, contract.Operations)
	}
	if capture.InputSchemaRef == "" || capture.OutputSchemaRef == "" {
		t.Fatalf("operation schema refs missing: %+v", *capture)
	}
	schemaData, err := os.ReadFile(filepath.Join("..", "..", "schemas", "product-capture-provider.schema.json"))
	if err != nil {
		t.Fatalf("read provider schema: %v", err)
	}
	sum := sha256.Sum256(schemaData)
	if want := "sha256:" + hex.EncodeToString(sum[:]); contract.ConfigSchemaDigest != want {
		t.Fatalf("config schema digest = %q, want %q", contract.ConfigSchemaDigest, want)
	}
	if !strings.HasPrefix(capture.InputSchemaDigest, "sha256:") || !strings.HasPrefix(capture.OutputSchemaDigest, "sha256:") {
		t.Fatalf("operation schema digests missing: %+v", *capture)
	}
	if !containsString(capture.Artifacts, ProductJSONArtifact) {
		t.Fatalf("operation artifacts = %v, want %q", capture.Artifacts, ProductJSONArtifact)
	}
}

func TestPluginManifestsExposeProviderContract(t *testing.T) {
	for _, path := range []string{"plugin.json", "plugin.contracts.json"} {
		data, err := os.ReadFile(filepath.Join("..", "..", path))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var manifest struct {
			Contracts []struct {
				ID     string `json:"id"`
				Path   string `json:"path"`
				Schema string `json:"schema"`
			} `json:"contracts"`
		}
		if path == "plugin.contracts.json" {
			var contracts []struct {
				ID     string `json:"id"`
				Path   string `json:"path"`
				Schema string `json:"schema"`
			}
			dec := json.NewDecoder(bytes.NewReader(data))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&contracts); err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
			manifest.Contracts = contracts
		} else {
			if err := json.Unmarshal(data, &manifest); err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
		}
		found := false
		for _, contract := range manifest.Contracts {
			if contract.ID == "product-capture.browser.v1" &&
				contract.Path == "contracts/product-capture-provider.json" &&
				contract.Schema == "schemas/product-capture-provider.schema.json" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s does not expose product-capture provider contract: %+v", path, manifest.Contracts)
		}
	}
}

func TestWriteProbeReportsComputeCapabilities(t *testing.T) {
	var out bytes.Buffer
	if err := WriteProbe(&out); err != nil {
		t.Fatalf("probe: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode probe: %v", err)
	}
	if got["executor_provider"] != ExecutorProvider {
		t.Fatalf("executor_provider: %v", got["executor_provider"])
	}
	if got["workload_kind"] != WorkloadKind {
		t.Fatalf("workload_kind: %v", got["workload_kind"])
	}
	for _, tool := range got["runtime_tools"].([]any) {
		if tool == "playwright" {
			t.Fatalf("probe must not advertise playwright runtime: %v", got["runtime_tools"])
		}
	}
}

func TestMainRejectsUnknownRequestFields(t *testing.T) {
	dir := t.TempDir()
	req := filepath.Join(dir, "request.json")
	out := filepath.Join(dir, "snapshot.json")
	if err := os.WriteFile(req, []byte(`{"workload":{"url":"https://www.amazon.com/dp/B08H75RTZ8","allowed_hosts":["www.amazon.com"]},"surprise":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Main([]string{"--request", req, "--output", out}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected failure, stdout=%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "unknown field") {
		t.Fatalf("stderr missing strict decode error: %s", stderr.String())
	}
}

func TestMainRejectsUnsupportedHosts(t *testing.T) {
	dir := t.TempDir()
	req := filepath.Join(dir, "request.json")
	out := filepath.Join(dir, "snapshot.json")
	if err := os.WriteFile(req, []byte(`{"workload":{"url":"https://example.com/product","allowed_hosts":["example.com"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Main([]string{"--request", req, "--output", out}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected failure, stdout=%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "unsupported host") {
		t.Fatalf("stderr missing host error: %s", stderr.String())
	}
}

func TestMainCapturesAmazonFixture(t *testing.T) {
	dir := t.TempDir()
	req := filepath.Join(dir, "request.json")
	out := filepath.Join(dir, "snapshot.json")
	fixture := filepath.Join("..", "snapshot", "testdata", "amazon_xbox.html")
	t.Setenv("PRODUCT_CAPTURE_HTML_FIXTURE", fixture)
	if err := os.WriteFile(req, []byte(`{"workload":{"url":"https://www.amazon.com/Microsoft-Xbox-Gaming-Console-video-game/dp/B08H75RTZ8","allowed_hosts":["www.amazon.com"],"capture_mode":"browser","timeout_seconds":30,"max_html_bytes":1048576,"max_image_count":4}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Main([]string{"--request", req, "--output", out}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("capture failed: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var got struct {
		Provider                 string   `json:"provider"`
		RequestedURL             string   `json:"requested_url"`
		ExternalID               string   `json:"external_id"`
		CanonicalURL             string   `json:"canonical_url"`
		Title                    string   `json:"title"`
		Price                    string   `json:"price"`
		ImageURL                 string   `json:"image_url"`
		Images                   []string `json:"images,omitempty"`
		VariantKey               string   `json:"variant_key"`
		RequiresUserConfirmation bool     `json:"requires_user_confirmation"`
		RawHTML                  string   `json:"raw_html,omitempty"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if got.Provider != "browser_capture" {
		t.Fatalf("provider: %q", got.Provider)
	}
	if !strings.Contains(got.Title, "Xbox Series X") {
		t.Fatalf("title: %q", got.Title)
	}
	if got.Price != "637.00" {
		t.Fatalf("price: %q", got.Price)
	}
	if got.RequestedURL == "" || got.ExternalID != "B08H75RTZ8" || got.CanonicalURL == "" {
		t.Fatalf("product identity fields missing: %+v", got)
	}
	if got.ImageURL == "" || got.VariantKey == "" || !got.RequiresUserConfirmation {
		t.Fatalf("variant/image fields missing: %+v", got)
	}
	if len(got.Images) > 4 {
		t.Fatalf("max_image_count ignored: %d", len(got.Images))
	}
	if got.RawHTML != "" {
		t.Fatalf("raw html leaked")
	}
}

func TestMainRunsBrowserDiagnosticWithFakePlaywright(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not installed; CI provisions Node for generated Playwright script regressions: %v", err)
	}
	dir := t.TempDir()
	moduleDir := filepath.Join(dir, "node_modules", "playwright")
	if err := os.MkdirAll(moduleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "index.js"), []byte(`
global.navigator = {};
Object.defineProperty(global.navigator, 'webdriver', { configurable: true, get: () => true });
Object.defineProperty(global.navigator, 'userAgent', { configurable: true, get: () => 'Fake Chrome' });
Object.defineProperty(global.navigator, 'language', { configurable: true, get: () => 'en-US' });
Object.defineProperty(global.navigator, 'languages', { configurable: true, get: () => ['en-US', 'en'] });
Object.defineProperty(global.navigator, 'platform', { configurable: true, get: () => 'Linux x86_64' });
Object.defineProperty(global.navigator, 'hardwareConcurrency', { configurable: true, get: () => 8 });
Object.defineProperty(global.navigator, 'deviceMemory', { configurable: true, get: () => 8 });
Object.defineProperty(global.navigator, 'maxTouchPoints', { configurable: true, get: () => 0 });
Object.defineProperty(global.navigator, 'plugins', { configurable: true, get: () => [{ name: 'PDF Viewer' }] });
Object.defineProperty(global.navigator, 'mimeTypes', { configurable: true, get: () => [{ type: 'application/pdf' }] });
global.screen = { width: 1440, height: 900, availWidth: 1440, availHeight: 855, colorDepth: 24, pixelDepth: 24 };
global.window = {
  outerWidth: 1440,
  outerHeight: 900,
  innerWidth: 1280,
  innerHeight: 720,
  devicePixelRatio: 1,
  matchMedia: (query) => ({ matches: query.includes('prefers-color-scheme') }),
  chrome: { runtime: {} },
};
global.document = {
  visibilityState: 'visible',
  hasFocus: () => true,
  get cookie() { return 'redacted=value'; },
};
global.location = { href: 'https://diag.example.test/capture' };
global.Intl = {
  DateTimeFormat: () => ({ resolvedOptions: () => ({ timeZone: 'UTC' }) }),
};
global.fetch = async () => ({ ok: true, status: 204 });
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async (fn, arg) => { fn(arg); },
      goto: async (url) => { global.location.href = url; return { status: () => 200 }; },
      url: () => global.location.href,
      evaluate: async (fn) => await fn(),
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError: class TimeoutError extends Error {} };
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_PATH", filepath.Join(dir, "node_modules"))

	var stdout, stderr bytes.Buffer
	code := Main([]string{"--browser-diagnostic-url", "https://diag.example.test/capture"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("diagnostic failed with code %d stderr=%s", code, stderr.String())
	}

	var got struct {
		TargetURL      string `json:"target_url"`
		FinalURL       string `json:"final_url"`
		PostedToOrigin bool   `json:"posted_to_origin"`
		BrowserSignals struct {
			Navigator struct {
				Webdriver any    `json:"webdriver"`
				UserAgent string `json:"user_agent"`
			} `json:"navigator"`
			Document struct {
				CookiePresent bool `json:"cookie_present"`
				CookieLength  int  `json:"cookie_length"`
			} `json:"document"`
		} `json:"browser_signals"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode diagnostic output: %v\nstdout=%s", err, stdout.String())
	}
	if got.TargetURL != "https://diag.example.test/capture" || got.FinalURL != got.TargetURL {
		t.Fatalf("unexpected diagnostic URLs: %+v", got)
	}
	if got.BrowserSignals.Navigator.Webdriver != nil {
		t.Fatalf("diagnostic did not apply webdriver guard: %#v", got.BrowserSignals.Navigator.Webdriver)
	}
	if got.BrowserSignals.Navigator.UserAgent != "Fake Chrome" {
		t.Fatalf("user agent = %q", got.BrowserSignals.Navigator.UserAgent)
	}
	if !got.BrowserSignals.Document.CookiePresent || got.BrowserSignals.Document.CookieLength == 0 {
		t.Fatalf("diagnostic should report cookie presence without values: %+v", got.BrowserSignals.Document)
	}
	if !got.PostedToOrigin {
		t.Fatalf("diagnostic should post browser signals back to the controlled origin: %+v", got)
	}
}

func TestBrowserDiagnosticSkipsPostAfterCrossOriginRedirect(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not installed; CI provisions Node for generated Playwright script regressions: %v", err)
	}
	dir := t.TempDir()
	moduleDir := filepath.Join(dir, "node_modules", "playwright")
	if err := os.MkdirAll(moduleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "index.js"), []byte(`
global.navigator = {};
Object.defineProperty(global.navigator, 'webdriver', { configurable: true, get: () => true });
Object.defineProperty(global.navigator, 'userAgent', { configurable: true, get: () => 'Fake Chrome' });
global.window = { matchMedia: () => ({ matches: false }) };
global.screen = {};
global.document = {
  visibilityState: 'visible',
  hasFocus: () => true,
  createElement: () => ({ getContext: () => null }),
  get cookie() { return ''; },
};
global.location = { href: 'https://diag.example.test/capture' };
global.Intl = {
  DateTimeFormat: () => ({ resolvedOptions: () => ({ timeZone: 'UTC' }) }),
};
global.fetch = async () => { throw new Error('fetch should not run after a cross-origin redirect'); };
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async (fn, arg) => { fn(arg); },
      goto: async () => { global.location.href = 'https://unexpected.example.test/capture'; return { status: () => 302 }; },
      url: () => global.location.href,
      evaluate: async (fn) => await fn(),
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError: class TimeoutError extends Error {} };
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_PATH", filepath.Join(dir, "node_modules"))

	var stdout, stderr bytes.Buffer
	code := Main([]string{"--browser-diagnostic-url", "https://diag.example.test/capture"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("diagnostic failed with code %d stderr=%s", code, stderr.String())
	}

	var got struct {
		TargetURL      string `json:"target_url"`
		FinalURL       string `json:"final_url"`
		PostedToOrigin bool   `json:"posted_to_origin"`
		PostError      string `json:"post_error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode diagnostic output: %v\nstdout=%s", err, stdout.String())
	}
	if got.FinalURL != "https://unexpected.example.test/capture" {
		t.Fatalf("final_url = %q", got.FinalURL)
	}
	if got.PostedToOrigin {
		t.Fatalf("diagnostic posted after cross-origin redirect: %+v", got)
	}
	if !strings.Contains(got.PostError, "final origin") {
		t.Fatalf("post_error should explain skipped cross-origin post: %+v", got)
	}
}

func TestBrowserDiagnosticScriptSharesCaptureBrowserIdentity(t *testing.T) {
	if !strings.Contains(playwrightBrowserDiagnosticScript, "launchChromeBrowser") {
		t.Fatalf("diagnostic script must use the shared browser launcher")
	}
	for _, required := range []string{
		"channel: 'chrome'",
		"headless: true",
		"--disable-blink-features=AutomationControlled",
		"chromeUserAgent",
		"Network.setUserAgentOverride",
		"navigator, 'webdriver'",
	} {
		if !strings.Contains(playwrightBrowserDiagnosticScript, required) {
			t.Fatalf("diagnostic script missing capture browser identity behavior %q", required)
		}
	}
}

func TestPlaywrightBrowserIdentityAvoidsMixedChromeVersionSignals(t *testing.T) {
	if strings.Contains(playwrightBrowserPrelude, "Chrome/124.0.0.0") {
		t.Fatalf("browser identity must not pin a stale Chrome version")
	}
	for _, required := range []string{
		"browser.version()",
		"normalizeChromeVersion",
		"Network.setUserAgentOverride",
		"userAgentMetadata",
		"fullVersionList",
	} {
		if !strings.Contains(playwrightBrowserPrelude, required) {
			t.Fatalf("browser identity must align user agent and client hints; missing %q", required)
		}
	}
}

func TestPlaywrightBrowserIdentityAvoidsMixedPlatformSignals(t *testing.T) {
	for _, required := range []string{
		"navigatorPlatform: 'MacIntel'",
		"userAgentDataPlatform: 'macOS'",
		"platform: productCaptureBrowserIdentity.navigatorPlatform",
		"Object.defineProperty(navigator, 'platform'",
		"Object.defineProperty(navigator, 'userAgentData'",
		"getHighEntropyValues",
	} {
		if !strings.Contains(playwrightBrowserPrelude, required) {
			t.Fatalf("browser identity must align JS platform signals; missing %q", required)
		}
	}
}

func TestPlaywrightBrowserIdentityAvoidsMalformedLanguageSignals(t *testing.T) {
	for _, disallowed := range []string{
		"acceptLanguage: 'en-US,en;q=0.9'",
		"'Accept-Language': 'en-US,en;q=0.9'",
		"extraHTTPHeaders",
		"en;q=0.9']",
	} {
		if strings.Contains(playwrightBrowserPrelude, disallowed) {
			t.Fatalf("browser identity must not create malformed language signals; found %q", disallowed)
		}
	}
	for _, required := range []string{
		"acceptLanguage: productCaptureBrowserIdentity.languages.join(',')",
		"Object.defineProperty(navigator, 'language'",
		"Object.defineProperty(navigator, 'languages'",
		"Object.freeze([...identity.languages])",
		"languages: ['en-US', 'en']",
		"locale: productCaptureBrowserIdentity.language",
	} {
		if !strings.Contains(playwrightBrowserPrelude, required) {
			t.Fatalf("browser identity must align language signals; missing %q", required)
		}
	}
}

func TestMainRunsWorkflowComputeDynamicProviderEnvelope(t *testing.T) {
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(wd, "..", "snapshot", "testdata", "amazon_xbox.html")
	t.Setenv("PRODUCT_CAPTURE_HTML_FIXTURE", fixture)
	t.Chdir(dir)
	input := `{
	  "protocol_version":"compute.v1alpha1",
	  "task_id":"task-123",
	  "lease_id":"lease-123",
		  "provider_config":{
		    "plugin_id":"workflow-plugin-product-capture",
		    "provider_id":"browser",
		    "contract_id":"product-capture.browser.v1",
		    "version":"v1.0.0",
		    "config_ref":"config://network-products/product-capture/browser",
		    "config_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		  },
	  "operation":"capture_product",
	  "input":{
	    "url":"https://www.amazon.com/Microsoft-Xbox-Gaming-Console-video-game/dp/B08H75RTZ8",
	    "allowed_hosts":["www.amazon.com"],
	    "capture_mode":"browser",
	    "timeout_seconds":30,
	    "max_html_bytes":1048576,
	    "max_image_count":2
	  }
	}`

	var stdout, stderr bytes.Buffer
	code := Main(nil, &stdout, &stderr, strings.NewReader(input))
	if code != 0 {
		t.Fatalf("capture failed: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	var result struct {
		Artifacts []string `json:"artifacts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode provider result: %v\n%s", err, stdout.String())
	}
	if !containsString(result.Artifacts, ProductJSONArtifact) {
		t.Fatalf("artifacts = %v, want %q", result.Artifacts, ProductJSONArtifact)
	}
	data, err := os.ReadFile(ProductJSONArtifact)
	if err != nil {
		t.Fatalf("read product artifact: %v", err)
	}
	assertFileMode(t, ProductJSONArtifact, 0o644)
	var got struct {
		Title                    string   `json:"title"`
		RequestedURL             string   `json:"requested_url"`
		VariantKey               string   `json:"variant_key"`
		RequiresUserConfirmation bool     `json:"requires_user_confirmation"`
		Images                   []string `json:"images,omitempty"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode product artifact: %v", err)
	}
	if !strings.Contains(got.Title, "Xbox Series X") {
		t.Fatalf("title: %q", got.Title)
	}
	if got.RequestedURL == "" || got.VariantKey == "" || !got.RequiresUserConfirmation {
		t.Fatalf("variant fields missing from product artifact: %+v", got)
	}
	if len(got.Images) > 2 {
		t.Fatalf("max_image_count ignored: %d", len(got.Images))
	}
}

func TestMainRunsWorkflowComputeProviderEnvelopeWithRuntimeMetadata(t *testing.T) {
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(wd, "..", "snapshot", "testdata", "amazon_xbox.html")
	t.Setenv("PRODUCT_CAPTURE_HTML_FIXTURE", fixture)
	t.Chdir(dir)
	envelope := validWorkflowComputeProviderEnvelope(t)
	input := marshalNestedProviderEnvelopeFromValidatedRuntimeRequest(t, envelope)

	var stdout, stderr bytes.Buffer
	code := Main(nil, &stdout, &stderr, bytes.NewReader(input))
	if code != 0 {
		t.Fatalf("capture failed: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	var result struct {
		Artifacts []string `json:"artifacts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode provider result: %v\n%s", err, stdout.String())
	}
	if !containsString(result.Artifacts, ProductJSONArtifact) {
		t.Fatalf("artifacts = %v, want %q", result.Artifacts, ProductJSONArtifact)
	}
}

func TestMainRejectsInvalidWorkflowComputeRuntimeMetadata(t *testing.T) {
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(wd, "..", "snapshot", "testdata", "amazon_xbox.html")
	t.Setenv("PRODUCT_CAPTURE_HTML_FIXTURE", fixture)
	t.Chdir(dir)

	tests := []struct {
		name    string
		mutate  func(*dynamicEnvelope)
		wantErr string
	}{
		{
			name: "partial runtime profile",
			mutate: func(env *dynamicEnvelope) {
				env.RuntimeProfile = coreprotocol.ProviderRuntimeProfile{
					ExecutionSecurityTier: coreprotocol.ExecutionTrustedNative,
				}
			},
			wantErr: "runtime_profile",
		},
		{
			name: "backend without matching executor",
			mutate: func(env *dynamicEnvelope) {
				env.RuntimeBackend.Executors = nil
			},
			wantErr: "runtime_backend",
		},
		{
			name: "backend summary missing executor provider",
			mutate: func(env *dynamicEnvelope) {
				env.RuntimeBackend.ExecutorProviders = nil
			},
			wantErr: "runtime_backend",
		},
		{
			name: "backend executor below security floor",
			mutate: func(env *dynamicEnvelope) {
				env.Executor = coreprotocol.ExecutorRef{}
				env.RuntimeBackend.Executors[0].ExecutionSecurityTier = coreprotocol.ExecutionTrustedNative
			},
			wantErr: "runtime_backend",
		},
		{
			name: "backend executor version mismatch",
			mutate: func(env *dynamicEnvelope) {
				env.RuntimeBackend.Executors[0].Version = "v2"
			},
			wantErr: "runtime_backend",
		},
		{
			name: "backend missing selected runtime profile",
			mutate: func(env *dynamicEnvelope) {
				env.RuntimeBackend.RuntimeProfiles = []coreprotocol.RuntimeProfile{coreprotocol.RuntimeProfileContainerBuild}
			},
			wantErr: "runtime_backend",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := validWorkflowComputeProviderEnvelope(t)
			tc.mutate(&env)

			var stdout, stderr bytes.Buffer
			code := Main(nil, &stdout, &stderr, bytes.NewReader(marshalNestedProviderEnvelopeFromValidatedRuntimeRequest(t, env)))
			if code == 0 {
				t.Fatalf("expected failure, stdout=%s", stdout.String())
			}
			if !strings.Contains(stderr.String(), tc.wantErr) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.wantErr)
			}
		})
	}
}

func TestMainRejectsInvalidWorkflowComputeProviderConfig(t *testing.T) {
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(wd, "..", "snapshot", "testdata", "amazon_xbox.html")
	t.Setenv("PRODUCT_CAPTURE_HTML_FIXTURE", fixture)
	t.Chdir(dir)

	env := validWorkflowComputeProviderEnvelope(t)
	env.ProviderConfig.ConfigRef = "https://example.invalid/config.json"
	input, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal provider envelope: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := Main(nil, &stdout, &stderr, bytes.NewReader(input))
	if code == 0 {
		t.Fatalf("expected failure, stdout=%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "provider_config") {
		t.Fatalf("stderr = %q, want provider_config", stderr.String())
	}
}

func TestCaptureHTMLWithPlaywrightReportsOutputLimitBeforeNodePipeNoise(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake node executable uses a POSIX shell script")
	}
	dir := t.TempDir()
	node := filepath.Join(dir, "node")
	if err := os.WriteFile(node, []byte("#!/bin/sh\nprintf '0123456789abcdef'\nprintf 'write EPIPE\\n' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := captureHTMLWithPlaywright(Workload{
		URL:            "https://www.amazon.com/dp/B09B8V1LZ3",
		AllowedHosts:   []string{"www.amazon.com"},
		TimeoutSeconds: 1,
		MaxHTMLBytes:   8,
	})
	if err == nil {
		t.Fatalf("expected output limit error")
	}
	if !strings.Contains(err.Error(), "capture output exceeds max_html_bytes 8") {
		t.Fatalf("expected output limit error before pipe noise, got: %v", err)
	}
	if strings.Contains(err.Error(), "EPIPE") {
		t.Fatalf("error leaked node pipe noise: %v", err)
	}
}

func TestIsZeroMetadataHandlesNil(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("isZeroMetadata panicked for nil: %v", r)
		}
	}()
	if !isZeroMetadata(nil) {
		t.Fatal("nil metadata should be treated as zero")
	}
	var profile *coreprotocol.ProviderRuntimeProfile
	if !isZeroMetadata(profile) {
		t.Fatal("typed nil metadata should be treated as zero")
	}
}

func TestProviderSchemaAcceptsBuyMyWishlistLiveInputAndRejectsDemoFields(t *testing.T) {
	compiler := jsonschema.NewCompiler()
	schemaPath := filepath.Join("..", "..", "schemas", "product-capture-operation-input.schema.json")
	schema, err := compiler.Compile(schemaPath)
	if err != nil {
		t.Fatalf("compile input schema: %v", err)
	}

	liveInput := map[string]any{
		"url":             "https://www.amazon.com/Microsoft-Xbox-Gaming-Console-video-game/dp/B08H75RTZ8",
		"allowed_hosts":   []any{"www.amazon.com", "amazon.com"},
		"capture_mode":    "browser",
		"timeout_seconds": float64(60),
		"max_html_bytes":  float64(1048576),
		"max_image_count": float64(8),
		"metadata_only":   false,
	}
	if err := schema.Validate(liveInput); err != nil {
		t.Fatalf("BuyMyWishlist live input rejected: %v", err)
	}

	for _, tc := range []struct {
		name  string
		field string
		value any
	}{
		{name: "mock html", field: "mock_html", value: "<html></html>"},
		{name: "fixture path", field: "fixture_path", value: "internal/provider/testdata/demo.html"},
		{name: "demo product id", field: "demo_product_id", value: "demo-123"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			demoOnly := map[string]any{
				"url":           "https://www.amazon.com/dp/B08H75RTZ8",
				"allowed_hosts": []any{"www.amazon.com"},
				"capture_mode":  "browser",
				tc.field:        tc.value,
			}
			if err := schema.Validate(demoOnly); err == nil {
				t.Fatalf("schema accepted demo-only field %q", tc.field)
			}
		})
	}
}

func validWorkflowComputeProviderEnvelope(t *testing.T) dynamicEnvelope {
	t.Helper()
	input := json.RawMessage(`{
	  "url":"https://www.amazon.com/Microsoft-Xbox-Gaming-Console-video-game/dp/B08H75RTZ8",
	  "allowed_hosts":["www.amazon.com"],
	  "capture_mode":"browser",
	  "timeout_seconds":30,
	  "max_html_bytes":1048576,
	  "max_image_count":1
	}`)
	executor := coreprotocol.ExecutorRef{
		Provider:              ExecutorProvider,
		Version:               "v1",
		ExecutionSecurityTier: coreprotocol.ExecutionSandboxedContainer,
		ProofTier:             coreprotocol.ProofArtifactHash,
		ImageDigest:           "sha256:" + strings.Repeat("b", 64),
		RootFSDigest:          "sha256:" + strings.Repeat("c", 64),
	}
	return dynamicEnvelope{
		ProtocolVersion: ComputeProtocolVersion,
		TaskID:          "task-product-capture-live",
		LeaseID:         "lease-product-capture-live",
		WorkloadKind:    coreprotocol.WorkloadProvider,
		ProviderConfig: coreprotocol.ProviderConfig{
			PluginID:     ProviderName,
			ProviderID:   "browser",
			ContractID:   "product-capture.browser.v1",
			Version:      "v1.0.0",
			ConfigRef:    "config://network-products/product-capture/browser",
			ConfigDigest: "sha256:" + strings.Repeat("a", 64),
		},
		Operation: CaptureOperation,
		Input:     input,
		Executor:  executor,
		RuntimeProfile: coreprotocol.ProviderRuntimeProfile{
			ID:                     "product-capture-browser-sandboxed-container-artifact-hash-runtime",
			RuntimeProfile:         coreprotocol.RuntimeProfileSandboxedOCI,
			ExecutorProvider:       ExecutorProvider,
			ExecutionSecurityTier:  coreprotocol.ExecutionSandboxedContainer,
			ProofTier:              coreprotocol.ProofArtifactHash,
			AllowedRuntimeTools:    []coreprotocol.ContainerRuntimeTool{coreprotocol.ContainerRuntimePodman},
			ImageDigestRequired:    true,
			RootFSDigestRequired:   true,
			AllowedMountRefs:       []string{"workspace"},
			WritablePaths:          []string{"/tmp"},
			WritableRootFS:         coreprotocol.RuntimePermissionForbidden,
			Privileged:             coreprotocol.RuntimePermissionForbidden,
			HostNamespaces:         coreprotocol.RuntimePermissionForbidden,
			HostSocket:             coreprotocol.RuntimePermissionForbidden,
			SeccompDisable:         coreprotocol.RuntimePermissionForbidden,
			NoNewPrivilegesDisable: coreprotocol.RuntimePermissionForbidden,
			ConformanceProfiles:    []string{"sandboxed-oci-v1", "product-capture-v1"},
		},
		RuntimeBackend: coreprotocol.RuntimeBackendReport{
			ProtocolVersion:     ComputeProtocolVersion,
			BackendID:           "podman-rootless",
			Family:              coreprotocol.RuntimeBackendFamilyPodman,
			Tool:                coreprotocol.ContainerRuntimePodman,
			Version:             "5.0.0",
			OS:                  "linux",
			Arch:                "amd64",
			Status:              coreprotocol.RuntimeBackendSupported,
			IsolationMode:       coreprotocol.RuntimeIsolationUserNamespace,
			InstallBurden:       coreprotocol.RuntimeInstallSystemInstalled,
			RuntimeProfiles:     []coreprotocol.RuntimeProfile{coreprotocol.RuntimeProfileSandboxedOCI},
			ExecutorProviders:   []string{ExecutorProvider},
			Executors:           []coreprotocol.ExecutorRef{executor},
			ConformanceProfiles: []string{"sandboxed-oci-v1", "product-capture-v1"},
			Evidence: coreprotocol.RuntimeBackendEvidence{
				Digest:    "sha256:" + strings.Repeat("d", 64),
				Workspace: true,
				Network:   true,
				Env:       true,
				Proof:     true,
				Cleanup:   true,
			},
			GeneratedAt: time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC),
		},
		Env: map[string]string{"PRODUCT_CAPTURE_MODE": "browser"},
		Limits: coreprotocol.ResourceLimits{
			RuntimeSeconds: 60,
			OutputBytes:    10 << 20,
		},
	}
}

func marshalNestedProviderEnvelopeFromValidatedRuntimeRequest(t *testing.T, env dynamicEnvelope) []byte {
	t.Helper()
	input, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal provider envelope: %v", err)
	}
	req := coreprotocol.RuntimeExecutionRequest{
		ProtocolVersion: ComputeProtocolVersion,
		TaskID:          env.TaskID,
		LeaseID:         env.LeaseID,
		WorkloadKind:    coreprotocol.WorkloadProvider,
		ProviderConfig:  env.ProviderConfig,
		Operation:       "run-dynamic-provider",
		Input:           input,
		Env:             map[string]string{"WF_PROVIDER_ENVELOPE": "compute-core"},
		Limits:          env.Limits,
	}
	if err := req.Validate(); err != nil {
		t.Fatalf("runtime execution request should be compute-core valid: %v", err)
	}
	return req.Input
}

func TestMainRejectsUnknownDynamicEnvelopeFields(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Main(nil, &stdout, &stderr, strings.NewReader(`{
	  "protocol_version":"compute.v1alpha1",
	  "task_id":"task-123",
	  "lease_id":"lease-123",
	  "provider_config":{"plugin_id":"workflow-plugin-product-capture","provider_id":"browser","contract_id":"product-capture.browser.v1","version":"v1.0.0"},
	  "operation":"capture_product",
	  "input":{"url":"https://www.amazon.com/dp/B08H75RTZ8","allowed_hosts":["www.amazon.com"]},
	  "surprise":true
	}`))
	if code == 0 {
		t.Fatalf("expected failure, stdout=%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "unknown field") {
		t.Fatalf("stderr missing strict decode error: %s", stderr.String())
	}
}

func TestMainRejectsUnsupportedDynamicOperation(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Main(nil, &stdout, &stderr, strings.NewReader(`{
	  "protocol_version":"compute.v1alpha1",
	  "task_id":"task-123",
	  "lease_id":"lease-123",
	  "provider_config":{"plugin_id":"workflow-plugin-product-capture","provider_id":"browser","contract_id":"product-capture.browser.v1","version":"v1.0.0","config_ref":"config://network-products/product-capture/browser"},
	  "operation":"scrape_checkout",
	  "input":{"url":"https://www.amazon.com/dp/B08H75RTZ8","allowed_hosts":["www.amazon.com"]}
	}`))
	if code == 0 {
		t.Fatalf("expected failure, stdout=%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "unsupported operation") {
		t.Fatalf("stderr missing operation error: %s", stderr.String())
	}
}

func TestPlaywrightScriptHandlesOnlyBenignAmazonContinuationGates(t *testing.T) {
	for _, disallowed := range []string{
		"stealth",
		"HeadlessChrome",
		"'continue',",
	} {
		if strings.Contains(playwrightCaptureScript, disallowed) {
			t.Fatalf("playwright script contains disallowed automation marker %q", disallowed)
		}
	}
	for _, required := range []string{
		"userAgent",
		"Mozilla/5.0",
		"AppleWebKit/537.36",
		"Chrome/",
		"navigator",
		"webdriver",
		"undefined",
		"--disable-blink-features=AutomationControlled",
		"--no-sandbox",
		"--disable-setuid-sandbox",
		"--disable-dev-shm-usage",
	} {
		if !strings.Contains(playwrightCaptureScript, required) {
			t.Fatalf("playwright script missing browser identity guard %q", required)
		}
	}
	if !strings.Contains(playwrightCaptureScript, "validateCaptcha") || !strings.Contains(playwrightCaptureScript, "manual review") {
		t.Fatalf("playwright script must fail closed on CAPTCHA/interstitial pages")
	}
	for _, required := range []string{
		"handleAmazonContinuationGate",
		"continue shopping",
		"data-product-capture-continuation-candidate",
		"clickFirstWorkingContinuation",
		"locator.nth(index)",
	} {
		if !strings.Contains(playwrightCaptureScript, required) {
			t.Fatalf("playwright script must handle benign Amazon continuation gate; missing %q", required)
		}
	}
}

func TestPlaywrightScriptPrefersStandardChromeChannel(t *testing.T) {
	for _, required := range []string{
		"channel: 'chrome'",
		"launchChromeBrowser",
	} {
		if !strings.Contains(playwrightCaptureScript, required) {
			t.Fatalf("playwright script should launch standard Chrome instead of bundled Chromium; missing %q", required)
		}
	}
	for _, disallowed := range []string{
		"channel of",
		"msedge",
		"chromium.launch(launchOptions)",
	} {
		if strings.Contains(playwrightCaptureScript, disallowed) {
			t.Fatalf("playwright script should not silently fall back to non-Chrome launch path %q", disallowed)
		}
	}
}

func TestPlaywrightScriptUsesPersistentProfileWhenConfigured(t *testing.T) {
	profileDir := filepath.Join(t.TempDir(), "chrome-profile")
	t.Setenv("PRODUCT_CAPTURE_BROWSER_PROFILE_DIR", profileDir)
	fakePlaywright := fmt.Sprintf(`
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
function withDocument(fn) {
  const previousDocument = global.document;
  global.document = {
    body: { textContent: 'product page' },
    querySelectorAll: (selector) => selector === '#productTitle' ? [{ value: '', textContent: ' Echo Dot ' }] : [],
    querySelector: (selector) => {
      if (selector === '#landingImage') return { getAttribute: (name) => name === 'src' ? 'https://m.media-amazon.com/images/I/echo.jpg' : '' };
      return null;
    },
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => { throw new Error('ephemeral browser launch used despite configured profile'); },
  launchPersistentContext: async (userDataDir, options) => {
    if (userDataDir !== %q) throw new Error('profile dir mismatch: ' + userDataDir);
    if (!options || options.channel !== 'chrome') throw new Error('standard Chrome channel not preserved');
    return {
      newPage: async () => ({
        addInitScript: async (fn, requestedURL) => { fn(requestedURL); },
        goto: async () => {},
        url: () => 'https://www.amazon.com/dp/B09B8V1LZ3',
        locator: (selector) => {
          if (selector === 'form[action*="/errors/validateCaptcha"]') return { count: async () => 0 };
          return { count: async () => 0, first: () => ({ click: async () => {} }) };
        },
        waitForLoadState: async () => {},
        waitForTimeout: async () => {},
        waitForFunction: async (fn) => {
          if (!withDocument(fn)) throw new TimeoutError('timeout');
        },
        evaluate: async (fn) => withDocument(fn),
        content: async () => '<html><head><link rel="canonical" href="https://www.amazon.com/dp/B09B8V1LZ3"></head><body><span id="productTitle">Echo Dot</span><img id="landingImage" src="https://m.media-amazon.com/images/I/echo.jpg"></body></html>',
      }),
      close: async () => {},
    };
  },
};
exports.errors = { TimeoutError };
`, profileDir)
	stdout, stderr, err := runPlaywrightScriptWithFakeURL(t, fakePlaywright, "https://www.amazon.com/dp/B09B8V1LZ3")
	if err != nil {
		t.Fatalf("capture script failed with configured persistent profile: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `id="productTitle"`) {
		t.Fatalf("capture script did not emit product html: %s", stdout.String())
	}
}

func TestPlaywrightScriptUsesExtractorCompatibleProductTitleEvidence(t *testing.T) {
	if strings.Contains(playwrightCaptureScript, "locator('#productTitle').waitFor") {
		t.Fatalf("playwright script uses strict #productTitle locator; Amazon may render a visible span and hidden input with that id")
	}
	for _, required := range []string{
		"waitForFunction",
		"document.querySelectorAll('#productTitle')",
		"titleNodes.some",
		"node.textContent",
		"node.value",
	} {
		if !strings.Contains(playwrightCaptureScript, required) {
			t.Fatalf("playwright script should wait for product title evidence; missing %q", required)
		}
	}
}

func TestPlaywrightScriptRejectsGenericGPPathMetadataEvidence(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
function withDocument(fn) {
  const previousDocument = global.document;
  const metaNode = { getAttribute: (name) => name === 'content' ? 'Amazon Echo Dot (newest model) - Vibrant sounding speaker' : '' };
  const canonicalNode = { getAttribute: (name) => name === 'href' ? 'https://www.amazon.com/gp/anything' : '' };
  const imageNode = { getAttribute: (name) => name === 'src' ? 'https://m.media-amazon.com/images/I/echo.jpg' : '' };
  if (global.__productCaptureRequestedURL !== 'https://www.amazon.com/gp/anything') {
    throw new Error('requested URL was not injected');
  }
  global.document = {
    body: { textContent: 'product page' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return [];
      return [];
    },
    querySelector: (selector) => {
      if (selector === 'meta[property="og:title"]') return metaNode;
      if (selector === 'link[rel="canonical"]') return canonicalNode;
      if (selector === '#landingImage') return imageNode;
      return null;
    },
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async (fn, requestedURL) => { fn(requestedURL); },
      goto: async () => {},
      url: () => 'https://www.amazon.com/gp/anything',
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForTimeout: async () => {},
      waitForFunction: async (fn) => {
        if (withDocument(fn)) throw new Error('generic /gp metadata was accepted by title wait predicate');
        throw new TimeoutError('timeout');
      },
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><head><link rel="canonical" href="https://www.amazon.com/gp/anything"><meta property="og:title" content="Amazon Echo Dot (newest model) - Vibrant sounding speaker"></head><body><img id="landingImage" src="https://m.media-amazon.com/images/I/echo.jpg"></body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	_, stderr, err := runPlaywrightScriptWithFakeURL(t, fakePlaywright, "https://www.amazon.com/gp/anything")
	if err == nil {
		t.Fatalf("expected generic /gp metadata to fail closed")
	}
	if !strings.Contains(stderr.String(), "amazon product page did not expose product title") {
		t.Fatalf("stderr missing product title failure: %s", stderr.String())
	}
}

func TestPlaywrightScriptRejectsMalformedASINMetadataEvidence(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
function withDocument(fn) {
  const previousDocument = global.document;
  const metaNode = { getAttribute: (name) => name === 'content' ? 'Amazon Echo Dot (newest model) - Vibrant sounding speaker' : '' };
  const canonicalNode = { getAttribute: (name) => name === 'href' ? 'https://www.amazon.com/dp/not-a-real-product' : '' };
  const imageNode = { getAttribute: (name) => name === 'src' ? 'https://m.media-amazon.com/images/I/echo.jpg' : '' };
  if (global.__productCaptureRequestedURL !== 'https://www.amazon.com/dp/not-a-real-product') {
    throw new Error('requested URL was not injected');
  }
  global.document = {
    body: { textContent: 'product page' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return [];
      return [];
    },
    querySelector: (selector) => {
      if (selector === 'meta[property="og:title"]') return metaNode;
      if (selector === 'link[rel="canonical"]') return canonicalNode;
      if (selector === '#landingImage') return imageNode;
      return null;
    },
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async (fn, requestedURL) => { fn(requestedURL); },
      goto: async () => {},
      url: () => 'https://www.amazon.com/dp/not-a-real-product',
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForTimeout: async () => {},
      waitForFunction: async (fn) => {
        if (withDocument(fn)) throw new Error('malformed ASIN metadata was accepted by title wait predicate');
        throw new TimeoutError('timeout');
      },
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><head><link rel="canonical" href="https://www.amazon.com/dp/not-a-real-product"><meta property="og:title" content="Amazon Echo Dot (newest model) - Vibrant sounding speaker"></head><body><img id="landingImage" src="https://m.media-amazon.com/images/I/echo.jpg"></body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	_, stderr, err := runPlaywrightScriptWithFakeURL(t, fakePlaywright, "https://www.amazon.com/dp/not-a-real-product")
	if err == nil {
		t.Fatalf("expected malformed ASIN metadata to fail closed")
	}
	if !strings.Contains(stderr.String(), "amazon product page did not expose product title") {
		t.Fatalf("stderr missing product title failure: %s", stderr.String())
	}
}

func TestPlaywrightScriptAcceptsGPProductMetadataEvidence(t *testing.T) {
	runPlaywrightMetadataEvidenceURLCase(t,
		"https://www.amazon.com/gp/product/B09B8V1LZ3",
		"https://www.amazon.com/gp/product/B09B8V1LZ3",
	)
}

func TestPlaywrightScriptAcceptsGPAWDMetadataEvidence(t *testing.T) {
	runPlaywrightMetadataEvidenceURLCase(t,
		"https://www.amazon.com/gp/aw/d/B09B8V1LZ3",
		"https://www.amazon.com/gp/aw/d/B09B8V1LZ3",
	)
}

func runPlaywrightMetadataEvidenceURLCase(t *testing.T, targetURL, canonicalURL string) {
	t.Helper()
	fakePlaywright := fmt.Sprintf(`
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
function withDocument(fn) {
  const previousDocument = global.document;
  const metaNode = { getAttribute: (name) => name === 'content' ? 'Amazon Echo Dot (newest model) - Vibrant sounding speaker' : '' };
  const canonicalNode = { getAttribute: (name) => name === 'href' ? %q : '' };
  const imageNode = { getAttribute: (name) => name === 'src' ? 'https://m.media-amazon.com/images/I/echo.jpg' : '' };
  if (global.__productCaptureRequestedURL !== %q) {
    throw new Error('requested URL was not injected');
  }
  global.document = {
    body: { textContent: 'product page' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return [];
      return [];
    },
    querySelector: (selector) => {
      if (selector === 'meta[property="og:title"]') return metaNode;
      if (selector === 'link[rel="canonical"]') return canonicalNode;
      if (selector === '#landingImage') return imageNode;
      return null;
    },
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async (fn, requestedURL) => { fn(requestedURL); },
      goto: async () => {},
      url: () => %q,
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForTimeout: async () => {},
      waitForFunction: async (fn) => {
        if (!withDocument(fn)) throw new TimeoutError('timeout');
      },
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><head><link rel="canonical" href="%s"><meta property="og:title" content="Amazon Echo Dot (newest model) - Vibrant sounding speaker"></head><body><img id="landingImage" src="https://m.media-amazon.com/images/I/echo.jpg"></body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`, canonicalURL, targetURL, targetURL, canonicalURL)
	stdout, stderr, err := runPlaywrightScriptWithFakeURL(t, fakePlaywright, targetURL)
	if err != nil {
		t.Fatalf("capture script failed with metadata evidence for %s: %v\nstderr=%s", targetURL, err, stderr.String())
	}
	snap, err := snapshot.ExtractAmazon(stdout.String(), snapshot.ExtractOptions{URL: targetURL})
	if err != nil {
		t.Fatalf("captured html should remain extractable for %s: %v", targetURL, err)
	}
	if snap.ExternalID != "B09B8V1LZ3" {
		t.Fatalf("asin: %q", snap.ExternalID)
	}
}

func TestPlaywrightScriptWaitsForCaptureRelevantNodes(t *testing.T) {
	for _, required := range []string{
		"optionalWait",
		"#landingImage",
		"#imgTagWrapperId img",
		"#main-image-container img",
		"#corePrice_feature_div .a-offscreen",
		".priceToPay .a-offscreen",
		"#deliveryBlockMessage",
		"#primeShippingMessage_feature_div",
		".catch(() => {})",
	} {
		if !strings.Contains(playwrightCaptureScript, required) {
			t.Fatalf("playwright script missing optional capture wait for %q", required)
		}
	}
}

func TestPlaywrightScriptOptionalWaitAcceptsMainImageContainer(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
function withDocument(fn) {
  const previousDocument = global.document;
  global.document = {
    body: { textContent: 'product page' },
    querySelectorAll: (selector) => selector === '#productTitle' ? [{ value: '', textContent: ' Echo Dot ' }] : [],
    querySelector: (selector) => {
      if (selector === '#main-image-container img') return { getAttribute: (name) => name === 'src' ? 'https://m.media-amazon.com/images/I/echo.jpg' : '' };
      return null;
    },
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async (fn, requestedURL) => { fn(requestedURL); },
      goto: async () => {},
      url: () => 'https://www.amazon.com/dp/B09B8V1LZ3',
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForTimeout: async () => {},
      waitForFunction: async (fn) => {
        if (!withDocument(fn)) throw new Error('optional/title predicate did not accept main image container');
      },
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><head><link rel="canonical" href="https://www.amazon.com/dp/B09B8V1LZ3"></head><body><span id="productTitle">Echo Dot</span><div id="main-image-container"><img src="https://m.media-amazon.com/images/I/echo.jpg"></div></body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	stdout, stderr, err := runPlaywrightScriptWithFakeURL(t, fakePlaywright, "https://www.amazon.com/dp/B09B8V1LZ3")
	if err != nil {
		t.Fatalf("capture script failed with main image optional wait evidence: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `main-image-container`) {
		t.Fatalf("capture script did not emit main image container html: %s", stdout.String())
	}
}

func TestPlaywrightScriptEmitsHTMLWhenTitleWaitTimesOut(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
function withDocument(fn) {
  const previousDocument = global.document;
  const titleNodes = [
    { value: '', textContent: '' },
    { value: '', textContent: ' Xbox Series X ' },
  ];
  global.document = {
    querySelectorAll: (selector) => selector === '#productTitle' ? titleNodes : [],
    querySelector: () => null,
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async () => {},
      goto: async () => {},
      locator: (selector) => {
        if (selector !== 'form[action*="/errors/validateCaptcha"]') throw new Error('unexpected selector ' + selector);
        return { count: async () => 0 };
      },
      waitForFunction: async (fn) => {
        withDocument(fn);
        throw new TimeoutError('Timeout 15000ms exceeded');
      },
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><head><link rel="canonical" href="https://www.amazon.com/dp/B08H75RTZ8"></head><body><input id="productTitle" value="Xbox Series X"><img id="landingImage" src="https://m.media-amazon.com/images/I/xbox.jpg"></body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	stdout, stderr, err := runPlaywrightScriptWithFakeURL(t, fakePlaywright, "https://www.amazon.com/dp/B08H75RTZ8")
	if err != nil {
		t.Fatalf("capture script failed after title wait timeout: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `id="productTitle"`) {
		t.Fatalf("capture script did not emit current page html: %s", stdout.String())
	}
	snap, err := snapshot.ExtractAmazon(stdout.String(), snapshot.ExtractOptions{URL: "https://www.amazon.com/dp/B08H75RTZ8"})
	if err != nil {
		t.Fatalf("captured html should remain extractable: %v", err)
	}
	if snap.Title != "Xbox Series X" {
		t.Fatalf("title: %q", snap.Title)
	}
}

func TestPlaywrightScriptAcceptsMetadataProductTitleEvidence(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
function withDocument(fn) {
  const previousDocument = global.document;
  const metaNode = { getAttribute: (name) => name === 'content' ? 'Amazon Echo Dot (newest model) - Vibrant sounding speaker' : '' };
  const canonicalNode = { getAttribute: (name) => name === 'href' ? 'https://www.amazon.com/Amazon-vibrant-helpful-routines-Charcoal/dp/B09B8V1LZ3' : '' };
  const imageNode = { getAttribute: (name) => name === 'src' ? 'https://m.media-amazon.com/images/I/echo.jpg' : '' };
  if (global.__productCaptureRequestedURL !== 'https://www.amazon.com/Amazon-vibrant-helpful-routines-Charcoal/dp/B09B8V1LZ3') {
    throw new Error('requested URL was not injected');
  }
  global.document = {
    body: { textContent: 'product page' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return [];
      return [];
    },
    querySelector: (selector) => {
      if (selector === 'meta[property="og:title"]') return metaNode;
      if (selector === 'link[rel="canonical"]') return canonicalNode;
      if (selector === '#landingImage') return imageNode;
      return null;
    },
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async (fn, requestedURL) => { fn(requestedURL); },
      goto: async () => {},
      url: () => 'https://www.amazon.com/Amazon-vibrant-helpful-routines-Charcoal/dp/B09B8V1LZ3',
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForTimeout: async () => {},
      waitForFunction: async (fn) => {
        if (!withDocument(fn)) throw new TimeoutError('timeout');
      },
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><head><link rel="canonical" href="https://www.amazon.com/Amazon-vibrant-helpful-routines-Charcoal/dp/B09B8V1LZ3"><meta property="og:title" content="Amazon Echo Dot (newest model) - Vibrant sounding speaker"></head><body><img id="landingImage" src="https://m.media-amazon.com/images/I/echo.jpg"></body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	stdout, stderr, err := runPlaywrightScriptWithFakeURL(t, fakePlaywright, "https://www.amazon.com/Amazon-vibrant-helpful-routines-Charcoal/dp/B09B8V1LZ3")
	if err != nil {
		t.Fatalf("capture script failed with metadata title evidence: %v\nstderr=%s", err, stderr.String())
	}
	snap, err := snapshot.ExtractAmazon(stdout.String(), snapshot.ExtractOptions{URL: "https://www.amazon.com/Amazon-vibrant-helpful-routines-Charcoal/dp/B09B8V1LZ3"})
	if err != nil {
		t.Fatalf("captured html should remain extractable: %v", err)
	}
	if snap.Title != "Amazon Echo Dot (newest model) - Vibrant sounding speaker" {
		t.Fatalf("title: %q", snap.Title)
	}
}

func TestPlaywrightScriptAcceptsMainImageContainerMetadataEvidence(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
function withDocument(fn) {
  const previousDocument = global.document;
  const metaNode = { getAttribute: (name) => name === 'content' ? 'Amazon Echo Dot (newest model) - Vibrant sounding speaker' : '' };
  const canonicalNode = { getAttribute: (name) => name === 'href' ? 'https://www.amazon.com/Amazon-vibrant-helpful-routines-Charcoal/dp/B09B8V1LZ3' : '' };
  const imageNode = { getAttribute: (name) => name === 'src' ? 'https://m.media-amazon.com/images/I/echo.jpg' : '' };
  if (global.__productCaptureRequestedURL !== 'https://www.amazon.com/Amazon-vibrant-helpful-routines-Charcoal/dp/B09B8V1LZ3') {
    throw new Error('requested URL was not injected');
  }
  global.document = {
    body: { textContent: 'product page' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return [];
      return [];
    },
    querySelector: (selector) => {
      if (selector === 'meta[property="og:title"]') return metaNode;
      if (selector === 'link[rel="canonical"]') return canonicalNode;
      if (selector === '#main-image-container img') return imageNode;
      return null;
    },
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async (fn, requestedURL) => { fn(requestedURL); },
      goto: async () => {},
      url: () => 'https://www.amazon.com/Amazon-vibrant-helpful-routines-Charcoal/dp/B09B8V1LZ3',
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForTimeout: async () => {},
      waitForFunction: async (fn) => {
        if (!withDocument(fn)) throw new TimeoutError('timeout');
      },
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><head><link rel="canonical" href="https://www.amazon.com/Amazon-vibrant-helpful-routines-Charcoal/dp/B09B8V1LZ3"><meta property="og:title" content="Amazon Echo Dot (newest model) - Vibrant sounding speaker"></head><body><div id="main-image-container"><img src="https://m.media-amazon.com/images/I/echo.jpg"></div></body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	stdout, stderr, err := runPlaywrightScriptWithFakeURL(t, fakePlaywright, "https://www.amazon.com/Amazon-vibrant-helpful-routines-Charcoal/dp/B09B8V1LZ3")
	if err != nil {
		t.Fatalf("capture script failed with main image container metadata evidence: %v\nstderr=%s", err, stderr.String())
	}
	snap, err := snapshot.ExtractAmazon(stdout.String(), snapshot.ExtractOptions{URL: "https://www.amazon.com/Amazon-vibrant-helpful-routines-Charcoal/dp/B09B8V1LZ3"})
	if err != nil {
		t.Fatalf("captured html should remain extractable: %v", err)
	}
	if snap.Title != "Amazon Echo Dot (newest model) - Vibrant sounding speaker" {
		t.Fatalf("title: %q", snap.Title)
	}
}

func TestPlaywrightScriptAcceptsPriceContainerMetadataEvidence(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
function withDocument(fn) {
  const previousDocument = global.document;
  const metaNode = { getAttribute: (name) => name === 'content' ? 'Amazon Echo Dot (newest model) - Vibrant sounding speaker' : '' };
  const canonicalNode = { getAttribute: (name) => name === 'href' ? 'https://www.amazon.com/dp/B09B8V1LZ3' : '' };
  const priceNode = { textContent: '$34.99', getAttribute: () => '' };
  if (global.__productCaptureRequestedURL !== 'https://www.amazon.com/dp/B09B8V1LZ3') {
    throw new Error('requested URL was not injected');
  }
  global.document = {
    body: { textContent: 'product page' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return [];
      return [];
    },
    querySelector: (selector) => {
      if (selector === 'meta[property="og:title"]') return metaNode;
      if (selector === 'link[rel="canonical"]') return canonicalNode;
      if (selector === '.priceToPay .a-offscreen') return priceNode;
      return null;
    },
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async (fn, requestedURL) => { fn(requestedURL); },
      goto: async () => {},
      url: () => 'https://www.amazon.com/dp/B09B8V1LZ3',
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForTimeout: async () => {},
      waitForFunction: async (fn) => {
        if (!withDocument(fn)) throw new TimeoutError('timeout');
      },
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><head><link rel="canonical" href="https://www.amazon.com/dp/B09B8V1LZ3"><meta property="og:title" content="Amazon Echo Dot (newest model) - Vibrant sounding speaker"></head><body><div class="priceToPay"><span class="a-offscreen">$34.99</span></div></body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	stdout, stderr, err := runPlaywrightScriptWithFakeURL(t, fakePlaywright, "https://www.amazon.com/dp/B09B8V1LZ3")
	if err != nil {
		t.Fatalf("capture script failed with price container metadata evidence: %v\nstderr=%s", err, stderr.String())
	}
	snap, err := snapshot.ExtractAmazon(stdout.String(), snapshot.ExtractOptions{URL: "https://www.amazon.com/dp/B09B8V1LZ3"})
	if err != nil {
		t.Fatalf("captured html should remain extractable: %v", err)
	}
	if snap.Title != "Amazon Echo Dot (newest model) - Vibrant sounding speaker" {
		t.Fatalf("title: %q", snap.Title)
	}
}

func TestPlaywrightScriptClicksContinuationEvenWhenMetadataTitleExists(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
let clicked = false;
const attrs = {};
const metaNode = { getAttribute: (name) => name === 'content' ? 'Amazon Echo Dot (newest model) - Vibrant sounding speaker' : '' };
const canonicalNode = { getAttribute: (name) => name === 'href' ? 'https://www.amazon.com/Amazon-vibrant-helpful-routines-Charcoal/dp/B09B8V1LZ3' : '' };
const imageNode = { getAttribute: (name) => name === 'src' ? 'https://m.media-amazon.com/images/I/echo.jpg' : '' };
const continuationNode = {
  value: 'Continue Shopping',
  textContent: '',
  getAttribute: (name) => attrs[name] || '',
  setAttribute: (name, value) => { attrs[name] = value; },
  removeAttribute: (name) => { delete attrs[name]; },
};
function withDocument(fn) {
  const previousDocument = global.document;
  const titleNodes = clicked ? [{ value: '', textContent: ' Echo Dot ' }] : [];
  if (global.__productCaptureRequestedURL !== 'https://www.amazon.com/Amazon-vibrant-helpful-routines-Charcoal/dp/B09B8V1LZ3') {
    throw new Error('requested URL was not injected');
  }
  global.document = {
    body: { textContent: clicked ? 'Echo Dot' : '\n  Continue Shopping  \n' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return titleNodes;
      if (selector === '[data-product-capture-continuation-candidate]') return attrs['data-product-capture-continuation-candidate'] ? [continuationNode] : [];
      if (selector === 'button,input[type="submit"],input[type="button"],a,[role="button"]') return clicked ? [] : [continuationNode];
      if (selector === 'form[action*="/errors/validateCaptcha"]') return [];
      if (selector === 'img[src*="captcha" i],img[alt*="captcha" i],input[name*="captcha" i],input[id*="captcha" i],iframe[src*="captcha" i],iframe[src*="challenge" i]') return [];
      return [];
    },
    querySelector: (selector) => {
      if (!clicked && selector === 'meta[property="og:title"]') return metaNode;
      if (!clicked && selector === 'link[rel="canonical"]') return canonicalNode;
      if (!clicked && selector === '#landingImage') return imageNode;
      return null;
    },
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async (fn, requestedURL) => { fn(requestedURL); },
      goto: async () => {},
      url: () => 'https://www.amazon.com/Amazon-vibrant-helpful-routines-Charcoal/dp/B09B8V1LZ3',
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        if (selector === '[data-product-capture-continuation-candidate="true"]') {
          return {
            count: async () => attrs['data-product-capture-continuation-candidate'] ? 1 : 0,
            first: () => ({ click: async () => { clicked = true; } }),
            nth: () => ({ click: async () => { clicked = true; } }),
          };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForTimeout: async () => {},
      waitForFunction: async (fn) => {
        if (!withDocument(fn)) throw new TimeoutError('timeout');
      },
      evaluate: async (fn) => withDocument(fn),
      content: async () => clicked
        ? '<html><head><link rel="canonical" href="https://www.amazon.com/Amazon-vibrant-helpful-routines-Charcoal/dp/B09B8V1LZ3"></head><body><span id="productTitle">Echo Dot</span><img id="landingImage" src="https://m.media-amazon.com/images/I/echo.jpg"></body></html>'
        : '<html><head><link rel="canonical" href="https://www.amazon.com/Amazon-vibrant-helpful-routines-Charcoal/dp/B09B8V1LZ3"><meta property="og:title" content="Amazon Echo Dot (newest model) - Vibrant sounding speaker"></head><body><input value="Continue Shopping"></body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	stdout, stderr, err := runPlaywrightScriptWithFakeURL(t, fakePlaywright, "https://www.amazon.com/Amazon-vibrant-helpful-routines-Charcoal/dp/B09B8V1LZ3")
	if err != nil {
		t.Fatalf("capture script failed after metadata-bearing continuation click: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `id="productTitle"`) {
		t.Fatalf("capture script did not emit product html after metadata-bearing continuation: %s", stdout.String())
	}
}

func TestPlaywrightScriptRejectsGenericMetadataProductTitleEvidence(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
function withDocument(fn) {
  const previousDocument = global.document;
  const metaNode = { getAttribute: (name) => name === 'content' ? 'Amazon.com. Spend less. Smile more.' : '' };
  const canonicalNode = { getAttribute: (name) => name === 'href' ? 'https://www.amazon.com/dp/B09B8V1LZ3' : '' };
  const imageNode = { getAttribute: (name) => name === 'src' ? 'https://m.media-amazon.com/images/I/echo.jpg' : '' };
  if (global.__productCaptureRequestedURL !== 'https://www.amazon.com/dp/B09B8V1LZ3') {
    throw new Error('requested URL was not injected');
  }
  global.document = {
    body: { textContent: 'shopping page without product details' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return [];
      return [];
    },
    querySelector: (selector) => {
      if (selector === 'meta[property="og:title"]') return metaNode;
      if (selector === 'link[rel="canonical"]') return canonicalNode;
      if (selector === '#landingImage') return imageNode;
      return null;
    },
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async (fn, requestedURL) => { fn(requestedURL); },
      goto: async () => {},
      url: () => 'https://www.amazon.com/dp/B09B8V1LZ3',
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForTimeout: async () => {},
      waitForFunction: async (fn) => {
        if (withDocument(fn)) throw new Error('generic metadata was accepted by title wait predicate');
        throw new TimeoutError('timeout');
      },
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><head><meta property="og:title" content="Amazon.com. Spend less. Smile more."></head><body>shopping page without product details</body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	_, stderr, err := runPlaywrightScriptWithFakeURL(t, fakePlaywright, "https://www.amazon.com/dp/B09B8V1LZ3")
	if err == nil {
		t.Fatalf("expected generic metadata title to fail closed")
	}
	if !strings.Contains(stderr.String(), "amazon product page did not expose product title") {
		t.Fatalf("stderr missing product title failure: %s", stderr.String())
	}
}

func TestPlaywrightScriptRejectsMetadataTitleWhenCanonicalASINDiffers(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
function withDocument(fn) {
  const previousDocument = global.document;
  const metaNode = { getAttribute: (name) => name === 'content' ? 'Amazon Echo Dot (newest model) - Vibrant sounding speaker' : '' };
  const canonicalNode = { getAttribute: (name) => name === 'href' ? 'https://www.amazon.com/dp/B08WRONG11' : '' };
  const imageNode = { getAttribute: (name) => name === 'src' ? 'https://m.media-amazon.com/images/I/echo.jpg' : '' };
  if (global.__productCaptureRequestedURL !== 'https://www.amazon.com/Amazon-vibrant-helpful-routines-Charcoal/dp/B09B8V1LZ3') {
    throw new Error('requested URL was not injected');
  }
  global.document = {
    body: { textContent: 'product page' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return [];
      return [];
    },
    querySelector: (selector) => {
      if (selector === 'meta[property="og:title"]') return metaNode;
      if (selector === 'link[rel="canonical"]') return canonicalNode;
      if (selector === '#landingImage') return imageNode;
      return null;
    },
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async (fn, requestedURL) => { fn(requestedURL); },
      goto: async () => {},
      url: () => 'https://www.amazon.com/Amazon-vibrant-helpful-routines-Charcoal/dp/B09B8V1LZ3',
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForTimeout: async () => {},
      waitForFunction: async (fn) => {
        if (withDocument(fn)) throw new Error('mismatched canonical metadata was accepted by title wait predicate');
        throw new TimeoutError('timeout');
      },
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><head><link rel="canonical" href="https://www.amazon.com/dp/B08WRONG11"><meta property="og:title" content="Amazon Echo Dot (newest model) - Vibrant sounding speaker"></head><body><img id="landingImage" src="https://m.media-amazon.com/images/I/echo.jpg"></body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	_, stderr, err := runPlaywrightScriptWithFakeURL(t, fakePlaywright, "https://www.amazon.com/Amazon-vibrant-helpful-routines-Charcoal/dp/B09B8V1LZ3")
	if err == nil {
		t.Fatalf("expected mismatched canonical ASIN metadata to fail closed")
	}
	if !strings.Contains(stderr.String(), "amazon product page did not expose product title") {
		t.Fatalf("stderr missing product title failure: %s", stderr.String())
	}
}

func TestPlaywrightScriptDoesNotClickContinuationOnMetadataReadyProductPage(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
let clicked = false;
const attrs = {};
const metaNode = { getAttribute: (name) => name === 'content' ? 'Amazon Echo Dot (newest model) - Vibrant sounding speaker' : '' };
const canonicalNode = { getAttribute: (name) => name === 'href' ? 'https://www.amazon.com/Amazon-vibrant-helpful-routines-Charcoal/dp/B09B8V1LZ3' : '' };
const imageNode = { getAttribute: (name) => name === 'src' ? 'https://m.media-amazon.com/images/I/echo.jpg' : '' };
const continuationNode = {
  value: 'Continue Shopping',
  textContent: '',
  getAttribute: (name) => attrs[name] || '',
  setAttribute: (name, value) => { attrs[name] = value; },
  removeAttribute: (name) => { delete attrs[name]; },
};
function withDocument(fn) {
  const previousDocument = global.document;
  if (global.__productCaptureRequestedURL !== 'https://www.amazon.com/Amazon-vibrant-helpful-routines-Charcoal/dp/B09B8V1LZ3') {
    throw new Error('requested URL was not injected');
  }
  global.document = {
    body: { textContent: 'product page with Continue Shopping accessory link' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return [];
      if (selector === '[data-product-capture-continuation-candidate]') return attrs['data-product-capture-continuation-candidate'] ? [continuationNode] : [];
      if (selector === 'button,input[type="submit"],input[type="button"],a,[role="button"]') return [continuationNode];
      if (selector === 'form[action*="/errors/validateCaptcha"]') return [];
      if (selector === 'img[src*="captcha" i],img[alt*="captcha" i],input[name*="captcha" i],input[id*="captcha" i],iframe[src*="captcha" i],iframe[src*="challenge" i]') return [];
      return [];
    },
    querySelector: (selector) => {
      if (selector === 'meta[property="og:title"]') return metaNode;
      if (selector === 'link[rel="canonical"]') return canonicalNode;
      if (selector === '#landingImage') return imageNode;
      return null;
    },
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async (fn, requestedURL) => { fn(requestedURL); },
      goto: async () => {},
      url: () => 'https://www.amazon.com/Amazon-vibrant-helpful-routines-Charcoal/dp/B09B8V1LZ3',
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        if (selector === '[data-product-capture-continuation-candidate="true"]') {
          return {
            count: async () => attrs['data-product-capture-continuation-candidate'] ? 1 : 0,
            first: () => ({ click: async () => { throw new Error('clicked unrelated continuation'); } }),
            nth: () => ({ click: async () => { throw new Error('clicked unrelated continuation'); } }),
          };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForTimeout: async () => {},
      waitForFunction: async (fn) => {
        if (!withDocument(fn)) throw new TimeoutError('timeout');
      },
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><head><link rel="canonical" href="https://www.amazon.com/Amazon-vibrant-helpful-routines-Charcoal/dp/B09B8V1LZ3"><meta property="og:title" content="Amazon Echo Dot (newest model) - Vibrant sounding speaker"></head><body><input value="Continue Shopping"><img id="landingImage" src="https://m.media-amazon.com/images/I/echo.jpg"></body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	stdout, stderr, err := runPlaywrightScriptWithFakeURL(t, fakePlaywright, "https://www.amazon.com/Amazon-vibrant-helpful-routines-Charcoal/dp/B09B8V1LZ3")
	if err != nil {
		t.Fatalf("capture script failed on metadata-ready product page: %v\nstderr=%s", err, stderr.String())
	}
	if strings.Contains(stdout.String(), `data-product-capture-continuation-candidate`) {
		t.Fatalf("capture script marked unrelated continuation on product page: html=%s", stdout.String())
	}
}

func TestPlaywrightScriptPropagatesNonTimeoutTitleWaitErrors(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
function withDocument(fn) {
  const previousDocument = global.document;
  global.document = {
    body: { textContent: '' },
    querySelectorAll: (selector) => selector === '#productTitle' ? [{ value: 'Xbox Series X', textContent: '' }] : [],
    querySelector: () => null,
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async () => {},
      goto: async () => {},
      locator: (selector) => {
        if (selector !== 'form[action*="/errors/validateCaptcha"]') throw new Error('unexpected selector ' + selector);
        return { count: async () => 0 };
      },
      waitForFunction: async () => { throw new Error('Target page, context or browser has been closed'); },
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><body><input id="productTitle" value="Xbox Series X"></body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	_, stderr, err := runPlaywrightScriptWithFake(t, fakePlaywright)
	if err == nil {
		t.Fatalf("expected non-timeout title wait error to propagate")
	}
	if !strings.Contains(stderr.String(), "Target page, context or browser has been closed") {
		t.Fatalf("stderr missing propagated browser error: %s", stderr.String())
	}
}

func TestPlaywrightScriptFailsClosedWhenInterstitialAppearsAfterTitleWait(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
let locatorChecks = 0;
function withDocument(fn) {
  const previousDocument = global.document;
  const titleNodes = [
    { value: '', textContent: '' },
    { value: 'Xbox Series X', textContent: '' },
  ];
  global.document = {
    querySelectorAll: (selector) => selector === '#productTitle' ? titleNodes : [],
    querySelector: () => null,
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async () => {},
      goto: async () => {},
      locator: (selector) => {
        if (selector !== 'form[action*="/errors/validateCaptcha"]') throw new Error('unexpected selector ' + selector);
        return { count: async () => locatorChecks++ === 0 ? 0 : 1 };
      },
      waitForFunction: async (fn) => withDocument(fn),
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><body><input id="productTitle" value="Xbox Series X"><form action="/errors/validateCaptcha"></form></body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	_, stderr, err := runPlaywrightScriptWithFake(t, fakePlaywright)
	if err == nil {
		t.Fatalf("expected late interstitial to fail closed")
	}
	if !strings.Contains(stderr.String(), "amazon interstitial requires manual review") {
		t.Fatalf("stderr missing interstitial failure: %s", stderr.String())
	}
}

func TestPlaywrightScriptClicksContinueShoppingBeforeTitleWait(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
let clicked = false;
const attrs = {};
const continuationNode = {
  value: 'Continue Shopping',
  textContent: '',
  getAttribute: (name) => attrs[name] || '',
  setAttribute: (name, value) => { attrs[name] = value; },
  removeAttribute: (name) => { delete attrs[name]; },
};
function withDocument(fn) {
  const previousDocument = global.document;
  const titleNodes = clicked ? [{ value: '', textContent: ' Echo Dot ' }] : [];
  global.document = {
    body: { textContent: ' Continue Shopping ' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return titleNodes;
      if (selector === '[data-product-capture-continuation-candidate]') return attrs['data-product-capture-continuation-candidate'] ? [continuationNode] : [];
      if (selector === 'button,input[type="submit"],input[type="button"],a,[role="button"]') return [continuationNode];
      return [];
    },
    querySelector: () => null,
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async () => {},
      goto: async () => {},
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        if (selector === '[data-product-capture-continuation-candidate="true"]') {
          return {
            count: async () => attrs['data-product-capture-continuation-candidate'] === 'true' && !clicked ? 1 : 0,
            first: () => ({
              click: async () => { clicked = true; },
            }),
            nth: () => ({
              click: async () => { clicked = true; },
            }),
          };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForFunction: async (fn) => withDocument(fn),
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><head><link rel="canonical" href="https://www.amazon.com/dp/B09B8V1LZ3"></head><body><span id="productTitle">Echo Dot</span></body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	stdout, stderr, err := runPlaywrightScriptWithFake(t, fakePlaywright)
	if err != nil {
		t.Fatalf("capture script failed after continuation click: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `id="productTitle"`) {
		t.Fatalf("capture script did not emit product html after continuation: %s", stdout.String())
	}
}

func TestPlaywrightScriptDoesNotClickValidateCaptchaContinuationForm(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
let clicked = false;
const attrs = {};
const continuationNode = {
  value: 'Continue Shopping',
  textContent: '',
  getAttribute: (name) => attrs[name] || '',
  setAttribute: (name, value) => { attrs[name] = value; },
  removeAttribute: (name) => { delete attrs[name]; },
};
const captchaForm = { contains: (node) => node === continuationNode };
function withDocument(fn) {
  const previousDocument = global.document;
  const titleNodes = clicked ? [{ value: '', textContent: ' Echo Dot ' }] : [];
  global.document = {
    body: { textContent: clicked ? 'Echo Dot' : 'Continue Shopping' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return titleNodes;
      if (selector === 'form[action*="/errors/validateCaptcha"]') return clicked ? [] : [captchaForm];
      if (selector === 'img[src*="captcha" i],img[alt*="captcha" i],input[name*="captcha" i],input[id*="captcha" i],iframe[src*="captcha" i],iframe[src*="challenge" i]') return [];
      if (selector === '[data-product-capture-continuation-candidate]') return attrs['data-product-capture-continuation-candidate'] ? [continuationNode] : [];
      if (selector === 'button,input[type="submit"],input[type="button"],a,[role="button"]') return clicked ? [] : [continuationNode];
      return [];
    },
    querySelector: () => null,
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async () => {},
      goto: async () => {},
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => clicked ? 0 : 1 };
        }
        if (selector === '[data-product-capture-continuation-candidate="true"]') {
          return {
            count: async () => attrs['data-product-capture-continuation-candidate'] === 'true' && !clicked ? 1 : 0,
            first: () => ({
              click: async () => { throw new Error('clicked validateCaptcha continuation'); },
            }),
            nth: () => ({
              click: async () => { throw new Error('clicked validateCaptcha continuation'); },
            }),
          };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForFunction: async (fn) => withDocument(fn),
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><head><link rel="canonical" href="https://www.amazon.com/dp/B09B8V1LZ3"></head><body><span id="productTitle">Echo Dot</span></body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	_, stderr, err := runPlaywrightScriptWithFake(t, fakePlaywright)
	if err == nil {
		t.Fatalf("expected validateCaptcha continuation form to fail closed")
	}
	if strings.Contains(stderr.String(), "clicked validateCaptcha continuation") || !strings.Contains(stderr.String(), "amazon interstitial requires manual review") {
		t.Fatalf("stderr missing manual review failure or clicked validateCaptcha gate: %s", stderr.String())
	}
}

func TestPlaywrightScriptDoesNotClickValidateCaptchaContinuationOutsideForm(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
const attrs = {};
const continuationNode = {
  value: 'Continue Shopping',
  textContent: '',
  getAttribute: (name) => attrs[name] || '',
  setAttribute: (name, value) => { attrs[name] = value; },
  removeAttribute: (name) => { delete attrs[name]; },
};
const captchaForm = { contains: () => false };
function withDocument(fn) {
  const previousDocument = global.document;
  global.document = {
    body: { textContent: 'Continue Shopping' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return [];
      if (selector === 'form[action*="/errors/validateCaptcha"]') return [captchaForm];
      if (selector === 'img[src*="captcha" i],img[alt*="captcha" i],input[name*="captcha" i],input[id*="captcha" i],iframe[src*="captcha" i],iframe[src*="challenge" i]') return [];
      if (selector === '[data-product-capture-continuation-candidate]') return attrs['data-product-capture-continuation-candidate'] ? [continuationNode] : [];
      if (selector === 'button,input[type="submit"],input[type="button"],a,[role="button"]') return [continuationNode];
      return [];
    },
    querySelector: () => null,
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async () => {},
      goto: async () => {},
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 1 };
        }
        if (selector === '[data-product-capture-continuation-candidate="true"]') {
          return {
            count: async () => attrs['data-product-capture-continuation-candidate'] === 'true' ? 1 : 0,
            first: () => ({
              click: async () => { throw new Error('clicked outside-form continuation'); },
            }),
            nth: () => ({
              click: async () => { throw new Error('clicked outside-form continuation'); },
            }),
          };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForFunction: async () => { throw new TimeoutError('timeout'); },
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><body>continue shopping outside form</body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	_, stderr, err := runPlaywrightScriptWithFake(t, fakePlaywright)
	if err == nil {
		t.Fatalf("expected outside-form validateCaptcha continuation to fail closed")
	}
	if strings.Contains(stderr.String(), "clicked outside-form continuation") || !strings.Contains(stderr.String(), "amazon interstitial requires manual review") {
		t.Fatalf("stderr missing manual review failure or clicked outside-form gate: %s", stderr.String())
	}
}

func TestPlaywrightScriptClicksCaseInsensitiveContinuationSubmitValue(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
let clicked = false;
const attrs = {};
const continuationNode = {
  value: 'continue shopping',
  textContent: '',
  getAttribute: (name) => attrs[name] || '',
  setAttribute: (name, value) => { attrs[name] = value; },
  removeAttribute: (name) => { delete attrs[name]; },
};
function withDocument(fn) {
  const previousDocument = global.document;
  const titleNodes = clicked ? [{ value: '', textContent: ' Echo Dot ' }] : [];
  global.document = {
    body: { textContent: ' continue shopping ' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return titleNodes;
      if (selector === '[data-product-capture-continuation-candidate]') return attrs['data-product-capture-continuation-candidate'] ? [continuationNode] : [];
      if (selector === 'button,input[type="submit"],input[type="button"],a,[role="button"]') return [continuationNode];
      return [];
    },
    querySelector: () => null,
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async () => {},
      goto: async () => {},
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        if (selector === '[data-product-capture-continuation-candidate="true"]') {
          return {
            count: async () => attrs['data-product-capture-continuation-candidate'] === 'true' && !clicked ? 1 : 0,
            first: () => ({
              click: async () => { clicked = true; },
            }),
            nth: () => ({
              click: async () => { clicked = true; },
            }),
          };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForFunction: async (fn) => withDocument(fn),
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><head><link rel="canonical" href="https://www.amazon.com/dp/B09B8V1LZ3"></head><body><span id="productTitle">Echo Dot</span></body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	stdout, stderr, err := runPlaywrightScriptWithFake(t, fakePlaywright)
	if err != nil {
		t.Fatalf("capture script failed after case-insensitive continuation submit click: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `id="productTitle"`) {
		t.Fatalf("capture script did not emit product html after continuation submit: %s", stdout.String())
	}
}

func TestPlaywrightScriptClicksNormalizedContinuationControl(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
let clicked = false;
const attrs = {};
const continuationNode = {
  value: ' Continue   Shopping ',
  textContent: '',
  getAttribute: (name) => attrs[name] || '',
  setAttribute: (name, value) => { attrs[name] = value; },
};
function withDocument(fn) {
  const previousDocument = global.document;
  const titleNodes = clicked ? [{ value: '', textContent: ' Echo Dot ' }] : [];
  global.document = {
    body: { textContent: ' Continue Shopping ' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return titleNodes;
      if (selector === '[data-product-capture-continuation-candidate]') return attrs['data-product-capture-continuation-candidate'] ? [continuationNode] : [];
      if (selector === 'button,input[type="submit"],input[type="button"],a,[role="button"]') return [continuationNode];
      return [];
    },
    querySelector: () => null,
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async () => {},
      goto: async () => {},
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        if (selector.includes('Continue Shopping')) {
          return { count: async () => 0, first: () => ({ click: async () => {} }) };
        }
        if (selector === '[data-product-capture-continuation-candidate="true"]') {
          return {
            count: async () => attrs['data-product-capture-continuation-candidate'] === 'true' && !clicked ? 1 : 0,
            first: () => ({
              click: async () => { clicked = true; },
            }),
          };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForFunction: async (fn) => withDocument(fn),
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><head><link rel="canonical" href="https://www.amazon.com/dp/B09B8V1LZ3"></head><body><span id="productTitle">Echo Dot</span></body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	stdout, stderr, err := runPlaywrightScriptWithFake(t, fakePlaywright)
	if err != nil {
		t.Fatalf("capture script failed after normalized continuation click: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `id="productTitle"`) {
		t.Fatalf("capture script did not emit product html after normalized continuation: %s", stdout.String())
	}
}

func TestPlaywrightScriptClicksSimilarBenignContinuationLabel(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
let clicked = false;
const attrs = {};
const continuationNode = {
  value: ' Continue to product ',
  textContent: '',
  getAttribute: (name) => attrs[name] || '',
  setAttribute: (name, value) => { attrs[name] = value; },
  removeAttribute: (name) => { delete attrs[name]; },
};
function withDocument(fn) {
  const previousDocument = global.document;
  const titleNodes = clicked ? [{ value: '', textContent: ' Echo Dot ' }] : [];
  global.document = {
    body: { textContent: ' Continue to product ' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return titleNodes;
      if (selector === 'button,input[type="submit"],input[type="button"],a,[role="button"]') return [continuationNode];
      if (selector === '[data-product-capture-continuation-candidate="true"]') return attrs['data-product-capture-continuation-candidate'] === 'true' ? [continuationNode] : [];
      return [];
    },
    querySelector: () => null,
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async () => {},
      goto: async () => {},
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        if (selector.includes('Continue Shopping')) {
          return { count: async () => 0, first: () => ({ click: async () => {} }) };
        }
        if (selector === '[data-product-capture-continuation-candidate="true"]') {
          return {
            count: async () => attrs['data-product-capture-continuation-candidate'] === 'true' && !clicked ? 1 : 0,
            first: () => ({
              click: async () => { clicked = true; },
            }),
            nth: () => ({
              click: async () => { clicked = true; },
            }),
          };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForFunction: async (fn) => withDocument(fn),
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><body><span id="productTitle">Echo Dot</span></body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	stdout, stderr, err := runPlaywrightScriptWithFake(t, fakePlaywright)
	if err != nil {
		t.Fatalf("capture script failed after similar continuation click: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `id="productTitle"`) {
		t.Fatalf("capture script did not emit product html after similar continuation: %s", stdout.String())
	}
}

func TestPlaywrightScriptSkipsUnclickableNormalizedContinuationCandidate(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
let clicked = false;
const attrs = [{}, {}];
const continuationNodes = [
  {
    value: ' Continue   Shopping ',
    textContent: '',
    getAttribute: (name) => attrs[0][name] || '',
    setAttribute: (name, value) => { attrs[0][name] = value; },
    removeAttribute: (name) => { delete attrs[0][name]; },
  },
  {
    value: ' Continue   Shopping ',
    textContent: '',
    getAttribute: (name) => attrs[1][name] || '',
    setAttribute: (name, value) => { attrs[1][name] = value; },
    removeAttribute: (name) => { delete attrs[1][name]; },
  },
];
function withDocument(fn) {
  const previousDocument = global.document;
  const titleNodes = clicked ? [{ value: '', textContent: ' Echo Dot ' }] : [];
  global.document = {
    body: { textContent: ' Continue Shopping ' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return titleNodes;
      if (selector === 'button,input[type="submit"],input[type="button"],a,[role="button"]') return continuationNodes;
      return [];
    },
    querySelector: () => null,
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
function continuationLocator() {
  const candidates = continuationNodes
    .map((node, index) => ({ node, index }))
    .filter(({ index }) => attrs[index]['data-product-capture-continuation-candidate'] === 'true');
  return {
    count: async () => clicked ? 0 : candidates.length,
    first: () => ({
      click: async () => { throw new TimeoutError('first candidate is hidden'); },
    }),
    nth: (index) => ({
      click: async () => {
        if (index === 0) throw new TimeoutError('first candidate is hidden');
        clicked = true;
      },
    }),
  };
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async () => {},
      goto: async () => {},
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        if (selector.includes('Continue Shopping')) {
          return { count: async () => 0, first: () => ({ click: async () => {} }) };
        }
        if (selector === '[data-product-capture-continuation-candidate="true"]') {
          return continuationLocator();
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForFunction: async (fn) => withDocument(fn),
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><head><link rel="canonical" href="https://www.amazon.com/dp/B09B8V1LZ3"></head><body><span id="productTitle">Echo Dot</span></body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	stdout, stderr, err := runPlaywrightScriptWithFake(t, fakePlaywright)
	if err != nil {
		t.Fatalf("capture script failed after second normalized continuation click: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `id="productTitle"`) {
		t.Fatalf("capture script did not emit product html after second normalized continuation: %s", stdout.String())
	}
}

func TestPlaywrightScriptFallsBackToNormalizedContinuationAfterExactClickFails(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
let clicked = false;
const attrs = {};
const continuationNode = {
  value: ' Continue   Shopping ',
  textContent: '',
  getAttribute: (name) => attrs[name] || '',
  setAttribute: (name, value) => { attrs[name] = value; },
  removeAttribute: (name) => { delete attrs[name]; },
};
function withDocument(fn) {
  const previousDocument = global.document;
  const titleNodes = clicked ? [{ value: '', textContent: ' Echo Dot ' }] : [];
  global.document = {
    body: { textContent: ' Continue Shopping ' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return titleNodes;
      if (selector === 'button,input[type="submit"],input[type="button"],a,[role="button"]') return [continuationNode];
      return [];
    },
    querySelector: () => null,
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async () => {},
      goto: async () => {},
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        if (selector.includes('Continue Shopping')) {
          return {
            count: async () => 1,
            first: () => ({
              click: async () => { throw new TimeoutError('exact candidate is hidden'); },
            }),
            nth: () => ({
              click: async () => { throw new TimeoutError('exact candidate is hidden'); },
            }),
          };
        }
        if (selector === '[data-product-capture-continuation-candidate="true"]') {
          return {
            count: async () => attrs['data-product-capture-continuation-candidate'] === 'true' && !clicked ? 1 : 0,
            first: () => ({
              click: async () => { clicked = true; },
            }),
            nth: () => ({
              click: async () => { clicked = true; },
            }),
          };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForFunction: async (fn) => withDocument(fn),
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><head><link rel="canonical" href="https://www.amazon.com/dp/B09B8V1LZ3"></head><body><span id="productTitle">Echo Dot</span></body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	stdout, stderr, err := runPlaywrightScriptWithFake(t, fakePlaywright)
	if err != nil {
		t.Fatalf("capture script failed after exact-to-normalized continuation fallback: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `id="productTitle"`) {
		t.Fatalf("capture script did not emit product html after exact-to-normalized fallback: %s", stdout.String())
	}
}

func TestPlaywrightScriptRemovesContinuationMarkersBeforeCapture(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
let clicked = false;
const attrs = {};
const continuationNode = {
  value: ' Continue   Shopping ',
  textContent: '',
  getAttribute: (name) => attrs[name] || '',
  setAttribute: (name, value) => { attrs[name] = value; },
  removeAttribute: (name) => { delete attrs[name]; },
};
function withDocument(fn) {
  const previousDocument = global.document;
  const titleNodes = clicked ? [{ value: '', textContent: ' Echo Dot ' }] : [];
  global.document = {
    body: { textContent: ' Continue Shopping ' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return titleNodes;
      if (selector === '[data-product-capture-continuation-candidate]') return attrs['data-product-capture-continuation-candidate'] ? [continuationNode] : [];
      if (selector === 'button,input[type="submit"],input[type="button"],a,[role="button"]') return [continuationNode];
      return [];
    },
    querySelector: () => null,
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async () => {},
      goto: async () => {},
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        if (selector.includes('Continue Shopping')) {
          return { count: async () => 0, first: () => ({ click: async () => {} }) };
        }
        if (selector === '[data-product-capture-continuation-candidate="true"]') {
          return {
            count: async () => attrs['data-product-capture-continuation-candidate'] === 'true' && !clicked ? 1 : 0,
            first: () => ({
              click: async () => { clicked = true; },
            }),
            nth: () => ({
              click: async () => { clicked = true; },
            }),
          };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForFunction: async (fn) => withDocument(fn),
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><body><span id="productTitle">Echo Dot</span><input value="Continue Shopping" ' + (attrs['data-product-capture-continuation-candidate'] ? 'data-product-capture-continuation-candidate="true"' : '') + '></body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	stdout, stderr, err := runPlaywrightScriptWithFake(t, fakePlaywright)
	if err != nil {
		t.Fatalf("capture script failed after marker cleanup: %v\nstderr=%s", err, stderr.String())
	}
	if strings.Contains(stdout.String(), "data-product-capture-continuation-candidate") {
		t.Fatalf("capture output leaked continuation marker: %s", stdout.String())
	}
}

func TestPlaywrightScriptFailsWhenContinuationMarkerCleanupFails(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
let clicked = false;
let cleanupSweep = false;
const attrs = {};
const continuationNode = {
  value: ' Continue   Shopping ',
  textContent: '',
  getAttribute: (name) => attrs[name] || '',
  setAttribute: (name, value) => { attrs[name] = value; },
  removeAttribute: (name) => {
    if (cleanupSweep) throw new Error('marker cleanup failed');
    delete attrs[name];
  },
};
function withDocument(fn) {
  const previousDocument = global.document;
  const titleNodes = clicked ? [{ value: '', textContent: ' Echo Dot ' }] : [];
  global.document = {
    body: { textContent: ' Continue Shopping ' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return titleNodes;
      if (selector === '[data-product-capture-continuation-candidate]') return attrs['data-product-capture-continuation-candidate'] ? [continuationNode] : [];
      if (selector === 'button,input[type="submit"],input[type="button"],a,[role="button"]') return [continuationNode];
      return [];
    },
    querySelector: () => null,
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async () => {},
      goto: async () => {},
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        if (selector.includes('Continue Shopping')) {
          return { count: async () => 0, first: () => ({ click: async () => {} }) };
        }
        if (selector === '[data-product-capture-continuation-candidate="true"]') {
          return {
            count: async () => attrs['data-product-capture-continuation-candidate'] === 'true' && !clicked ? 1 : 0,
            first: () => ({
              click: async () => { clicked = true; },
            }),
            nth: () => ({
              click: async () => { clicked = true; },
            }),
          };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForFunction: async (fn) => withDocument(fn),
      evaluate: async (fn) => {
        cleanupSweep = String(fn).includes("querySelectorAll('[' + marker + ']'") && !String(fn).includes('return { titleReady');
        try {
          return withDocument(fn);
        } finally {
          cleanupSweep = false;
        }
      },
      content: async () => '<html><body><span id="productTitle">Echo Dot</span><input value="Continue Shopping" data-product-capture-continuation-candidate="true"></body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	_, stderr, err := runPlaywrightScriptWithFake(t, fakePlaywright)
	if err == nil {
		t.Fatalf("expected marker cleanup failure")
	}
	if !strings.Contains(stderr.String(), "amazon continuation marker cleanup failed") {
		t.Fatalf("stderr missing marker cleanup failure: %s", stderr.String())
	}
}

func TestPlaywrightScriptRemovesMutatedContinuationMarkersBeforeCapture(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
let cleanupSweep = false;
const attrs = { 'data-product-capture-continuation-candidate': 'true' };
const mutatedNode = {
  value: '',
  textContent: '',
  getAttribute: (name) => attrs[name] || '',
  setAttribute: (name, value) => { attrs[name] = value; },
  removeAttribute: (name) => { delete attrs[name]; },
};
function withDocument(fn) {
  const previousDocument = global.document;
  global.document = {
    body: { textContent: ' product page ' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return [{ value: '', textContent: ' Echo Dot ' }];
      if (selector === '[data-product-capture-continuation-candidate]') return cleanupSweep && attrs['data-product-capture-continuation-candidate'] ? [mutatedNode] : [];
      if (selector === 'button,input[type="submit"],input[type="button"],a,[role="button"]') return [];
      return [];
    },
    querySelector: () => null,
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async () => {},
      goto: async () => {},
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForFunction: async (fn) => withDocument(fn),
      evaluate: async (fn) => {
        cleanupSweep = String(fn).includes("querySelectorAll('[' + marker + ']'") && !String(fn).includes('return { titleReady');
        try {
          return withDocument(fn);
        } finally {
          cleanupSweep = false;
        }
      },
      content: async () => '<html><body><span id="productTitle">Echo Dot</span><div ' + (attrs['data-product-capture-continuation-candidate'] ? 'data-product-capture-continuation-candidate="true"' : '') + '></div></body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	stdout, stderr, err := runPlaywrightScriptWithFake(t, fakePlaywright)
	if err != nil {
		t.Fatalf("capture script failed after mutated marker cleanup: %v\nstderr=%s", err, stderr.String())
	}
	if strings.Contains(stdout.String(), "data-product-capture-continuation-candidate") {
		t.Fatalf("capture output leaked mutated continuation marker: %s", stdout.String())
	}
}

func TestPlaywrightScriptDoesNotClickCaptchaLikeContinuation(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
function withDocument(fn) {
  const previousDocument = global.document;
  global.document = {
    body: { textContent: "Sorry, we need to make sure you're not a robot." },
    querySelectorAll: (selector) => selector === '#productTitle' ? [] : [],
    querySelector: () => null,
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async () => {},
      goto: async () => {},
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        if (selector.includes('[value="Continue Shopping" i]') || selector.includes('Continue Shopping')) {
          return {
            count: async () => 1,
            first: () => ({
              click: async () => { throw new Error('clicked CAPTCHA-like continuation'); },
            }),
          };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForFunction: async (fn) => withDocument(fn),
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><body>captcha</body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	_, stderr, err := runPlaywrightScriptWithFake(t, fakePlaywright)
	if err == nil {
		t.Fatalf("expected CAPTCHA-like continuation page to fail closed")
	}
	if strings.Contains(stderr.String(), "clicked CAPTCHA-like continuation") || !strings.Contains(stderr.String(), "amazon interstitial requires manual review") {
		t.Fatalf("stderr missing manual review failure or clicked CAPTCHA-like gate: %s", stderr.String())
	}
}

func TestPlaywrightScriptDoesNotClickChallengeLabeledExactContinuation(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
function withDocument(fn) {
  const previousDocument = global.document;
  const attrs = {};
  const continuationNode = {
    value: 'Continue Shopping verification required',
    textContent: '',
    getAttribute: (name) => attrs[name] || '',
    setAttribute: (name, value) => { attrs[name] = value; },
    removeAttribute: (name) => { delete attrs[name]; },
  };
  global.document = {
    body: { textContent: 'Continue Shopping verification required' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return [];
      if (selector === 'button,input[type="submit"],input[type="button"],a,[role="button"]') return [continuationNode];
      return [];
    },
    querySelector: () => null,
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async () => {},
      goto: async () => {},
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        if (selector.includes('[value="Continue Shopping" i]') || selector.includes('Continue Shopping')) {
          return {
            count: async () => 1,
            first: () => ({
              click: async () => { throw new Error('clicked challenge-labeled exact continuation'); },
            }),
          };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForFunction: async (fn) => withDocument(fn),
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><body>challenge</body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	_, stderr, err := runPlaywrightScriptWithFake(t, fakePlaywright)
	if err == nil {
		t.Fatalf("expected challenge-labeled exact continuation to fail closed")
	}
	if strings.Contains(stderr.String(), "clicked challenge-labeled exact continuation") || !strings.Contains(stderr.String(), "amazon product page did not expose product title") {
		t.Fatalf("stderr missing closed no-title failure or clicked challenge-labeled gate: %s", stderr.String())
	}
}

func TestPlaywrightScriptDoesNotClickGenericContinueOnBlockedAmazonPage(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
const attrs = {};
const continuationNode = {
  value: 'Continue',
  textContent: '',
  getAttribute: (name) => attrs[name] || '',
  setAttribute: (name, value) => { attrs[name] = value; },
  removeAttribute: (name) => { delete attrs[name]; },
};
function withDocument(fn) {
  const previousDocument = global.document;
  global.document = {
    body: { textContent: 'We detected unusual activity. Continue to sign in.' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return [];
      if (selector === '[data-product-capture-continuation-candidate]') return attrs['data-product-capture-continuation-candidate'] ? [continuationNode] : [];
      if (selector === 'button,input[type="submit"],input[type="button"],a,[role="button"]') return [continuationNode];
      return [];
    },
    querySelector: () => null,
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async () => {},
      goto: async () => {},
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        if (selector === '[data-product-capture-continuation-candidate="true"]') {
          return {
            count: async () => attrs['data-product-capture-continuation-candidate'] === 'true' ? 1 : 0,
            first: () => ({
              click: async () => { throw new Error('clicked generic blocked continuation'); },
            }),
            nth: () => ({
              click: async () => { throw new Error('clicked generic blocked continuation'); },
            }),
          };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForTimeout: async () => {},
      waitForFunction: async () => { throw new TimeoutError('timeout'); },
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><body>blocked continue</body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	_, stderr, err := runPlaywrightScriptWithFake(t, fakePlaywright)
	if err == nil {
		t.Fatalf("expected generic blocked continuation to fail closed")
	}
	if strings.Contains(stderr.String(), "clicked generic blocked continuation") || !strings.Contains(stderr.String(), "amazon product page did not expose product title") {
		t.Fatalf("stderr missing closed no-title failure or clicked generic blocked gate: %s", stderr.String())
	}
}

func TestPlaywrightScriptDoesNotClickImageOnlyCaptchaContinuation(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
function withDocument(fn) {
  const previousDocument = global.document;
  global.document = {
    body: { textContent: 'Continue Shopping' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return [];
      if (selector === 'img[src*="captcha" i],img[alt*="captcha" i],input[name*="captcha" i],input[id*="captcha" i],iframe[src*="captcha" i],iframe[src*="challenge" i]') return [{}];
      return [];
    },
    querySelector: () => null,
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async () => {},
      goto: async () => {},
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        if (selector.includes('[value="Continue Shopping" i]') || selector.includes('Continue Shopping')) {
          return {
            count: async () => 1,
            first: () => ({
              click: async () => { throw new Error('clicked image-only CAPTCHA continuation'); },
            }),
          };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForFunction: async (fn) => withDocument(fn),
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><body>captcha image</body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	_, stderr, err := runPlaywrightScriptWithFake(t, fakePlaywright)
	if err == nil {
		t.Fatalf("expected image-only CAPTCHA page to fail closed")
	}
	if strings.Contains(stderr.String(), "clicked image-only CAPTCHA continuation") || !strings.Contains(stderr.String(), "amazon interstitial requires manual review") {
		t.Fatalf("stderr missing manual review failure or clicked CAPTCHA-like gate: %s", stderr.String())
	}
}

func TestPlaywrightScriptDoesNotClickWhenInterstitialCheckIsUnknown(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async () => {},
      goto: async () => {},
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        if (selector.includes('[value="Continue Shopping" i]') || selector.includes('Continue Shopping')) {
          return {
            count: async () => 1,
            first: () => ({
              click: async () => { throw new Error('clicked unknown interstitial continuation'); },
            }),
          };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForFunction: async () => { throw new TimeoutError('timeout'); },
      evaluate: async () => { throw new Error('execution context destroyed'); },
      content: async () => '<html><body>unknown</body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	_, stderr, err := runPlaywrightScriptWithFake(t, fakePlaywright)
	if err == nil {
		t.Fatalf("expected unknown interstitial state to fail closed")
	}
	if strings.Contains(stderr.String(), "clicked unknown interstitial continuation") || !strings.Contains(stderr.String(), "amazon interstitial requires manual review") {
		t.Fatalf("stderr missing manual review failure or clicked unknown gate: %s", stderr.String())
	}
}

func TestPlaywrightScriptReportsSanitizedNoTitleDiagnostics(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
function withDocument(fn) {
  const previousDocument = global.document;
  global.document = {
    body: { textContent: 'shopping page without product details' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return [];
      if (selector === 'button,input[type="submit"],input[type="button"],a,[role="button"]') return [];
      return [];
    },
    querySelector: () => null,
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async () => {},
      goto: async () => {},
      url: () => 'https://www.amazon.com/dp/B09B8V1LZ3',
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForTimeout: async () => {},
      waitForFunction: async () => { throw new TimeoutError('timeout'); },
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><body>shopping page without product details</body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	_, stderr, err := runPlaywrightScriptWithFake(t, fakePlaywright)
	if err == nil {
		t.Fatalf("expected missing title to fail")
	}
	for _, want := range []string{
		"amazon product page did not expose product title",
		"title_ready=false",
		"captcha=false",
		"continuation_candidates=0",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr missing sanitized diagnostic %q: %s", want, stderr.String())
		}
	}
	for _, leak := range []string{
		"https://www.amazon.com/dp/B09B8V1LZ3",
		"shopping page without product details",
		"<html><body>",
	} {
		if strings.Contains(stderr.String(), leak) {
			t.Fatalf("stderr leaked page detail %q: %s", leak, stderr.String())
		}
	}
}

func TestPlaywrightScriptDiagnosticsDoNotLeakArbitraryControlLabels(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
const privateControl = {
  value: '',
  textContent: 'Jane Customer jane@example.com 123 Main Street',
  getAttribute: () => '',
  setAttribute: () => {},
  removeAttribute: () => {},
};
function withDocument(fn) {
  const previousDocument = global.document;
  global.document = {
    body: { textContent: 'shopping page without product details' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return [];
      if (selector === 'button,input[type="submit"],input[type="button"],a,[role="button"]') return [privateControl];
      return [];
    },
    querySelector: () => null,
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async () => {},
      goto: async () => {},
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForTimeout: async () => {},
      waitForFunction: async () => { throw new TimeoutError('timeout'); },
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><body>shopping page without product details</body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	_, stderr, err := runPlaywrightScriptWithFake(t, fakePlaywright)
	if err == nil {
		t.Fatalf("expected missing title to fail")
	}
	for _, want := range []string{
		"amazon product page did not expose product title",
		"continuation_candidates=0",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr missing diagnostic %q: %s", want, stderr.String())
		}
	}
	for _, leak := range []string{
		"control_labels=",
		"Jane",
		"jane",
		"example",
		"123 Main",
	} {
		if strings.Contains(stderr.String(), leak) {
			t.Fatalf("stderr leaked arbitrary control label %q: %s", leak, stderr.String())
		}
	}
}

func TestPlaywrightScriptPreservesFinalTitleReadFailures(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
let titleReadyCalls = 0;
function withDocument(fn) {
  const previousDocument = global.document;
  global.document = {
    body: { textContent: 'plain page' },
    querySelectorAll: (selector) => selector === '#productTitle' ? [] : [],
    querySelector: () => null,
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async () => {},
      goto: async () => {},
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForTimeout: async () => {},
      waitForFunction: async () => { throw new TimeoutError('timeout'); },
      evaluate: async (fn) => {
        if (String(fn).includes('return { titleReady')) return withDocument(fn);
        if (String(fn).includes("querySelectorAll('#productTitle')")) {
          titleReadyCalls++;
          if (titleReadyCalls >= 3) throw new Error('execution context destroyed during final title check');
        }
        return withDocument(fn);
      },
      content: async () => '<html><body>plain page</body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	_, stderr, err := runPlaywrightScriptWithFake(t, fakePlaywright)
	if err == nil {
		t.Fatalf("expected final title check failure")
	}
	if !strings.Contains(stderr.String(), "amazon product title readiness check failed") {
		t.Fatalf("stderr missing final title readiness failure: %s", stderr.String())
	}
	if strings.Contains(stderr.String(), "amazon product page did not expose product title") {
		t.Fatalf("final title readiness failure was reported as missing title: %s", stderr.String())
	}
}

func TestPlaywrightScriptReportsUnavailableDiagnosticsWhenCaptchaFormCountFails(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
function withDocument(fn) {
  const previousDocument = global.document;
  global.document = {
    body: { textContent: 'shopping page without product details' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return [];
      if (selector === 'button,input[type="submit"],input[type="button"],a,[role="button"]') return [];
      return [];
    },
    querySelector: () => null,
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
let titleReadyCalls = 0;
let diagnosticsMayFail = false;
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async () => {},
      goto: async () => {},
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return {
            count: async () => {
              if (diagnosticsMayFail) throw new Error('locator context destroyed');
              return 0;
            },
          };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForTimeout: async () => {},
      waitForFunction: async () => { throw new TimeoutError('timeout'); },
      evaluate: async (fn) => {
        if (String(fn).includes('return { titleReady')) return withDocument(fn);
        if (String(fn).includes("querySelectorAll('#productTitle')")) {
          titleReadyCalls++;
          if (titleReadyCalls >= 3) diagnosticsMayFail = true;
        }
        return withDocument(fn);
      },
      content: async () => '<html><body>shopping page without product details</body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	_, stderr, err := runPlaywrightScriptWithFake(t, fakePlaywright)
	if err == nil {
		t.Fatalf("expected missing title to fail")
	}
	for _, want := range []string{
		"amazon product page did not expose product title",
		"diagnostics_available=false",
		"diagnostics_error=captcha_form_count_failed",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr missing unavailable diagnostic %q: %s", want, stderr.String())
		}
	}
	for _, unavailable := range []string{
		"captcha_form_count=0",
		"captcha_challenge_count=0",
		"continuation_candidates=0",
	} {
		if strings.Contains(stderr.String(), unavailable) {
			t.Fatalf("stderr reported unavailable diagnostic count %q: %s", unavailable, stderr.String())
		}
	}
}

func TestPlaywrightScriptReportsUnavailableDiagnosticsWhenEvaluationFails(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
	let titleReadyCalls = 0;
	let diagnosticsMayFail = false;
	exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async () => {},
      goto: async () => {},
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForTimeout: async () => {},
      waitForFunction: async () => { throw new TimeoutError('timeout'); },
      evaluate: async (fn) => {
        if (String(fn).includes('return { titleReady')) {
          if (diagnosticsMayFail) throw new Error('execution context destroyed');
          return { titleReady: false, captchaText: false, captchaChallengeCount: 0, continuationCandidates: 0 };
        }
        if (String(fn).includes("querySelectorAll('#productTitle')")) {
          titleReadyCalls++;
          if (titleReadyCalls >= 3) diagnosticsMayFail = true;
        }
        return false;
      },
      content: async () => '<html><body>unknown</body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	_, stderr, err := runPlaywrightScriptWithFake(t, fakePlaywright)
	if err == nil {
		t.Fatalf("expected missing title to fail")
	}
	for _, want := range []string{
		"amazon product page did not expose product title",
		"diagnostics_available=false",
		"diagnostics_error=evaluate_failed",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr missing unavailable diagnostic %q: %s", want, stderr.String())
		}
	}
	for _, unavailable := range []string{
		"captcha_challenge_count=0",
		"continuation_candidates=0",
		"title_ready=false",
	} {
		if strings.Contains(stderr.String(), unavailable) {
			t.Fatalf("stderr reported unavailable diagnostic count %q: %s", unavailable, stderr.String())
		}
	}
}

func TestPlaywrightScriptTreatsContinuationPrecheckErrorAsOptional(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
let threwProductTitleReady = false;
function withDocument(fn) {
  const previousDocument = global.document;
  global.document = {
    querySelectorAll: (selector) => selector === '#productTitle' ? [{ value: '', textContent: ' Echo Dot ' }] : [],
    querySelector: () => null,
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async () => {},
      goto: async () => {},
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        if (selector.includes('Continue Shopping')) {
          return { count: async () => 0, first: () => ({ click: async () => {} }) };
        }
        throw new Error('unexpected selector ' + selector);
      },
      waitForFunction: async (fn) => withDocument(fn),
      evaluate: async (fn) => {
        if (String(fn).includes('return { titleReady')) return withDocument(fn);
        if (!threwProductTitleReady && String(fn).includes("querySelectorAll('#productTitle')")) {
          threwProductTitleReady = true;
          throw new Error('Execution context was destroyed');
        }
        return withDocument(fn);
      },
      content: async () => '<html><body><span id="productTitle">Echo Dot</span></body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	stdout, stderr, err := runPlaywrightScriptWithFake(t, fakePlaywright)
	if err != nil {
		t.Fatalf("capture script should treat continuation precheck errors as optional: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `id="productTitle"`) {
		t.Fatalf("capture script did not continue to title wait: %s", stdout.String())
	}
}

func TestPlaywrightScriptTreatsContinuationClickTimeoutAsOptional(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
let titleReady = false;
const attrs = {};
const continuationNode = {
  value: 'Continue Shopping',
  textContent: '',
  getAttribute: (name) => attrs[name] || '',
  setAttribute: (name, value) => { attrs[name] = value; },
  removeAttribute: (name) => { delete attrs[name]; },
};
function withDocument(fn) {
  const previousDocument = global.document;
  global.document = {
    body: { textContent: ' Continue Shopping ' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return titleReady ? [{ value: '', textContent: ' Echo Dot ' }] : [];
      if (selector === '[data-product-capture-continuation-candidate]') return attrs['data-product-capture-continuation-candidate'] ? [continuationNode] : [];
      if (selector === 'button,input[type="submit"],input[type="button"],a,[role="button"]') return [continuationNode];
      return [];
    },
    querySelector: () => null,
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async () => {},
      goto: async () => {},
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        if (selector === '[data-product-capture-continuation-candidate="true"]') {
          return {
            count: async () => attrs['data-product-capture-continuation-candidate'] === 'true' ? 1 : 0,
            first: () => ({
              click: async () => {
                titleReady = true;
                throw new TimeoutError('Timeout 5000ms exceeded');
              },
            }),
            nth: () => ({
              click: async () => {
                titleReady = true;
                throw new TimeoutError('Timeout 5000ms exceeded');
              },
            }),
          };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForFunction: async (fn) => withDocument(fn),
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><body><span id="productTitle">Echo Dot</span></body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	stdout, stderr, err := runPlaywrightScriptWithFake(t, fakePlaywright)
	if err != nil {
		t.Fatalf("capture script should treat continuation click timeouts as optional: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `id="productTitle"`) {
		t.Fatalf("capture script did not continue to title wait: %s", stdout.String())
	}
}

func TestPlaywrightScriptStopsContinuationCandidateClicksAtDeadline(t *testing.T) {
	fakePlaywright := `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
let now = 0;
Date.now = () => now;
let secondClicked = false;
let titleReady = false;
const attrs = [{}, {}];
const continuationNodes = attrs.map((nodeAttrs) => ({
  value: 'Continue Shopping',
  textContent: '',
  getAttribute: (name) => nodeAttrs[name] || '',
  setAttribute: (name, value) => { nodeAttrs[name] = value; },
  removeAttribute: (name) => { delete nodeAttrs[name]; },
}));
function withDocument(fn) {
  const previousDocument = global.document;
  global.document = {
    body: { textContent: ' Continue Shopping ' },
    querySelectorAll: (selector) => {
      if (selector === '#productTitle') return titleReady ? [{ value: '', textContent: ' Echo Dot ' }] : [];
      if (selector === 'button,input[type="submit"],input[type="button"],a,[role="button"]') return continuationNodes;
      return [];
    },
    querySelector: () => null,
  };
  try {
    return fn();
  } finally {
    global.document = previousDocument;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      addInitScript: async () => {},
      goto: async () => {},
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') {
          return { count: async () => 0 };
        }
        if (selector === '[data-product-capture-continuation-candidate="true"]') {
          return {
            count: async () => attrs.filter((nodeAttrs) => nodeAttrs['data-product-capture-continuation-candidate'] === 'true').length,
            first: () => ({
              click: async () => {
                titleReady = true;
                now = 30000;
                throw new TimeoutError('first candidate consumed deadline');
              },
            }),
            nth: (index) => ({
              click: async () => {
                if (index === 1) {
                  secondClicked = true;
                  titleReady = true;
                }
              },
            }),
          };
        }
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForLoadState: async () => {},
      waitForFunction: async (fn) => withDocument(fn),
      evaluate: async (fn) => withDocument(fn),
      content: async () => titleReady ? '<html><body><span id="productTitle">Echo Dot</span></body></html>' : '<html><body>No title yet</body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
process.on('exit', () => {
  if (secondClicked) {
    console.error('second continuation candidate clicked after deadline');
    process.exitCode = 42;
  }
});
`
	stdout, stderr, err := runPlaywrightScriptWithFakeTimeout(t, fakePlaywright, "30000")
	if err != nil {
		t.Fatalf("capture script should stop continuation clicks at deadline without failing capture: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `id="productTitle"`) {
		t.Fatalf("capture script did not continue to title wait after deadline-bound click attempts: %s", stdout.String())
	}
}

func runPlaywrightScriptWithFake(t *testing.T, fakePlaywright string) (bytes.Buffer, bytes.Buffer, error) {
	return runPlaywrightScriptWithFakeURLTimeout(t, fakePlaywright, "https://www.amazon.com/dp/B08H75RTZ8", "30000")
}

func runPlaywrightScriptWithFakeURL(t *testing.T, fakePlaywright, targetURL string) (bytes.Buffer, bytes.Buffer, error) {
	return runPlaywrightScriptWithFakeURLTimeout(t, fakePlaywright, targetURL, "30000")
}

func runPlaywrightScriptWithFakeTimeout(t *testing.T, fakePlaywright string, timeout string) (bytes.Buffer, bytes.Buffer, error) {
	return runPlaywrightScriptWithFakeURLTimeout(t, fakePlaywright, "https://www.amazon.com/dp/B08H75RTZ8", timeout)
}

func runPlaywrightScriptWithFakeURLTimeout(t *testing.T, fakePlaywright string, targetURL string, timeout string) (bytes.Buffer, bytes.Buffer, error) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not installed; CI provisions Node for generated Playwright script regressions: %v", err)
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "capture.js")
	if err := os.WriteFile(script, []byte(playwrightCaptureScript), 0o600); err != nil {
		t.Fatal(err)
	}
	moduleDir := filepath.Join(dir, "node_modules", "playwright")
	if err := os.MkdirAll(moduleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "index.js"), []byte(fakePlaywright), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", script, targetURL, timeout)
	cmd.Env = withoutNodeOverrides(os.Environ())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout, stderr, err
}

func withoutNodeOverrides(env []string) []string {
	out := env[:0]
	for _, item := range env {
		if strings.HasPrefix(item, "NODE_OPTIONS=") || strings.HasPrefix(item, "NODE_PATH=") {
			continue
		}
		out = append(out, item)
	}
	return out
}

func TestPlaywrightScriptRetriesTransientNavigationFailures(t *testing.T) {
	for _, required := range []string{
		"isTransientNavigationError",
		"'Timeout',",
		"net::ERR_NETWORK_CHANGED",
		"net::ERR_NETWORK_RESET",
		"net::ERR_TIMED_OUT",
		"for (let attempt = 0; attempt < 3 && remainingTimeout(deadline) > 0; attempt++)",
		"if (budget <= 0) break;",
		"await page.goto(url, { waitUntil: 'commit', timeout });",
		"String(err)",
	} {
		if !strings.Contains(playwrightCaptureScript, required) {
			t.Fatalf("playwright script must retry transient navigation failure path %q", required)
		}
	}
	if strings.Contains(playwrightCaptureScript, "waitUntil: 'domcontentloaded'") {
		t.Fatalf("playwright script should not make Amazon DOMContentLoaded the primary navigation gate")
	}
	retryIndex := strings.Index(playwrightCaptureScript, "await gotoWithTransientRetry(page, url, deadline);")
	captchaIndex := -1
	if retryIndex >= 0 {
		afterRetry := playwrightCaptureScript[retryIndex:]
		if relative := strings.Index(afterRetry, "if (await hasAmazonInterstitial(page))"); relative >= 0 {
			captchaIndex = retryIndex + relative
		}
	}
	if retryIndex < 0 || captchaIndex < 0 || captchaIndex < retryIndex {
		t.Fatal("playwright script must check CAPTCHA/interstitials after retryable navigation only")
	}
}

func TestPlaywrightScriptRetriesPlainNavigationTimeoutWithinBudget(t *testing.T) {
	for _, required := range []string{
		"'Timeout',",
		"productTitleReady(page)",
		"waitForProductTitle(page, deadline)",
		"if (timeout <= 0) return await safeProductTitleReady(page)",
		"const titleWait = Math.min(remainingTimeout(deadline), 15000)",
		"await waitForProductTitle(page, Date.now() + titleWait)",
		"remainingTimeout(deadline",
		"Math.floor(budget * 0.65)",
		"if (loadTimeout > 0) await page.waitForLoadState('domcontentloaded'",
		"navigation timed out before capture started",
	} {
		if !strings.Contains(playwrightCaptureScript, required) {
			t.Fatalf("playwright script should retry plain navigation timeouts within one capture budget; missing %q", required)
		}
	}
	if strings.Contains(playwrightCaptureScript, "for (let attempt = 1; attempt <= 3; attempt++)") {
		t.Fatalf("playwright script should not spend the full timeout on each retry")
	}
	if strings.Contains(playwrightCaptureScript, "return Math.max(floor, deadline - Date.now())") {
		t.Fatalf("playwright script should not extend expired capture budgets with a timeout floor")
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %v, want %v", path, got, want)
	}
}
