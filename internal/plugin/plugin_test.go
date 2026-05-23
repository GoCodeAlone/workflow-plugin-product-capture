package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/GoCodeAlone/workflow-compute/pkg/protocol"
)

const testProviderImageRef = "ghcr.io/gocodealone/workflow-plugin-product-capture/product-capture-browser@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestManifest(t *testing.T) {
	manifest := NewPlugin().Manifest()
	if manifest.Name != "workflow-plugin-product-capture" {
		t.Fatalf("manifest name: %q", manifest.Name)
	}
}

func TestStepTypes(t *testing.T) {
	steps := NewPlugin().(interface{ StepTypes() []string })
	got := steps.StepTypes()
	want := []string{"step.product_capture"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("step types: got %#v", got)
	}
}

func TestPluginManifestStepTypesMatchRuntime(t *testing.T) {
	data, err := os.ReadFile("../../plugin.json")
	if err != nil {
		t.Fatalf("read plugin manifest: %v", err)
	}
	var manifest struct {
		Capabilities struct {
			StepTypes []string `json:"stepTypes"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode plugin manifest: %v", err)
	}
	steps := NewPlugin().(interface{ StepTypes() []string })
	if strings.Join(manifest.Capabilities.StepTypes, ",") != strings.Join(steps.StepTypes(), ",") {
		t.Fatalf("manifest step types %v do not match runtime %v", manifest.Capabilities.StepTypes, steps.StepTypes())
	}
}

func TestProductCaptureStepDispatchesDynamicURLAndReturnsPreview(t *testing.T) {
	var submitted protocol.Task
	var taskCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/tasks":
			if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
				t.Fatalf("decode task: %v", err)
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"task": submitted})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks":
			taskCalls++
			status := protocol.TaskQueued
			if taskCalls > 1 {
				status = protocol.TaskSucceeded
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tasks": []protocol.Task{{
				ID:     submitted.ID,
				OrgID:  submitted.OrgID,
				PoolID: submitted.PoolID,
				Status: status,
			}}, "stalls": []any{}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/proofs":
			proof := proofReceipt(submitted.ID)
			proof.ResultPreview = map[string]any{
				"title":          "Xbox Series X",
				"seller":         "Sole Providers",
				"prime_eligible": false,
				"error":          "diagnostic only",
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"proofs": []protocol.ProofReceipt{proof}})
		default:
			t.Fatalf("request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	step, err := newProductCaptureStep("capture", map[string]any{
		"server_url":              srv.URL,
		"auth_token_ref":          "secret:compute-token",
		"id":                      "capture-1",
		"product_id":              "wishlist-product-capture",
		"org_id":                  "org-1",
		"pool_id":                 "pool-1",
		"policy_id":               "policy-1",
		"timeout_seconds":         90,
		"url_field":               "url",
		"allowed_hosts":           []any{"www.amazon.com", "amazon.com"},
		"provider_image_ref":      testProviderImageRef,
		"capture_timeout_seconds": 45,
		"max_html_bytes":          1 << 20,
		"max_image_count":         8,
		"poll_interval":           "1ms",
		"wait_timeout":            "100ms",
	})
	if err != nil {
		t.Fatalf("newProductCaptureStep: %v", err)
	}
	result, err := step.Execute(context.Background(), nil, nil, map[string]any{
		"url": "https://www.amazon.com/dp/B0DL7CKRJ5?th=1",
	}, nil, runtimeSecrets())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.StopPipeline {
		t.Fatalf("unexpected stop: %+v", result.Output)
	}
	if submitted.ProductID != "wishlist-product-capture" {
		t.Fatalf("submitted product id: %+v", submitted)
	}
	if submitted.Workload.Kind != protocol.WorkloadProvider || submitted.Workload.Provider == nil {
		t.Fatalf("submitted workload: %+v", submitted.Workload)
	}
	if submitted.Workload.Provider.ProviderConfig != productCaptureProviderConfig("wishlist-product-capture") {
		t.Fatalf("provider config: %+v", submitted.Workload.Provider.ProviderConfig)
	}
	if submitted.Workload.Provider.Operation != "capture_product" {
		t.Fatalf("operation: %q", submitted.Workload.Provider.Operation)
	}
	if submitted.Workload.Provider.ImageRef != testProviderImageRef {
		t.Fatalf("image ref: %q", submitted.Workload.Provider.ImageRef)
	}
	if !strings.Contains(string(submitted.Workload.Provider.Input), `"url":"https://www.amazon.com/dp/B0DL7CKRJ5?th=1"`) {
		t.Fatalf("provider input: %s", submitted.Workload.Provider.Input)
	}
	if result.Output["title"] != "Xbox Series X" || result.Output["seller"] != "Sole Providers" || result.Output["prime_eligible"] != false {
		t.Fatalf("preview output: %+v", result.Output)
	}
	if result.Output["error"] != nil {
		t.Fatalf("preview error key should not be promoted: %+v", result.Output)
	}
}

func TestProductCaptureStepRejectsUnknownConfig(t *testing.T) {
	cfg := productCaptureConfigMap("https://compute.example.test")
	cfg["url_field"] = "url"
	cfg["allowed_hosts"] = []any{"www.amazon.com"}
	cfg["provider_image_ref"] = testProviderImageRef
	cfg["unknown"] = true
	if _, err := newProductCaptureStep("capture", cfg); err == nil {
		t.Fatal("expected strict unknown-field error")
	}
}

func TestProductCaptureStepAcceptsWorkflowInternalConfigDir(t *testing.T) {
	cfg := productCaptureConfigMap("https://compute.example.test")
	cfg["url_field"] = "url"
	cfg["allowed_hosts"] = []any{"www.amazon.com"}
	cfg["provider_image_ref"] = testProviderImageRef
	cfg["_config_dir"] = "/app"
	if _, err := newProductCaptureStep("capture", cfg); err != nil {
		t.Fatalf("expected Workflow-injected _config_dir to be accepted: %v", err)
	}
}

func productCaptureConfigMap(serverURL string) map[string]any {
	return map[string]any{
		"server_url":         serverURL,
		"auth_token_ref":     "secret:compute-token",
		"id":                 "capture-1",
		"product_id":         "wishlist-product-capture",
		"org_id":             "org-1",
		"pool_id":            "pool-1",
		"policy_id":          "policy-1",
		"timeout_seconds":    60,
		"url":                "https://www.amazon.com/dp/B0DL7CKRJ5",
		"allowed_hosts":      []any{"www.amazon.com"},
		"provider_image_ref": testProviderImageRef,
	}
}

func productCaptureProviderConfig(productID string) protocol.ProviderConfig {
	return protocol.ProviderConfig{
		PluginID:   "workflow-plugin-product-capture",
		ProviderID: "browser",
		ContractID: "product-capture.browser.v1",
		Version:    "v1.0.0",
		ConfigRef:  "config://network-products/" + productID + "/browser",
	}
}

func runtimeSecrets() map[string]any {
	return map[string]any{
		"secrets": map[string]any{
			"compute-token": "token",
		},
	}
}

func proofReceipt(taskID string) protocol.ProofReceipt {
	return protocol.ProofReceipt{
		ID:           "proof-1",
		TaskID:       taskID,
		WorkerID:     "worker-1",
		ArtifactHash: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Verifier: protocol.VerifierResult{
			Status:   protocol.VerificationAccepted,
			Provider: "artifact-hash",
		},
	}
}
