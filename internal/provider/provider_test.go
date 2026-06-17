package provider

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
	stdout, stderr, err := runPlaywrightScriptWithFake(t, fakePlaywright)
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
  const metaNodes = [{ getAttribute: (name) => name === 'content' ? 'Echo Dot (5th Gen)' : '' }];
  global.document = {
    querySelectorAll: (selector) => selector === '#productTitle' ? [] : [],
    querySelector: (selector) => selector === 'meta[property="og:title"]' ? metaNodes[0] : null,
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
      waitForFunction: async (fn) => withDocument(fn),
      evaluate: async (fn) => withDocument(fn),
      content: async () => '<html><head><link rel="canonical" href="https://www.amazon.com/dp/B08N5WRWNW"><meta property="og:title" content="Echo Dot (5th Gen)"></head><body><img id="landingImage" src="https://m.media-amazon.com/images/I/echo.jpg"></body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
	stdout, stderr, err := runPlaywrightScriptWithFake(t, fakePlaywright)
	if err != nil {
		t.Fatalf("capture script failed with metadata title evidence: %v\nstderr=%s", err, stderr.String())
	}
	snap, err := snapshot.ExtractAmazon(stdout.String(), snapshot.ExtractOptions{URL: "https://www.amazon.com/dp/B08N5WRWNW"})
	if err != nil {
		t.Fatalf("captured html should remain extractable: %v", err)
	}
	if snap.Title != "Echo Dot (5th Gen)" {
		t.Fatalf("title: %q", snap.Title)
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
      evaluate: async () => true,
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

func runPlaywrightScriptWithFake(t *testing.T, fakePlaywright string) (bytes.Buffer, bytes.Buffer, error) {
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
	cmd := exec.Command("node", script, "https://www.amazon.com/dp/B08H75RTZ8", "30000")
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
		"await page.goto(url, { waitUntil: 'domcontentloaded', timeout });",
		"String(err)",
	} {
		if !strings.Contains(playwrightCaptureScript, required) {
			t.Fatalf("playwright script must retry transient navigation failure path %q", required)
		}
	}
	retryIndex := strings.Index(playwrightCaptureScript, "await gotoWithTransientRetry(page, url, deadline);")
	captchaIndex := strings.Index(playwrightCaptureScript, "if (await hasAmazonInterstitial(page))")
	if retryIndex < 0 || captchaIndex < 0 || captchaIndex < retryIndex {
		t.Fatal("playwright script must check CAPTCHA/interstitials after retryable navigation only")
	}
}

func TestPlaywrightScriptRetriesPlainNavigationTimeoutWithinBudget(t *testing.T) {
	for _, required := range []string{
		"'Timeout',",
		"productTitleReady(page)",
		"waitForProductTitle(page, deadline)",
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
