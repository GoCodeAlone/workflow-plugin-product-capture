package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-compute-core/protocol"
	"github.com/GoCodeAlone/workflow-plugin-product-capture/internal/stagingproof"
)

func TestRunLoadsInputsExecutesProofAndWritesRestrictedSummary(t *testing.T) {
	output := filepath.Join(t.TempDir(), "proof.json")
	var stdout bytes.Buffer
	called := false
	execute := func(_ context.Context, cfg stagingproof.Config) (stagingproof.Summary, error) {
		called = true
		if cfg.ServerURL != "https://compute.example.test" || cfg.Token != "scoped-token" ||
			cfg.OrgID != "org-1" || cfg.PoolID != "pool-1" || cfg.ProductID != "bmw-product-capture" ||
			cfg.PolicyID != "capture-staging" || cfg.WorkerID != "worker-1" ||
			cfg.ProductURL != "https://www.amazon.com/dp/B000000000" || cfg.AllowedHost != "www.amazon.com" ||
			cfg.BrowserDiagnosticURL != "https://diagnostic.example.test/browser" ||
			cfg.ProviderImageRef != "ghcr.io/example/browser@sha256:"+strings.Repeat("a", 64) {
			t.Fatalf("config = %+v", cfg)
		}
		if cfg.Contract.ContractID != "product-capture.browser.v1" || len(cfg.ProductSchema) == 0 || len(cfg.DiagnosticSchema) == 0 {
			t.Fatalf("contract/schemas not loaded: %+v, %d, %d", cfg.Contract, len(cfg.ProductSchema), len(cfg.DiagnosticSchema))
		}
		if cfg.ArtifactTimeout != 17*time.Second {
			t.Fatalf("artifact timeout = %s, want 17s", cfg.ArtifactTimeout)
		}
		return stagingproof.Summary{
			SchemaVersion: "product-capture-staging-proof.v1",
			TaskID:        "task-1", TaskStatus: protocol.TaskSucceeded,
			ProofID: "proof-1", ProofStatus: protocol.VerificationAccepted,
			WorkerID: "worker-1", ProviderImageRef: cfg.ProviderImageRef,
			Product: stagingproof.ProductSummary{Title: "Product", ImageURL: "https://images.example.test/p.jpg", Price: "$10.00"},
		}, nil
	}
	args := []string{
		"--server", "https://compute.example.test",
		"--token-env", "TEST_PROOF_TOKEN",
		"--org", "org-1",
		"--pool", "pool-1",
		"--product", "bmw-product-capture",
		"--policy", "capture-staging",
		"--worker-id", "worker-1",
		"--product-url", "https://www.amazon.com/dp/B000000000",
		"--allowed-host", "www.amazon.com",
		"--browser-diagnostic-url", "https://diagnostic.example.test/browser",
		"--provider-image-ref", "ghcr.io/example/browser@sha256:" + strings.Repeat("a", 64),
		"--contract", filepath.Join("..", "..", "contracts", "product-capture-provider.json"),
		"--product-schema", filepath.Join("..", "..", "schemas", "product-capture-operation-output.schema.json"),
		"--diagnostic-schema", filepath.Join("..", "..", "schemas", "browser-diagnostic-result.schema.json"),
		"--artifact-timeout", "17s",
		"--output", output,
	}
	getenv := func(name string) string {
		if name == "TEST_PROOF_TOKEN" {
			return "scoped-token"
		}
		return ""
	}
	if err := run(t.Context(), args, &stdout, getenv, execute); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !called {
		t.Fatal("proof was not executed")
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var summary stagingproof.Summary
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if summary.TaskID != "task-1" || !bytes.Equal(bytes.TrimSpace(stdout.Bytes()), bytes.TrimSpace(data)) {
		t.Fatalf("stdout/output mismatch: stdout=%s output=%s", stdout.Bytes(), data)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatalf("stat output: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("output mode = %o, want 600", info.Mode().Perm())
	}
}

func TestRunRejectsMissingScopedTokenBeforeExecution(t *testing.T) {
	called := false
	err := run(t.Context(), []string{
		"--server", "https://compute.example.test",
		"--token-env", "MISSING_TOKEN",
	}, &bytes.Buffer{}, func(string) string { return "" }, func(context.Context, stagingproof.Config) (stagingproof.Summary, error) {
		called = true
		return stagingproof.Summary{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "MISSING_TOKEN") {
		t.Fatalf("run error = %v, want missing token", err)
	}
	if called {
		t.Fatal("proof executed without scoped token")
	}
}

func TestRunProductOnlyDoesNotReadDiagnosticSchema(t *testing.T) {
	output := filepath.Join(t.TempDir(), "proof.json")
	executed := false
	err := run(t.Context(), []string{
		"--server", "https://compute.example.test",
		"--token-env", "TEST_PROOF_TOKEN",
		"--contract", filepath.Join("..", "..", "contracts", "product-capture-provider.json"),
		"--product-schema", filepath.Join("..", "..", "schemas", "product-capture-operation-output.schema.json"),
		"--diagnostic-schema", filepath.Join(t.TempDir(), "missing-diagnostic-schema.json"),
		"--output", output,
	}, &bytes.Buffer{}, func(string) string { return "scoped-token" }, func(_ context.Context, cfg stagingproof.Config) (stagingproof.Summary, error) {
		executed = true
		if len(cfg.DiagnosticSchema) != 0 {
			t.Fatalf("diagnostic schema bytes = %d, want 0", len(cfg.DiagnosticSchema))
		}
		return stagingproof.Summary{SchemaVersion: "product-capture-staging-proof.v1"}, nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !executed {
		t.Fatal("proof was not executed")
	}
}
