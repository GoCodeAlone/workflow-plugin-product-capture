package provider

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		Provider string   `json:"provider"`
		Title    string   `json:"title"`
		Price    string   `json:"price"`
		Images   []string `json:"images,omitempty"`
		RawHTML  string   `json:"raw_html,omitempty"`
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
	if len(got.Images) > 4 {
		t.Fatalf("max_image_count ignored: %d", len(got.Images))
	}
	if got.RawHTML != "" {
		t.Fatalf("raw html leaked")
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
		    "config_digest":"sha256:optional"
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
	var got struct {
		Title  string   `json:"title"`
		Images []string `json:"images,omitempty"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode product artifact: %v", err)
	}
	if !strings.Contains(got.Title, "Xbox Series X") {
		t.Fatalf("title: %q", got.Title)
	}
	if len(got.Images) > 2 {
		t.Fatalf("max_image_count ignored: %d", len(got.Images))
	}
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
	  "provider_config":{"plugin_id":"workflow-plugin-product-capture","provider_id":"browser","contract_id":"product-capture.browser.v1","version":"v1.0.0"},
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

func TestPlaywrightScriptDoesNotAutomateInterstitialOrAdvertiseStealth(t *testing.T) {
	for _, disallowed := range []string{
		"Continue shopping",
		"button.click",
		"stealth",
		"HeadlessChrome",
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

func TestPlaywrightScriptWaitsForVisibleProductTitleSpan(t *testing.T) {
	if strings.Contains(playwrightCaptureScript, "locator('#productTitle').waitFor") {
		t.Fatalf("playwright script uses strict #productTitle locator; Amazon may render a visible span and hidden input with that id")
	}
	if !strings.Contains(playwrightCaptureScript, "waitForFunction") || !strings.Contains(playwrightCaptureScript, "input#productTitle") {
		t.Fatalf("playwright script should wait for either visible product title text or hidden title input value")
	}
}

func TestPlaywrightScriptWaitsForCaptureRelevantNodes(t *testing.T) {
	for _, required := range []string{
		"optionalWait",
		"#landingImage",
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

func TestPlaywrightScriptRetriesTransientNavigationFailures(t *testing.T) {
	for _, required := range []string{
		"isTransientNavigationError",
		"net::ERR_NETWORK_CHANGED",
		"net::ERR_NETWORK_RESET",
		"net::ERR_TIMED_OUT",
		"for (let attempt = 1; attempt <= 3; attempt++)",
		"await page.goto(url, { waitUntil: 'domcontentloaded', timeout });",
		"String(err)",
	} {
		if !strings.Contains(playwrightCaptureScript, required) {
			t.Fatalf("playwright script must retry transient navigation failure path %q", required)
		}
	}
	retryIndex := strings.Index(playwrightCaptureScript, "await gotoWithTransientRetry(page, url, timeout);")
	captchaIndex := strings.Index(playwrightCaptureScript, `form[action*="/errors/validateCaptcha"]`)
	if retryIndex < 0 || captchaIndex < 0 || captchaIndex < retryIndex {
		t.Fatal("playwright script must check CAPTCHA/interstitials after retryable navigation only")
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
