package stagingproof

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-compute-core/protocol"
)

const (
	testImageDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testImageRef    = "ghcr.io/gocodealone/workflow-plugin-product-capture/product-capture-browser@" + testImageDigest
)

func TestRunCompletesGenericProductCaptureRoundTrip(t *testing.T) {
	fixture := newComputeFixture(t)
	server := httptest.NewServer(fixture)
	t.Cleanup(server.Close)

	cfg := testConfig(t, server.URL)
	summary, err := Run(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if summary.SchemaVersion != "product-capture-staging-proof.v1" ||
		summary.TaskStatus != protocol.TaskSucceeded ||
		summary.ProofStatus != protocol.VerificationAccepted ||
		summary.WorkerID != cfg.WorkerID ||
		summary.ProviderImageRef != testImageRef {
		t.Fatalf("summary identity = %+v", summary)
	}
	if summary.Capacity.MatchingOnlineAgents != 1 ||
		summary.Capacity.ActiveMatchingLeases != 0 ||
		summary.Capacity.QueuedMatchingTasks != 0 ||
		summary.Capacity.WorkerStatus != protocol.AgentOnline ||
		!strings.HasPrefix(summary.Capacity.Digest, "sha256:") {
		t.Fatalf("capacity = %+v", summary.Capacity)
	}
	if summary.Product.Title != "Example product" ||
		summary.Product.ImageURL != "https://images.example.test/product.jpg" ||
		summary.Product.Price != "19.99" ||
		summary.Product.Currency != "USD" {
		t.Fatalf("product = %+v", summary.Product)
	}
	if len(summary.Artifacts) != 1 || summary.Artifacts[0].Name != "product_json" ||
		summary.Artifacts[0].SHA256 != fixture.productSHA256() {
		t.Fatalf("artifacts = %+v", summary.Artifacts)
	}
	if summary.BrowserDiagnostic != nil {
		t.Fatalf("browser diagnostic = %+v, want nil", summary.BrowserDiagnostic)
	}

	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.submitted) != 1 {
		t.Fatalf("submitted tasks = %d, want 1", len(fixture.submitted))
	}
	submitted := fixture.submitted[0]
	if submitted.Workload.Kind != protocol.WorkloadProvider || submitted.Workload.Provider == nil {
		t.Fatalf("submitted workload = %+v", submitted.Workload)
	}
	provider := submitted.Workload.Provider
	if provider.Operation != "capture_product" || provider.ImageRef != testImageRef ||
		provider.ComponentRef != "" || provider.ComponentDigest != "" {
		t.Fatalf("provider workload = %+v", provider)
	}
	if submitted.Requirements.ExecutorProvider != "product-capture-browser" ||
		submitted.Requirements.ExecutionSecurityTier != protocol.ExecutionSandboxedContainer ||
		submitted.Requirements.ProofTier != protocol.ProofArtifactHash {
		t.Fatalf("requirements = %+v", submitted.Requirements)
	}
	var input map[string]any
	if err := json.Unmarshal(provider.Input, &input); err != nil {
		t.Fatalf("decode provider input: %v", err)
	}
	if input["url"] != cfg.ProductURL || !slices.Equal(input["allowed_hosts"].([]any), []any{cfg.AllowedHost}) {
		t.Fatalf("provider input = %#v", input)
	}

	allowed := map[string]bool{
		"GET /v1/agents": true,
		"GET /v1/leases": true,
		"GET /v1/tasks":  true,
		"POST /v1/tasks": true,
		"GET /v1/proofs": true,
		"GET /v1/tasks/" + submitted.ID + "/artifacts":                                                true,
		"GET /v1/tasks/" + submitted.ID + "/proofs/proof-" + submitted.ID + "/artifacts/product_json": true,
	}
	for _, request := range fixture.requests {
		if !allowed[request] {
			t.Fatalf("unexpected control-plane request %q", request)
		}
	}

	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	for _, secret := range []string{cfg.Token, cfg.ServerURL, cfg.ProductURL} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("summary leaked redacted input %q: %s", secret, encoded)
		}
	}
}

