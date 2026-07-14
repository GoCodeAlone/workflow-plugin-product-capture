package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-compute-core/protocol"
	"github.com/GoCodeAlone/workflow-plugin-product-capture/internal/stagingproof"
)

const maxSummaryBytes = 64 << 10

type proofRunner func(context.Context, stagingproof.Config) (stagingproof.Summary, error)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Getenv, stagingproof.Run); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "product capture staging proof: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout io.Writer, getenv func(string) string, execute proofRunner) error {
	fs := flag.NewFlagSet("product-capture-staging-proof", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	serverURL := fs.String("server", "", "workflow-compute server URL")
	tokenEnv := fs.String("token-env", "WORKFLOW_COMPUTE_TASK_TOKEN", "environment variable containing the scoped task token")
	orgID := fs.String("org", "", "organization ID")
	poolID := fs.String("pool", "", "pool ID")
	productID := fs.String("product", "", "network product ID")
	policyID := fs.String("policy", "", "task policy ID")
	workerID := fs.String("worker-id", "", "retained product-capture worker ID")
	productURL := fs.String("product-url", "", "product URL")
	allowedHost := fs.String("allowed-host", "", "exact allowed product host")
	diagnosticURL := fs.String("browser-diagnostic-url", "", "optional controlled HTTPS browser diagnostic endpoint")
	imageRef := fs.String("provider-image-ref", "", "exact digest-pinned provider image reference")
	contractPath := fs.String("contract", "contracts/product-capture-provider.json", "provider contract path")
	productSchemaPath := fs.String("product-schema", "schemas/product-capture-operation-output.schema.json", "product output schema path")
	diagnosticSchemaPath := fs.String("diagnostic-schema", "schemas/browser-diagnostic-result.schema.json", "browser diagnostic artifact schema path")
	outputPath := fs.String("output", "product-capture-staging-proof.json", "redacted summary output path")
	pollInterval := fs.Duration("poll-interval", 30*time.Second, "control-plane poll interval")
	capacityTimeout := fs.Duration("capacity-timeout", 30*time.Minute, "capacity wait timeout")
	resultTimeout := fs.Duration("result-timeout", 30*time.Minute, "per-task result timeout")
	artifactTimeout := fs.Duration("artifact-timeout", 5*time.Minute, "per-task artifact validation timeout")
	taskTimeout := fs.Int("task-timeout-seconds", 300, "compute task timeout in seconds, including provider result reporting")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if strings.TrimSpace(*tokenEnv) == "" || strings.TrimSpace(*tokenEnv) != *tokenEnv {
		return errors.New("token-env must name one environment variable")
	}
	token := getenv(*tokenEnv)
	if token == "" {
		return fmt.Errorf("scoped task token environment variable %s is empty", *tokenEnv)
	}

	contractData, err := os.ReadFile(*contractPath)
	if err != nil {
		return fmt.Errorf("read provider contract: %w", err)
	}
	var contract protocol.ProviderContract
	if err := protocol.DecodeStrict(strings.NewReader(string(contractData)), &contract); err != nil {
		return fmt.Errorf("decode provider contract: %w", err)
	}
	productSchema, err := os.ReadFile(*productSchemaPath)
	if err != nil {
		return fmt.Errorf("read product schema: %w", err)
	}
	var diagnosticSchema []byte
	if *diagnosticURL != "" {
		diagnosticSchema, err = os.ReadFile(*diagnosticSchemaPath)
		if err != nil {
			return fmt.Errorf("read browser diagnostic schema: %w", err)
		}
	}
	summary, err := execute(ctx, stagingproof.Config{
		ServerURL:            *serverURL,
		Token:                token,
		OrgID:                *orgID,
		PoolID:               *poolID,
		ProductID:            *productID,
		PolicyID:             *policyID,
		WorkerID:             *workerID,
		ProductURL:           *productURL,
		AllowedHost:          *allowedHost,
		BrowserDiagnosticURL: *diagnosticURL,
		ProviderImageRef:     *imageRef,
		Contract:             contract,
		ProductSchema:        productSchema,
		DiagnosticSchema:     diagnosticSchema,
		PollInterval:         *pollInterval,
		CapacityTimeout:      *capacityTimeout,
		ResultTimeout:        *resultTimeout,
		ArtifactTimeout:      *artifactTimeout,
		TaskTimeoutSeconds:   *taskTimeout,
	})
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return fmt.Errorf("encode redacted summary: %w", err)
	}
	data = append(data, '\n')
	if len(data) > maxSummaryBytes {
		return fmt.Errorf("redacted summary exceeds %d bytes", maxSummaryBytes)
	}
	if err := writeRestricted(*outputPath, data); err != nil {
		return fmt.Errorf("write redacted summary: %w", err)
	}
	if _, err := stdout.Write(data); err != nil {
		return fmt.Errorf("write summary output: %w", err)
	}
	return nil
}

func writeRestricted(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