func TestRunMatchesNetworkProductDirectModeCanonicalization(t *testing.T) {
	fixture := newComputeFixture(t)
	fixture.mutateSubmittedResponse = func(task *protocol.Task) {
		task.NetworkPolicy.Mode = protocol.NetworkModeDirect
	}
	server := httptest.NewServer(fixture)
	t.Cleanup(server.Close)
	cfg := testConfig(t, server.URL)
	cfg.CapacityTimeout = 5 * time.Second
	cfg.ResultTimeout = 5 * time.Second
	cfg.ArtifactTimeout = 5 * time.Second

	if _, err := Run(t.Context(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.submitted) != 1 {
		t.Fatalf("submitted tasks = %d, want 1", len(fixture.submitted))
	}
	if fixture.submitted[0].NetworkPolicy.Mode != protocol.NetworkModeDirect {
		t.Fatalf("submitted network mode = %q, want %q", fixture.submitted[0].NetworkPolicy.Mode, protocol.NetworkModeDirect)
	}
}

func TestRunSeparatesSandboxProvenanceFromProviderImage(t *testing.T) {
	fixture := newComputeFixture(t)
	sandboxImageDigest := "sha256:" + strings.Repeat("c", 64)
	fixture.agents[0].Capabilities.Executors[0].ImageDigest = sandboxImageDigest
	fixture.mutateProof = func(proof *protocol.ProofReceipt) {
		proof.Executor.ImageDigest = sandboxImageDigest
		proof.AgentSignature = protocol.SoftwareAgentProofSignature(*proof)
	}
	server := httptest.NewServer(fixture)
	t.Cleanup(server.Close)
	cfg := testConfig(t, server.URL)
	if _, ok := compatibleExecutor(fixture.agents[0], cfg); !ok {
		t.Fatal("valid sandbox executor rejected because its image digest differs from the provider workload image")
	}

	summary, err := Run(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.ProviderImageRef != testImageRef {
		t.Fatalf("provider image ref = %q, want %q", summary.ProviderImageRef, testImageRef)
	}
	if summary.Executor.ImageDigest != sandboxImageDigest {
		t.Fatalf("sandbox image digest = %q, want %q", summary.Executor.ImageDigest, sandboxImageDigest)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.submitted) != 1 || fixture.submitted[0].Workload.Provider == nil ||
		fixture.submitted[0].Workload.Provider.ImageRef != testImageRef {
		t.Fatalf("submitted provider workload = %+v, want image %q", fixture.submitted, testImageRef)
	}
}

func TestRunCapsProviderAndComputeTaskTimeoutTogether(t *testing.T) {
	fixture := newComputeFixture(t)
	server := httptest.NewServer(fixture)
	t.Cleanup(server.Close)
	cfg := testConfig(t, server.URL)
	cfg.TaskTimeoutSeconds = 600

	if _, err := Run(t.Context(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.submitted) != 1 {
		t.Fatalf("submitted tasks = %d, want 1", len(fixture.submitted))
	}
	submitted := fixture.submitted[0]
	if submitted.TimeoutSeconds != 300 {
		t.Fatalf("compute task timeout = %d, want 300", submitted.TimeoutSeconds)
	}
	var input struct {
		TimeoutSeconds int `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(submitted.Workload.Provider.Input, &input); err != nil {
		t.Fatalf("decode provider input: %v", err)
	}
	if input.TimeoutSeconds != submitted.TimeoutSeconds {
		t.Fatalf("provider timeout = %d, compute task timeout = %d", input.TimeoutSeconds, submitted.TimeoutSeconds)
	}
}

func TestRunRejectsInvalidImageReferencesBeforeNetworkAccess(t *testing.T) {
	for _, ref := range []string{
		"ghcr.io/gocodealone/product-capture:latest",
		"ghcr.io/gocodealone/product-capture@sha256:short",
		"ghcr.io/gocodealone/product-capture@" + testImageDigest + " extra",
	} {
		t.Run(ref, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
			t.Cleanup(server.Close)
			cfg := testConfig(t, server.URL)
			cfg.ProviderImageRef = ref
			if _, err := Run(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "digest-pinned") {
				t.Fatalf("Run error = %v, want digest-pinned rejection", err)
			}
			if calls != 0 {
				t.Fatalf("network calls = %d, want 0", calls)
			}
		})
	}
}

func TestRunRejectsProductURLWithoutAmazonASINBeforeNetworkAccess(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	t.Cleanup(server.Close)
	cfg := testConfig(t, server.URL)
	cfg.ProductURL = "https://www.amazon.com/"
	if _, err := Run(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "ASIN") {
		t.Fatalf("Run error = %v, want ASIN rejection", err)
	}
	if calls != 0 {
		t.Fatalf("network calls = %d, want 0", calls)
	}
}

func TestRunWaitsForExactIdleRetainedCapacity(t *testing.T) {
	tests := map[string]func(*computeFixture){
		"missing retained worker": func(f *computeFixture) { f.agents = nil },
		"multiple compatible workers": func(f *computeFixture) {
			other := f.agents[0]
			other.ID = "worker-2"
			f.agents = append(f.agents, other)
		},
		"different compatible worker": func(f *computeFixture) {
			other := f.agents[0]
			other.ID = "worker-2"
			other.Capabilities.Executors = append([]protocol.ExecutorRef(nil), other.Capabilities.Executors...)
			f.agents[0].Capabilities.Executors[0].RootFSDigest = ""
			f.agents = append(f.agents, other)
		},
		"active retained lease": func(f *computeFixture) {
			f.leases = []protocol.Lease{{WorkerID: "worker-1", ExpiresAt: time.Now().Add(time.Hour)}}
		},
		"queued product task": func(f *computeFixture) {
			f.queued = []protocol.Task{{
				ID: "task-already-queued", ProductID: "bmw-product-capture", OrgID: "org-1", PoolID: "pool-1",
				Status: protocol.TaskQueued, Workload: protocol.WorkloadSpec{Kind: protocol.WorkloadProvider},
			}}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newComputeFixture(t)
			mutate(fixture)
			server := httptest.NewServer(fixture)
			t.Cleanup(server.Close)
			cfg := testConfig(t, server.URL)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fixture.afterCapacity = cancel
			if _, err := Run(ctx, cfg); err == nil || !strings.Contains(err.Error(), "capacity") {
				t.Fatalf("Run error = %v, want capacity rejection", err)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if len(fixture.submitted) != 0 {
				t.Fatalf("submitted tasks = %d, want 0", len(fixture.submitted))
			}
		})
	}
}

func TestRunRejectsInvalidProductArtifacts(t *testing.T) {
	valid := productJSON()
	tests := map[string]func(*computeFixture){
		"undeclared name":        func(f *computeFixture) { f.artifactName = "unexpected_json" },
		"wrong content type":     func(f *computeFixture) { f.artifactContentType = "text/plain" },
		"oversized metadata":     func(f *computeFixture) { f.artifactSize = (1 << 20) + 1 },
		"metadata size mismatch": func(f *computeFixture) { f.artifactSize = int64(len(valid) - 1) },
		"oversized body": func(f *computeFixture) {
			f.productBody = append(valid, make([]byte, (1<<20)-len(valid)+1)...)
			f.artifactSize = int64(len(valid))
		},
		"invalid json": func(f *computeFixture) { f.productBody = []byte(`{"provider":`) },
		"schema invalid": func(f *computeFixture) {
			f.productBody = []byte(`{"provider":"amazon","url":"https://www.amazon.com/dp/B000000000","requested_url":"https://www.amazon.com/dp/B000000000"}`)
		},
		"invalid captured at": func(f *computeFixture) {
			setProductField(f, "captured_at", "not-a-date")
		},
		"invalid secondary image url": func(f *computeFixture) {
			setProductField(f, "images", []string{"not a URI"})
		},
		"digest mismatch": func(f *computeFixture) { f.artifactSHA256 = "sha256:" + strings.Repeat("f", 64) },
		"wrong requested url": func(f *computeFixture) {
			setProductField(f, "requested_url", "https://www.amazon.com/dp/B111111111")
		},
		"wrong result host": func(f *computeFixture) {
			setProductField(f, "url", "https://example.test/dp/B000000000")
		},
		"wrong result asin": func(f *computeFixture) {
			setProductField(f, "url", "https://www.amazon.com/dp/B111111111")
		},
		"wrong canonical asin": func(f *computeFixture) {
			setProductField(f, "canonical_url", "https://www.amazon.com/dp/B111111111")
		},
		"wrong external id": func(f *computeFixture) { setProductField(f, "external_id", "B111111111") },
		"wrong provider":    func(f *computeFixture) { setProductField(f, "provider", "amazon") },
		"malformed price":   func(f *computeFixture) { setProductField(f, "price", "$19.99") },
		"missing currency":  func(f *computeFixture) { setProductField(f, "currency", "") },
		"credential image url": func(f *computeFixture) {
			setProductField(f, "image_url", "https://user:password@images.example.test/product.jpg")
		},
		"unbounded title": func(f *computeFixture) {
			var product map[string]any
			if err := json.Unmarshal(valid, &product); err != nil {
				t.Fatal(err)
			}
			product["title"] = strings.Repeat("x", 513)
			f.productBody, _ = json.Marshal(product)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newComputeFixture(t)
			mutate(fixture)
			server := httptest.NewServer(fixture)
			t.Cleanup(server.Close)
			if _, err := Run(t.Context(), testConfig(t, server.URL)); err == nil {
				t.Fatal("Run succeeded, want artifact rejection")
			}
		})
	}
}

func TestRunRejectsUnsuccessfulTaskOrProof(t *testing.T) {
	t.Run("task", func(t *testing.T) {
		fixture := newComputeFixture(t)
		fixture.taskStatus = protocol.TaskFailed
		server := httptest.NewServer(fixture)
		t.Cleanup(server.Close)
		if _, err := Run(t.Context(), testConfig(t, server.URL)); err == nil || !strings.Contains(err.Error(), "failed") {
			t.Fatalf("Run error = %v, want failed task", err)
		}
	})
	t.Run("proof", func(t *testing.T) {
		fixture := newComputeFixture(t)
		fixture.proofStatus = protocol.VerificationRejected
		server := httptest.NewServer(fixture)
		t.Cleanup(server.Close)
		if _, err := Run(t.Context(), testConfig(t, server.URL)); err == nil || !strings.Contains(err.Error(), "rejected") {
			t.Fatalf("Run error = %v, want rejected proof", err)
		}
	})
}

func TestRunRejectsMismatchedTerminalRequirements(t *testing.T) {
	fixture := newComputeFixture(t)
	fixture.mutateTerminalTask = func(task *protocol.Task) {
		task.Requirements.ExecutorProvider = "other-executor"
	}
	server := httptest.NewServer(fixture)
	t.Cleanup(server.Close)
	if _, err := Run(t.Context(), testConfig(t, server.URL)); err == nil || !strings.Contains(err.Error(), "terminal task") {
		t.Fatalf("Run error = %v, want terminal task rejection", err)
	}
}

func TestRunRejectsMismatchedSubmittedTaskFields(t *testing.T) {
	tests := map[string]func(*protocol.Task){
		"timeout":        func(task *protocol.Task) { task.TimeoutSeconds++ },
		"network policy": func(task *protocol.Task) { task.NetworkPolicy.Mode = protocol.NetworkModeRelay },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newComputeFixture(t)
			fixture.mutateSubmittedResponse = mutate
			server := httptest.NewServer(fixture)
			t.Cleanup(server.Close)
			if _, err := Run(t.Context(), testConfig(t, server.URL)); err == nil || !strings.Contains(err.Error(), "submitted task response") {
				t.Fatalf("Run error = %v, want submitted task response rejection", err)
			}
		})
	}
}

func TestRunRejectsMismatchedTerminalTaskFields(t *testing.T) {
	tests := map[string]func(*protocol.Task){
		"timeout":         func(task *protocol.Task) { task.TimeoutSeconds++ },
		"network policy":  func(task *protocol.Task) { task.NetworkPolicy.AuditDestinations = true },
		"proof policy":    func(task *protocol.Task) { task.ProofPolicy.Quorum = 2 },
		"access policy":   func(task *protocol.Task) { task.AccessPolicy.ArtifactVisibility = protocol.AccessVisibility("private") },
		"residue policy":  func(task *protocol.Task) { task.ResiduePolicy.MaxReuseCount = 1 },
		"resource limits": func(task *protocol.Task) { task.ResourceLimits.CPUPercent = 1 },
		"labels":          func(task *protocol.Task) { task.Labels = map[string]string{"mutated": "true"} },
		"requested time":  func(task *protocol.Task) { task.RequestedAt = task.RequestedAt.Add(time.Second) },
		"signature":       func(task *protocol.Task) { task.Signature.KeyID = "other-key" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newComputeFixture(t)
			fixture.mutateTerminalTask = mutate
			server := httptest.NewServer(fixture)
			t.Cleanup(server.Close)
			if _, err := Run(t.Context(), testConfig(t, server.URL)); err == nil || !strings.Contains(err.Error(), "terminal task") {
				t.Fatalf("Run error = %v, want terminal task rejection", err)
			}
		})
	}
}

func TestRunRejectsMismatchedProofBindings(t *testing.T) {
	tests := map[string]func(*protocol.ProofReceipt){
		"org":    func(proof *protocol.ProofReceipt) { proof.OrgID = "other-org" },
		"pool":   func(proof *protocol.ProofReceipt) { proof.PoolID = "other-pool" },
		"policy": func(proof *protocol.ProofReceipt) { proof.PolicyID = "other-policy" },
		"input":  func(proof *protocol.ProofReceipt) { proof.InputHash = sha256RefForTest([]byte("other-input")) },
		"task hash": func(proof *protocol.ProofReceipt) {
			proof.TaskHash = sha256RefForTest([]byte("other-task"))
		},
		"artifact hash": func(proof *protocol.ProofReceipt) {
			proof.ArtifactHash = sha256RefForTest([]byte("other-artifacts"))
		},
		"dependency": func(proof *protocol.ProofReceipt) {
			proof.DependencyClosureHash = sha256RefForTest([]byte("other-workload"))
		},
		"provider": func(proof *protocol.ProofReceipt) { proof.Executor.Provider = "other-executor" },
		"image": func(proof *protocol.ProofReceipt) {
			proof.Executor.ImageDigest = sha256RefForTest([]byte("other-image"))
		},
		"executor version": func(proof *protocol.ProofReceipt) { proof.Executor.Version = "v0.1.61" },
		"rootfs": func(proof *protocol.ProofReceipt) {
			proof.Executor.RootFSDigest = sha256RefForTest([]byte("other-rootfs"))
		},
		"execution": func(proof *protocol.ProofReceipt) {
			proof.Executor.ExecutionSecurityTier = protocol.ExecutionTrustedNative
		},
		"proof tier":   func(proof *protocol.ProofReceipt) { proof.Executor.ProofTier = protocol.ProofReceiptOnly },
		"missing time": func(proof *protocol.ProofReceipt) { proof.FinishedAt = time.Time{} },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newComputeFixture(t)
			fixture.mutateProof = mutate
			server := httptest.NewServer(fixture)
			t.Cleanup(server.Close)
			if _, err := Run(t.Context(), testConfig(t, server.URL)); err == nil {
				t.Fatal("Run succeeded, want proof binding rejection")
			}
		})
	}
}

func TestRunRequiresCompleteCapacityExecutorIdentity(t *testing.T) {
	fixture := newComputeFixture(t)
	fixture.agents[0].Capabilities.Executors[0].RootFSDigest = ""
	server := httptest.NewServer(fixture)
	t.Cleanup(server.Close)
	cfg := testConfig(t, server.URL)
	cfg.CapacityTimeout = 10 * time.Millisecond
	if _, err := Run(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "capacity") {
		t.Fatalf("Run error = %v, want incomplete executor capacity rejection", err)
	}
}

func TestRunRejectsMismatchedProductSchemaDigestBeforeNetworkAccess(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	t.Cleanup(server.Close)
	cfg := testConfig(t, server.URL)
	cfg.ProductSchema = append(cfg.ProductSchema, '\n')
	if _, err := Run(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "output_schema_digest") {
		t.Fatalf("Run error = %v, want schema digest rejection", err)
	}
	if calls != 0 {
		t.Fatalf("network calls = %d, want 0", calls)
	}
}

func TestRunRejectsMismatchedDiagnosticSchemaDigestBeforeNetworkAccess(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	t.Cleanup(server.Close)
	cfg := testConfig(t, server.URL)
	cfg.BrowserDiagnosticURL = "https://diagnostic.example.test/product-capture-browser"
	cfg.DiagnosticSchema = append(cfg.DiagnosticSchema, '\n')
	if _, err := Run(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "diagnostic schema digest") {
		t.Fatalf("Run error = %v, want diagnostic schema digest rejection", err)
	}
	if calls != 0 {
		t.Fatalf("network calls = %d, want 0", calls)
	}
}

func TestRunRejectsMismatchedDiagnosticContractDigestBeforeNetworkAccess(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	t.Cleanup(server.Close)
	cfg := testConfig(t, server.URL)
	cfg.BrowserDiagnosticURL = "https://diagnostic.example.test/product-capture-browser"
	for index := range cfg.Contract.Operations {
		if cfg.Contract.Operations[index].ID == "browser_diagnostic" {
			cfg.Contract.Operations[index].OutputSchemaDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		}
	}
	if _, err := Run(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "browser_diagnostic output_schema_digest") {
		t.Fatalf("Run error = %v, want diagnostic contract digest rejection", err)
	}
	if calls != 0 {
		t.Fatalf("network calls = %d, want 0", calls)
	}
}

func TestRunRejectsMismatchedDiagnosticContractReferenceBeforeNetworkAccess(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	t.Cleanup(server.Close)
	cfg := testConfig(t, server.URL)
	cfg.BrowserDiagnosticURL = "https://diagnostic.example.test/product-capture-browser"
	for index := range cfg.Contract.Operations {
		if cfg.Contract.Operations[index].ID == "browser_diagnostic" {
			cfg.Contract.Operations[index].OutputSchemaRef = "schema://providers/workflow-plugin-product-capture/browser/operations/browser_diagnostic/wrong/v1"
		}
	}
	if _, err := Run(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "browser_diagnostic output_schema_ref") {
		t.Fatalf("Run error = %v, want diagnostic contract reference rejection", err)
	}
	if calls != 0 {
		t.Fatalf("network calls = %d, want 0", calls)
	}
}

func TestRunRejectsMismatchedDiagnosticContractInputIdentityBeforeNetworkAccess(t *testing.T) {
	tests := map[string]func(*protocol.ProviderOperation){
		"reference": func(operation *protocol.ProviderOperation) {
			operation.InputSchemaRef = "schema://providers/workflow-plugin-product-capture/browser/operations/browser_diagnostic/wrong/v1"
		},
		"digest": func(operation *protocol.ProviderOperation) {
			operation.InputSchemaDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
			t.Cleanup(server.Close)
			cfg := testConfig(t, server.URL)
			cfg.BrowserDiagnosticURL = "https://diagnostic.example.test/product-capture-browser"
			for index := range cfg.Contract.Operations {
				if cfg.Contract.Operations[index].ID == "browser_diagnostic" {
					mutate(&cfg.Contract.Operations[index])
				}
			}
			if _, err := Run(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "browser_diagnostic input_schema") {
				t.Fatalf("Run error = %v, want diagnostic input contract rejection", err)
			}
			if calls != 0 {
				t.Fatalf("network calls = %d, want 0", calls)
			}
		})
	}
}

func TestRunRetriesTransientControlPlaneReads(t *testing.T) {
	fixture := newComputeFixture(t)
	fixture.transientFailures = map[string]int{
		"GET /v1/agents": 1,
		"GET /v1/leases": 1,
		"GET /v1/proofs": 1,
	}
	fixture.transientArtifactListFailures = 1
	fixture.transientArtifactDownloadFailures = 1
	server := httptest.NewServer(fixture)
	t.Cleanup(server.Close)
	if _, err := Run(t.Context(), testConfig(t, server.URL)); err != nil {
		t.Fatalf("Run after transient control-plane failures: %v", err)
	}
}

func TestRunBoundsTransientArtifactReads(t *testing.T) {
	fixture := newComputeFixture(t)
	fixture.transientArtifactListFailures = 1_000_000
	server := httptest.NewServer(fixture)
	t.Cleanup(server.Close)
	cfg := testConfig(t, server.URL)
	cfg.ArtifactTimeout = 10 * time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	started := time.Now()
	if _, err := Run(ctx, cfg); err == nil || !strings.Contains(err.Error(), "list task artifacts") || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run error = %v, want bounded transient artifact failure", err)
	}
	if elapsed := time.Since(started); elapsed >= 500*time.Millisecond {
		t.Fatalf("artifact timeout returned after %s, want before outer context deadline", elapsed)
	}
}

func TestRunDoesNotRetryPermanentControlPlaneFailures(t *testing.T) {
	fixture := newComputeFixture(t)
	fixture.permanentFailures = map[string]int{"GET /v1/agents": http.StatusForbidden}
	server := httptest.NewServer(fixture)
	t.Cleanup(server.Close)
	if _, err := Run(t.Context(), testConfig(t, server.URL)); err == nil || !strings.Contains(err.Error(), "status 403") {
		t.Fatalf("Run error = %v, want permanent authorization failure", err)
	}

	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if got := countRequests(fixture.requests, "GET /v1/agents"); got != 1 {
		t.Fatalf("agent requests = %d, want 1", got)
	}
}

func TestRunOptionallyCapturesAcceptedBrowserDiagnostic(t *testing.T) {
	fixture := newComputeFixture(t)
	server := httptest.NewServer(fixture)
	t.Cleanup(server.Close)
	cfg := testConfig(t, server.URL)
	cfg.BrowserDiagnosticURL = "https://diagnostic.example.test/product-capture-browser"

	summary, err := Run(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.BrowserDiagnostic == nil {
		t.Fatal("browser diagnostic is nil")
	}
	diagnostic := summary.BrowserDiagnostic
	if diagnostic.ProofStatus != protocol.VerificationAccepted ||
		diagnostic.SchemaSHA256 != browserDiagnosticSchemaSHA256 ||
		diagnostic.Artifact.Name != "browser_diagnostic_json" ||
		diagnostic.Artifact.ContentType != "application/json" ||
		diagnostic.Artifact.SizeBytes != int64(len(fixture.diagnosticBody)) {
		t.Fatalf("browser diagnostic = %+v", diagnostic)
	}

	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.submitted) != 2 {
		t.Fatalf("submitted tasks = %d, want 2", len(fixture.submitted))
	}
	if fixture.submitted[0].Workload.Provider.Operation != "capture_product" ||
		fixture.submitted[1].Workload.Provider.Operation != "browser_diagnostic" {
		t.Fatalf("submitted operations = %q, %q", fixture.submitted[0].Workload.Provider.Operation, fixture.submitted[1].Workload.Provider.Operation)
	}
	for _, task := range fixture.submitted {
		if task.NetworkPolicy.Mode != protocol.NetworkModeDirect {
			t.Fatalf("submitted %q network mode = %q, want %q", task.Workload.Provider.Operation, task.NetworkPolicy.Mode, protocol.NetworkModeDirect)
		}
	}
	var input map[string]any
	if err := json.Unmarshal(fixture.submitted[1].Workload.Provider.Input, &input); err != nil {
		t.Fatalf("decode diagnostic input: %v", err)
	}
	if input["url"] != cfg.BrowserDiagnosticURL {
		t.Fatalf("diagnostic input = %#v", input)
	}
}

func TestRunRejectsInvalidBrowserDiagnosticArtifacts(t *testing.T) {
	tests := map[string]func(*computeFixture){
		"empty object": func(f *computeFixture) { f.diagnosticBody = []byte(`{}`) },
		"wrong target url": func(f *computeFixture) {
			setDiagnosticField(f, "target_url", "https://diagnostic.example.test/other")
		},
		"cross-origin final url": func(f *computeFixture) {
			setDiagnosticField(f, "final_url", "https://other.example.test/product-capture-browser")
		},
		"post not accepted": func(f *computeFixture) { setDiagnosticField(f, "posted_to_origin", false) },
		"post error":        func(f *computeFixture) { setDiagnosticField(f, "post_error", "post failed") },
		"empty signals":     func(f *computeFixture) { setDiagnosticField(f, "browser_signals", map[string]any{}) },
		"empty navigator signals": func(f *computeFixture) {
			setDiagnosticSignalSection(f, "navigator", map[string]any{})
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newComputeFixture(t)
			mutate(fixture)
			server := httptest.NewServer(fixture)
			t.Cleanup(server.Close)
			cfg := testConfig(t, server.URL)
			cfg.BrowserDiagnosticURL = "https://diagnostic.example.test/product-capture-browser"
			if _, err := Run(t.Context(), cfg); err == nil {
				t.Fatal("Run succeeded, want browser diagnostic rejection")
			}
		})
	}
}

func TestRunRejectsUnknownBrowserDiagnosticSignal(t *testing.T) {
	fixture := newComputeFixture(t)
	setDiagnosticSignalField(fixture, "document", "cookie_value", "secret-cookie")
	server := httptest.NewServer(fixture)
	t.Cleanup(server.Close)
	cfg := testConfig(t, server.URL)
	cfg.BrowserDiagnosticURL = "https://diagnostic.example.test/product-capture-browser"
	if _, err := Run(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "violates schema") {
		t.Fatalf("Run error = %v, want unknown diagnostic signal schema rejection", err)
	}
}

func testConfig(t *testing.T, serverURL string) Config {
	t.Helper()
	contractData, err := os.ReadFile(filepath.Join("..", "..", "contracts", "product-capture-provider.json"))
	if err != nil {
		t.Fatalf("read contract: %v", err)
	}
	var contract protocol.ProviderContract
	if err := json.Unmarshal(contractData, &contract); err != nil {
		t.Fatalf("decode contract: %v", err)
	}
	productSchema, err := os.ReadFile(filepath.Join("..", "..", "schemas", "product-capture-operation-output.schema.json"))
	if err != nil {
		t.Fatalf("read product schema: %v", err)
	}
	diagnosticSchema, err := os.ReadFile(filepath.Join("..", "..", "schemas", "browser-diagnostic-result.schema.json"))
	if err != nil {
		t.Fatalf("read diagnostic schema: %v", err)
	}
	return Config{
		ServerURL:          serverURL,
		Token:              "scoped-task-token",
		OrgID:              "org-1",
		PoolID:             "pool-1",
		ProductID:          "bmw-product-capture",
		PolicyID:           "product-capture-staging",
		WorkerID:           "worker-1",
		ProductURL:         "https://www.amazon.com/dp/B000000000",
		AllowedHost:        "www.amazon.com",
		ProviderImageRef:   testImageRef,
		Contract:           contract,
		ProductSchema:      productSchema,
		DiagnosticSchema:   diagnosticSchema,
		PollInterval:       time.Millisecond,
		CapacityTimeout:    100 * time.Millisecond,
		ResultTimeout:      100 * time.Millisecond,
		ArtifactTimeout:    100 * time.Millisecond,
		TaskTimeoutSeconds: 120,
	}
}

type computeFixture struct {
	mu                                sync.Mutex
	requests                          []string
	submitted                         []protocol.Task
	agents                            []protocol.Agent
	leases                            []protocol.Lease
	queued                            []protocol.Task
	productBody                       []byte
	diagnosticBody                    []byte
	artifactName                      string
	artifactContentType               string
	artifactSize                      int64
	artifactSHA256                    string
	taskStatus                        protocol.TaskStatus
	proofStatus                       protocol.VerificationStatus
	afterCapacity                     func()
	transientFailures                 map[string]int
	permanentFailures                 map[string]int
	transientArtifactListFailures     int
	transientArtifactDownloadFailures int
	mutateProof                       func(*protocol.ProofReceipt)
	mutateTerminalTask                func(*protocol.Task)
	mutateSubmittedResponse           func(*protocol.Task)
	handlerErrors                     []error
}

func newComputeFixture(t *testing.T) *computeFixture {
	t.Helper()
	fixture := &computeFixture{
		agents: []protocol.Agent{{
			ID: "worker-1", OrgID: "org-1", PoolID: "pool-1", Status: protocol.AgentOnline,
			Capabilities: protocol.Capabilities{
				ExecutorProviders: []string{"product-capture-browser"},
				Executors: []protocol.ExecutorRef{{
					Provider: "product-capture-browser", Version: "v0.1.60",
					ExecutionSecurityTier: protocol.ExecutionSandboxedContainer,
					ProofTier:             protocol.ProofArtifactHash,
					ImageDigest:           testImageDigest,
					RootFSDigest:          sha256RefForTest([]byte("rootfs")),
				}},
				WorkloadKinds:  []string{string(protocol.WorkloadProvider)},
				ExecutionTiers: []protocol.ExecutionSecurityTier{protocol.ExecutionSandboxedContainer},
				ProofTiers:     []protocol.ProofTier{protocol.ProofArtifactHash},
			},
		}},
		productBody: productJSON(),
		diagnosticBody: []byte(`{
			"target_url":"https://diagnostic.example.test/product-capture-browser",
			"final_url":"https://diagnostic.example.test/product-capture-browser",
			"posted_to_origin":true,
			"post_error":"",
			"browser_signals":{
				"navigator":{
					"webdriver":false,
					"user_agent":"Chrome",
					"user_agent_data":{"brands":[{"brand":"Chromium","version":"140"}],"mobile":false,"platform":"Linux"},
					"user_agent_high_entropy":null,
					"language":"en-US",
					"languages":["en-US","en"],
					"platform":"Linux x86_64",
					"hardware_concurrency":8,
					"device_memory":8,
					"max_touch_points":0,
					"plugins":{"length":1,"names":["PDF Viewer"]},
					"mime_types":{"length":1,"names":["application/pdf"]}
				},
				"window":{
					"outer_width":1920,"outer_height":1080,"inner_width":1920,"inner_height":936,
					"device_pixel_ratio":1,"chrome_runtime_present":true,"prefers_color_scheme_dark":false
				},
				"automation":{"playwright_binding_present":false,"playwright_init_scripts_present":false},
				"screen":{"width":1920,"height":1080,"avail_width":1920,"avail_height":1040,"color_depth":24,"pixel_depth":24},
				"document":{"cookie_present":false,"cookie_length":0,"visibility_state":"visible","has_focus":true},
				"intl":{"timezone":"UTC"},
				"webgl":{"available":true,"vendor":"Google Inc.","renderer":"ANGLE"}
			}
		}`),
		artifactName:        "product_json",
		artifactContentType: "application/json",
		artifactSize:        -1,
		taskStatus:          protocol.TaskSucceeded,
		proofStatus:         protocol.VerificationAccepted,
	}
	t.Cleanup(func() {
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		if err := errors.Join(fixture.handlerErrors...); err != nil {
			t.Errorf("compute fixture handler: %v", err)
		}
	})
	return fixture
}

func (f *computeFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer scoped-task-token" {
		w.WriteHeader(http.StatusUnauthorized)
		f.writeJSON(w, map[string]string{"error": "missing scoped bearer token"})
		return
	}
	requestKey := r.Method + " " + r.URL.Path
	if f.transientFailures[requestKey] > 0 {
		f.transientFailures[requestKey]--
		w.WriteHeader(http.StatusServiceUnavailable)
		f.writeJSON(w, map[string]string{"error": "auth state busy"})
		return
	}
	if status := f.permanentFailures[requestKey]; status != 0 {
		w.WriteHeader(status)
		f.writeJSON(w, map[string]string{"error": "request rejected"})
		return
	}
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/tasks/") && strings.HasSuffix(r.URL.Path, "/artifacts") && f.transientArtifactListFailures > 0 {
		f.transientArtifactListFailures--
		w.WriteHeader(http.StatusServiceUnavailable)
		f.writeJSON(w, map[string]string{"error": "artifact index busy"})
		return
	}
	if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/proofs/") && strings.Contains(r.URL.Path, "/artifacts/") && f.transientArtifactDownloadFailures > 0 {
		f.transientArtifactDownloadFailures--
		w.WriteHeader(http.StatusServiceUnavailable)
		f.writeJSON(w, map[string]string{"error": "artifact store busy"})
		return
	}

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/agents":
		f.writeJSON(w, map[string]any{"agents": f.agents, "summary": map[string]int{}})
	case r.Method == http.MethodGet && r.URL.Path == "/v1/leases":
		f.writeJSON(w, map[string]any{"leases": f.leases})
	case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks":
		tasks := append([]protocol.Task(nil), f.queued...)
		for _, submitted := range f.submitted {
			submitted.Status = f.taskStatus
			if f.mutateTerminalTask != nil {
				f.mutateTerminalTask(&submitted)
			}
			tasks = append(tasks, submitted)
		}
		f.writeJSON(w, protocol.TaskList{Tasks: tasks})
		if f.afterCapacity != nil && len(f.submitted) == 0 {
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			f.afterCapacity()
			f.afterCapacity = nil
		}
	case r.Method == http.MethodPost && r.URL.Path == "/v1/tasks":
		var task protocol.Task
		if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
			f.handlerErrors = append(f.handlerErrors, fmt.Errorf("decode submitted task: %w", err))
			http.Error(w, "invalid task", http.StatusBadRequest)
			return
		}
		f.submitted = append(f.submitted, task)
		created := task
		if f.mutateSubmittedResponse != nil {
			f.mutateSubmittedResponse(&created)
		}
		w.WriteHeader(http.StatusCreated)
		f.writeJSON(w, protocol.TaskResponse{Task: created})
	case r.Method == http.MethodGet && r.URL.Path == "/v1/proofs":
		proofs := make([]protocol.ProofReceipt, 0, len(f.submitted))
		for _, task := range f.submitted {
			finishedAt := time.Now().UTC()
			leasedTask := task
			if f.mutateTerminalTask != nil {
				f.mutateTerminalTask(&leasedTask)
			}
			leasedTask.Status = protocol.TaskLeased
			artifactName, artifactBody := "product_json", f.productBody
			if task.Workload.Provider != nil && task.Workload.Provider.Operation == "browser_diagnostic" {
				artifactName, artifactBody = "browser_diagnostic_json", f.diagnosticBody
			}
			proof := protocol.ProofReceipt{
				ID: "proof-" + task.ID, TaskID: task.ID, WorkerID: "worker-1",
				OrgID: task.OrgID, PoolID: task.PoolID, PolicyID: task.PolicyID,
				TaskHash: protocol.CanonicalHash(leasedTask), InputHash: task.InputHash,
				DependencyClosureHash: protocol.CanonicalHash(task.Workload),
				Executor: protocol.ExecutorRef{
					Provider: "product-capture-browser", Version: "v0.1.60",
					ExecutionSecurityTier: protocol.ExecutionSandboxedContainer,
					ProofTier:             protocol.ProofArtifactHash, ImageDigest: testImageDigest,
					RootFSDigest: sha256RefForTest([]byte("rootfs")),
				},
				StartedAt: finishedAt.Add(-time.Second), FinishedAt: finishedAt,
				ArtifactHash: aggregateArtifactHashForTest(map[string][]byte{artifactName: artifactBody}),
				Verifier:     protocol.VerifierResult{Provider: "signed_receipt", Status: f.proofStatus},
			}
			proof.AgentSignature = protocol.SoftwareAgentProofSignature(proof)
			if f.mutateProof != nil {
				f.mutateProof(&proof)
			}
			proofs = append(proofs, proof)
		}
		f.writeJSON(w, protocol.ProofList{Proofs: proofs})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/tasks/") && strings.HasSuffix(r.URL.Path, "/artifacts"):
		task, ok := f.taskFromPath(r.URL.Path)
		if !ok {
			http.NotFound(w, r)
			return
		}
		body := f.productBody
		name := f.artifactName
		contentType := f.artifactContentType
		isDiagnostic := task.Workload.Provider != nil && task.Workload.Provider.Operation == "browser_diagnostic"
		if isDiagnostic {
			body = f.diagnosticBody
			name = "browser_diagnostic_json"
			contentType = "application/json"
		}
		size := int64(len(body))
		if !isDiagnostic && f.artifactSize >= 0 {
			size = f.artifactSize
		}
		sha := sha256RefForTest(body)
		if !isDiagnostic && f.artifactSHA256 != "" {
			sha = f.artifactSHA256
		}
		proofID := "proof-" + task.ID
		f.writeJSON(w, map[string]any{"artifacts": []protocol.TaskArtifact{{
			TaskID: task.ID, ProofID: proofID, PoolID: task.PoolID,
			Name: name, Ref: fmt.Sprintf("artifact://%s/tasks/%s/proofs/%s/artifacts/%s", task.PoolID, task.ID, proofID, name),
			ContentType: contentType, SHA256: sha, SizeBytes: size,
			CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(time.Hour).UTC(),
		}}})
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/proofs/") && strings.Contains(r.URL.Path, "/artifacts/"):
		body := f.productBody
		for _, task := range f.submitted {
			if strings.Contains(r.URL.Path, "/"+task.ID+"/") && task.Workload.Provider != nil && task.Workload.Provider.Operation == "browser_diagnostic" {
				body = f.diagnosticBody
				break
			}
		}
		_, _ = w.Write(body)
	default:
		f.handlerErrors = append(f.handlerErrors, fmt.Errorf("unexpected request: %s %s", r.Method, r.URL.Path))
		http.Error(w, "unexpected request", http.StatusNotFound)
	}
}

func (f *computeFixture) taskFromPath(path string) (protocol.Task, bool) {
	for _, task := range f.submitted {
		if path == "/v1/tasks/"+task.ID+"/artifacts" {
			return task, true
		}
	}
	return protocol.Task{}, false
}

func (f *computeFixture) productSHA256() string {
	return sha256RefForTest(f.productBody)
}

func sha256RefForTest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func aggregateArtifactHashForTest(artifacts map[string][]byte) string {
	names := make([]string, 0, len(artifacts))
	for name := range artifacts {
		names = append(names, name)
	}
	slices.Sort(names)
	hash := sha256.New()
	for _, name := range names {
		_, _ = fmt.Fprintf(hash, "%s\x00", name)
		_, _ = hash.Write(artifacts[name])
		_, _ = hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func TestProviderArtifactHashMatchesWorkflowComputeGoldenVectors(t *testing.T) {
	tests := []struct {
		name      string
		artifacts map[string][]byte
		want      string
	}{
		{
			name:      "provider artifact",
			artifacts: map[string][]byte{"product_json": []byte(`{"price":"19.99"}`)},
			want:      "sha256:e7a2989587866e81c5c743d66c5f4bbec2c210b8194043ddf1c51b80b974e8fd",
		},
		{
			name:      "sorted artifacts",
			artifacts: map[string][]byte{"z.json": []byte("Z"), "a.json": []byte("A")},
			want:      "sha256:8a8d57a7ece4ab725f22825673c95ad4a93145f723654718175321e8f0c16f23",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := providerArtifactHash(tc.artifacts); got != tc.want {
				t.Fatalf("provider artifact hash = %q, want workflow-compute golden %q", got, tc.want)
			}
		})
	}
}

func productJSON() []byte {
	return []byte(`{
			"provider":"browser_capture",
		"provider_version":"v1",
		"merchant":"Amazon",
			"url":"https://www.amazon.com/dp/B000000000",
			"requested_url":"https://www.amazon.com/dp/B000000000",
			"canonical_url":"https://www.amazon.com/dp/B000000000",
			"external_id":"B000000000",
		"title":"Example product",
		"variant_key":"exact-url-sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"price":"19.99",
		"currency":"USD",
		"image_url":"https://images.example.test/product.jpg",
		"captured_at":"2026-07-13T12:00:00Z",
		"requires_user_confirmation":true
	}`)
}

func setProductField(fixture *computeFixture, name string, value any) {
	var product map[string]any
	if err := json.Unmarshal(fixture.productBody, &product); err != nil {
		panic(err)
	}
	product[name] = value
	data, err := json.Marshal(product)
	if err != nil {
		panic(err)
	}
	fixture.productBody = data
}

func setDiagnosticField(fixture *computeFixture, name string, value any) {
	var diagnostic map[string]any
	if err := json.Unmarshal(fixture.diagnosticBody, &diagnostic); err != nil {
		panic(err)
	}
	diagnostic[name] = value
	data, err := json.Marshal(diagnostic)
	if err != nil {
		panic(err)
	}
	fixture.diagnosticBody = data
}

func setDiagnosticSignalSection(fixture *computeFixture, name string, value any) {
	var diagnostic map[string]any
	if err := json.Unmarshal(fixture.diagnosticBody, &diagnostic); err != nil {
		panic(err)
	}
	signals := diagnostic["browser_signals"].(map[string]any)
	signals[name] = value
	data, err := json.Marshal(diagnostic)
	if err != nil {
		panic(err)
	}
	fixture.diagnosticBody = data
}

func setDiagnosticSignalField(fixture *computeFixture, section, name string, value any) {
	var diagnostic map[string]any
	if err := json.Unmarshal(fixture.diagnosticBody, &diagnostic); err != nil {
		panic(err)
	}
	signals := diagnostic["browser_signals"].(map[string]any)
	fields := signals[section].(map[string]any)
	fields[name] = value
	data, err := json.Marshal(diagnostic)
	if err != nil {
		panic(err)
	}
	fixture.diagnosticBody = data
}

func (f *computeFixture) writeJSON(w http.ResponseWriter, value any) {
	if err := json.NewEncoder(w).Encode(value); err != nil {
		f.handlerErrors = append(f.handlerErrors, fmt.Errorf("encode response: %w", err))
	}
}

func countRequests(requests []string, want string) int {
	count := 0
	for _, request := range requests {
		if request == want {
			count++
		}
	}
	return count
}
